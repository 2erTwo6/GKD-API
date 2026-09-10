package engine

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gkd-api/internal/db"
)

// testUp is a scripted upstream: the test closes makeOK / makeErr to make the
// racer deliver a success / failure, or leaves it silent (pending). The engine
// must cancel its context when it loses the race.
type testUp struct {
	body    string
	weight  int
	makeOK  chan struct{}
	makeErr chan struct{}

	launched atomic.Bool
	canceled atomic.Bool
}

func newUp(weight int, body string) *testUp {
	return &testUp{
		weight:  weight,
		body:    body,
		makeOK:  make(chan struct{}),
		makeErr: make(chan struct{}),
	}
}

func newScriptedLauncher(ups []*testUp) Launcher {
	return func(ctx context.Context, c Candidate, sink chan<- Result) {
		u := ups[c.Idx]
		u.launched.Store(true)
		go func() {
			select {
			case <-ctx.Done():
				u.canceled.Store(true)
				sink <- Result{Cand: c, Err: ctx.Err()}
			case <-u.makeOK:
				sink <- Result{
					Cand:   c,
					Status: 200,
					Reader: bufio.NewReader(strings.NewReader(u.body)),
					Body:   io.NopCloser(strings.NewReader("")),
				}
			case <-u.makeErr:
				sink <- Result{Cand: c, Err: errors.New("HTTP 500: boom")}
			}
		}()
	}
}

func vmFor(size, waitMs int, pick string, maxMs int) db.VirtualModel {
	return db.VirtualModel{BatchSize: size, BatchWaitMs: waitMs, PickMode: pick, MaxWaitMs: maxMs}
}

func readBody(t *testing.T, res *Result) string {
	t.Helper()
	b, err := io.ReadAll(res.Reader)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return string(b)
}

