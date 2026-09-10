package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gkd-api/internal/db"
)

type Candidate struct {
	Idx    int
	Real   db.RealModel
	Body   []byte
	Stream bool
}

// Result is a successful upstream response: HTTP 200 and at least one body byte.
type Result struct {
	Cand   Candidate
	Status int
	Header http.Header
	Reader *bufio.Reader
	Body   io.Closer
	Err    error
}

// Detail is the error string when Err != nil.
func (r *Result) Detail() string {
	if r.Err == nil {
		return ""
	}
	return r.Err.Error()
}

var (
	ErrNoResponse  = errors.New("所有真实模型均未响应")
	ErrNoCandidate = errors.New("没有可用的真实模型")
)

// Launcher starts an upstream request. It must not block; results are sent to
// sink exactly once. ctx is per-candidate: cancelling it aborts that request.
type Launcher func(ctx context.Context, c Candidate, sink chan<- Result)

// Run races candidates according to the virtual model's rule:
//   - candidates sorted by weight desc are dispatched in batches of vm.BatchSize
//     (<=0 means all at once);
//   - pick_mode "fastest": the first successful response wins immediately;
//   - pick_mode "weight": at each batch window's end the highest-weight
//     successful responder wins; if none has responded, keep waiting for the
//     first response when there is no next batch;
//   - max_wait_ms bounds the whole contest; on expiry -> ErrNoResponse.
//
// When Run returns with a winner, all losing requests have been aborted. The
// winner's connection stays alive after Run returns so the caller can keep
// streaming from it; the caller must eventually close Result.Body.
func Run(ctx context.Context, vm db.VirtualModel, cands []Candidate, launch Launcher) (*Result, error) {
	if len(cands) == 0 {
		return nil, ErrNoCandidate
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Real.Weight != cands[j].Real.Weight {
			return cands[i].Real.Weight > cands[j].Real.Weight
		}
		return cands[i].Idx < cands[j].Idx
	})

	maxWait := time.Duration(vm.MaxWaitMs) * time.Millisecond
	if maxWait <= 0 {
		maxWait = 60 * time.Second
	}
	batchWait := time.Duration(vm.BatchWaitMs) * time.Millisecond
	if batchWait <= 0 {
		batchWait = 3 * time.Second
	}
	size := vm.BatchSize
	if size <= 0 || size > len(cands) {
		size = len(cands)
	}

	// Timing context: bounds the whole contest but is detached from both the
	// client request (which may outlive the contest) and the racers (the
	// winner's connection must survive after Run returns).
	contest, cancelContest := context.WithTimeout(context.WithoutCancel(ctx), maxWait)
	defer cancelContest()

	// Each racer gets its own cancelable context on a detached parent.
	base := context.WithoutCancel(ctx)
	cancels := make([]context.CancelFunc, len(cands))
	var winner *Result
	defer func() {
		for i, c := range cancels {
			if c == nil {
				continue
			}
			if winner != nil && i == winner.Cand.Idx {
				continue // keep the winner's connection alive
			}
			c()
		}
	}()

	for lo := 0; lo < len(cands) && winner == nil && ctx.Err() == nil && contest.Err() == nil; lo += size {
		hi := lo + size
		if hi > len(cands) {
			hi = len(cands)
		}
		batch := cands[lo:hi]
		// Per-batch sink: aborted requests of previous batches must never
		// leak stale results into the next batch's contest.
		batchSink := make(chan Result, len(batch))
		for _, c := range batch {
			cctx, ccancel := context.WithCancel(base)
			cancels[c.Idx] = ccancel
			launch(cctx, c, batchSink)
		}
		hasNext := hi < len(cands)
		winner = raceBatch(ctx, contest, vm, batch, batchSink, hasNext, cancels)
	}

	if winner == nil {
		return nil, ErrNoResponse
	}
	return winner, nil
}

// raceBatch launches one batch and returns a winner or nil.
// If hasNext is true and the window expires without a usable response, the
// batch is aborted (contexts cancelled) and nil is returned so the next batch
// can be dispatched. If hasNext is false, the batch waits until the contest
// deadline for the first response (weight mode keeps grace semantics).
func raceBatch(ctx, contest context.Context, vm db.VirtualModel, batch []Candidate, sink <-chan Result, hasNext bool, cancels []context.CancelFunc) *Result {
	batchWait := time.Duration(vm.BatchWaitMs) * time.Millisecond
	if batchWait <= 0 {
		batchWait = 3 * time.Second
	}
	weightMode := vm.PickMode == "weight"

	var results []*Result // successful responders of this batch (weight mode)
	inFlight := len(batch)
	window := time.NewTimer(batchWait)
	defer window.Stop()

	abortBatch := func() {
		for _, c := range batch {
			if cf := cancels[c.Idx]; cf != nil {
				cf()
			}
		}
	}

	for inFlight > 0 {
		select {
		case r := <-sink:
			if r.Err != nil {
				inFlight--
				continue
			}
			if !weightMode {
				return &r
			}
			results = append(results, &r)
			inFlight--
		case <-window.C:
			if weightMode && len(results) > 0 {
				return bestWeighted(results)
			}
			if hasNext {
				abortBatch()
				return nil
			}
			if weightMode {
				// Grace expired with no responder: keep waiting for the
				// first response, bounded by the contest deadline.
				for inFlight > 0 {
					select {
					case r := <-sink:
						if r.Err != nil {
							inFlight--
							continue
						}
						return &r
					case <-contest.Done():
						return nil
					}
				}
				return nil
			}
			// fastest mode, last batch, window expired: hard timeout.
			return nil
		case <-contest.Done():
			return nil
		case <-ctx.Done(): // client gone: abort everything
			for _, c := range batch {
				if cf := cancels[c.Idx]; cf != nil {
					cf()
				}
			}
			return nil
		}
	}

	// Everyone in this batch finished without a usable response.
	if weightMode && len(results) > 0 {
		return bestWeighted(results)
	}
	return nil
}

func bestWeighted(results []*Result) *Result {
	var best *Result
	for _, r := range results {
		if best == nil || r.Cand.Real.Weight > best.Cand.Real.Weight {
			best = r
		}
	}
	return best
}

// HTTPDoLauncher is a Launcher backed by an http.Client. It treats a response
// as usable when status is 200 and the first body byte has been read.
func HTTPDoLauncher(client *http.Client) Launcher {
	return func(ctx context.Context, c Candidate, sink chan<- Result) {
		go func() {
			res := doUpstream(ctx, client, c)
			select {
			case sink <- res:
			default:
			}
		}()
	}
}

func doUpstream(ctx context.Context, client *http.Client, c Candidate) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, UpstreamURL(c), bytes.NewReader(c.Body))
	if err != nil {
		return Result{Cand: c, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	ApplyAuth(req, c)
	resp, err := client.Do(req)
	if err != nil {
		return Result{Cand: c, Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		return Result{Cand: c, Err: errors.New("HTTP " + strconv.Itoa(resp.StatusCode) + ": " + strings.TrimSpace(string(snippet)))}
	}
	rd := bufio.NewReaderSize(resp.Body, 8192)
	if _, err := rd.ReadByte(); err != nil {
		resp.Body.Close()
		return Result{Cand: c, Err: errors.New("上游返回空响应: " + err.Error())}
	}
	if err := rd.UnreadByte(); err != nil {
		resp.Body.Close()
		return Result{Cand: c, Err: err}
	}
	return Result{Cand: c, Status: resp.StatusCode, Header: resp.Header, Reader: rd, Body: resp.Body}
}