// waitCond polls cond until it returns true or the timeout elapses.
func waitCond(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestFastestWinsAndCancelsLosers(t *testing.T) {
	slow := newUp(1, `{"from":"slow"}`)
	fast := newUp(1, `{"from":"fast"}`)
	ups := []*testUp{slow, fast}

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(fast.makeOK)
	}()

	res, err := Run(context.Background(), vmFor(0, 5000, "fastest", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != nil {
		t.Fatal(err)
	}
	if got := readBody(t, res); got != `{"from":"fast"}` {
		t.Fatalf("winner should be fast, got %s", got)
	}
	waitCond(t, 2*time.Second, slow.canceled.Load, "loser must be cancelled after winner is chosen")
}

func scriptCands(ups []*testUp) []Candidate {
	cands := make([]Candidate, len(ups))
	for i, u := range ups {
		cands[i] = Candidate{Idx: i, Real: db.RealModel{Name: "real-" + itoaN(i), Weight: u.weight}, Body: []byte(`{}`)}
	}
	return cands
}

func itoaN(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

func TestFastestWindowIsHardTimeout(t *testing.T) {
	a := newUp(1, `{"a":1}`)
	b := newUp(1, `{"b":2}`)
	ups := []*testUp{a, b}

	start := time.Now()
	_, err := Run(t.Context(), vmFor(0, 100, "fastest", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != ErrNoResponse {
		t.Fatalf("want ErrNoResponse, got %v", err)
	}
	if el := time.Since(start); el > 300*time.Millisecond {
		t.Fatalf("503 must fire at window end, took %v", el)
	}
	waitCond(t, 2*time.Second, func() bool { return a.canceled.Load() && b.canceled.Load() }, "all racers must be aborted on timeout")
}

func TestWeightPicksHighestWeightAmongResponders(t *testing.T) {
	low := newUp(1, `{"w":"low"}`)
	high := newUp(10, `{"w":"high"}`)
	ups := []*testUp{low, high}

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(low.makeOK)
		time.Sleep(80 * time.Millisecond)
		close(high.makeOK)
	}()

	res, err := Run(t.Context(), vmFor(0, 400, "weight", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != nil {
		t.Fatal(err)
	}
	if got := readBody(t, res); got != `{"w":"high"}` {
		t.Fatalf("weight mode must prefer weight-10 responder, got %s", got)
	}
}

func TestWeightKeepsWaitingWhenNobodyResponds(t *testing.T) {
	a := newUp(10, `{"a":1}`)
	b := newUp(1, `{"b":1}`)
	ups := []*testUp{a, b}

	// grace (50ms) expires with no responders; the only batch keeps waiting;
	// a responds at ~300ms and wins immediately.
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(a.makeOK)
	}()

	start := time.Now()
	res, err := Run(t.Context(), vmFor(0, 50, "weight", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 250*time.Millisecond || el > 2*time.Second {
		t.Fatalf("grace expired with no response: keep waiting until ~300ms, got %v", el)
	}
	if got := readBody(t, res); got != `{"a":1}` {
		t.Fatalf("bad body %s", got)
	}
}

func TestBatchesProgressByWeight(t *testing.T) {
	w40 := newUp(40, `{"m":40}`)
	w30 := newUp(30, `{"m":30}`)
	w20 := newUp(20, `{"m":20}`)
	w10 := newUp(10, `{"m":10}`)
	ups := []*testUp{w40, w30, w20, w10}

	// batch1 [w40,w30] stays silent; window(100ms) aborts it; batch2 [w20,w10]
	// launched; w20 answers at ~30ms into batch 2 and wins.
	go func() {
		time.Sleep(100 + 30*time.Millisecond)
		close(w20.makeOK)
	}()

	start := time.Now()
	res, err := Run(t.Context(), vmFor(2, 100, "fastest", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("batch1 window(100ms) must elapse before batch 2, got %v", el)
	}
	if got := readBody(t, res); got != `{"m":20}` {
		t.Fatalf("winner should be w20 of batch 2, got %s", got)
	}
	waitCond(t, 2*time.Second, func() bool { return w40.canceled.Load() && w30.canceled.Load() }, "aborted batch1 racers must be cancelled")
}

func TestWeightBatchPicksBestOfBatchOnly(t *testing.T) {
	w5 := newUp(5, `{"m":5}`)
	w4 := newUp(4, `{"m":4}`)
	w2 := newUp(2, `{"m":2}`)
	ups := []*testUp{w5, w4, w2}

	// both batch1 members answer before the window (200ms) ends.
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(w5.makeOK)
		close(w4.makeOK)
	}()

	res, err := Run(t.Context(), vmFor(2, 200, "weight", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != nil {
		t.Fatal(err)
	}
	if got := readBody(t, res); got != `{"m":5}` {
		t.Fatalf("batch1 [w5,w4] window(200ms) has responders, pick weight5; got %s", got)
	}
	if w2.launched.Load() {
		t.Fatal("batch2 must never be launched when batch1 already produced a winner")
	}
	// never launched -> its cancel func exists (created at dispatch) but the
	// racer goroutine never ran, so canceled may stay false; nothing to wait for.
}

func TestFailedUpstreamsAreSkipped(t *testing.T) {
	bad1 := newUp(5, `x`)
	bad2 := newUp(4, `x`)
	good := newUp(1, `{"ok":true}`)
	ups := []*testUp{bad1, bad2, good}

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(bad1.makeErr)
		close(bad2.makeErr)
		close(good.makeOK)
	}()

	res, err := Run(t.Context(), vmFor(0, 5000, "fastest", 60000), scriptCands(ups), newScriptedLauncher(ups))
	if err != nil {
		t.Fatal(err)
	}
	if got := readBody(t, res); got != `{"ok":true}` {
		t.Fatalf("winner should be the good upstream, got %s", got)
	}
}

func TestAllUpstreamsFail(t *testing.T) {
	bad1 := newUp(1, `x`)
	bad2 := newUp(1, `x`)
	ups := []*testUp{bad1, bad2}
	go func() {
		close(bad1.makeErr)
		close(bad2.makeErr)
	}()

	if _, err := Run(t.Context(), vmFor(0, 5000, "fastest", 60000), scriptCands(ups), newScriptedLauncher(ups)); err != ErrNoResponse {
		t.Fatalf("want ErrNoResponse, got %v", err)
	}
}

func TestNoCandidate(t *testing.T) {
	if _, err := Run(t.Context(), vmFor(0, 100, "fastest", 60000), nil, newScriptedLauncher(nil)); err != ErrNoCandidate {
		t.Fatalf("want ErrNoCandidate, got %v", err)
	}
}

func TestMaxWaitBound(t *testing.T) {
	slow := newUp(1, `x`)
	ups := []*testUp{slow}

	start := time.Now()
	_, err := Run(t.Context(), vmFor(0, 100, "weight", 200), scriptCands(ups), newScriptedLauncher(ups))
	if err != ErrNoResponse {
		t.Fatalf("want ErrNoResponse, got %v", err)
	}
	if el := time.Since(start); el > 1*time.Second {
		t.Fatalf("max_wait_ms must bound the contest, got %v", el)
	}
}
