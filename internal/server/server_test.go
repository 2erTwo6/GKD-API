package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gkd-api/internal/db"
)

type captured struct {
	Path   string
	Header http.Header
	Body   map[string]any
}

func newTestServer(t *testing.T) (*httptest.Server, *gorm.DB) {
	t.Helper()
	g, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Bootstrap(g, "test-pass-123"); err != nil {
		t.Fatal(err)
	}
	r, err := New(g)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, g
}

// fakeOpenAI serves an OpenAI-compatible /chat/completions endpoint and
// captures every request before honoring the (configurable) delay.
func fakeOpenAI(t *testing.T, delay time.Duration, streamOut bool) (*httptest.Server, <-chan captured) {
	t.Helper()
	ch := make(chan captured, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		ch <- captured{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if streamOut {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"id\":\"u1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
			io.WriteString(w, "data: {\"id\":\"u1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\"},\"finish_reason\":null}]}\n\n")
			io.WriteString(w, "data: {\"id\":\"u1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"世界\"},\"finish_reason\":\"stop\"}]}\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"u1","object":"chat.completion","model":"gpt-x","choices":[{"index":0,"message":{"role":"assistant","content":"你好世界"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, ch
}

// fakeGemini serves a Gemini-compatible endpoint.
func fakeGemini(t *testing.T) (*httptest.Server, <-chan captured) {
	t.Helper()
	ch := make(chan captured, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		ch <- captured{Path: r.URL.Path + "?" + r.URL.RawQuery, Header: r.Header.Clone(), Body: body}
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, ":generateContent"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"来自Gemini的回答"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":6,"totalTokenCount":9}}`)
		case strings.HasSuffix(p, ":streamGenerateContent"):
			if r.URL.Query().Get("alt") != "sse" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"来自\"}],\"role\":\"model\"}}]}\n\n")
			io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Gemini\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":5,\"totalTokenCount\":8}}\n\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, ch
}

func postJSON(t *testing.T, url, key, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func adminCall(t *testing.T, srv *httptest.Server, token, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func numStr(i uint) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func getToken(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	code, body := adminCall(t, srv, "", http.MethodPost, "/api/admin/login", `{"username":"admin","password":"test-pass-123"}`)
	if code != 200 {
		t.Fatalf("login: %d %s", code, body)
	}
	var m struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil || m.Token == "" {
		t.Fatalf("no token: %s", body)
	}
	return m.Token
}

func createKey(t *testing.T, srv *httptest.Server, token string) (string, uint) {
	t.Helper()
	code, body := adminCall(t, srv, token, http.MethodPost, "/api/admin/api-keys", `{"name":"test"}`)
	if code != 200 {
		t.Fatalf("create key: %d %s", code, body)
	}
	var ak struct {
		Key string `json:"key"`
		ID  uint   `json:"id"`
	}
	json.Unmarshal([]byte(body), &ak)
	return ak.Key, ak.ID
}

func createVirtualModel(t *testing.T, srv *httptest.Server, token, payload string) uint {
	t.Helper()
	code, body := adminCall(t, srv, token, http.MethodPost, "/api/admin/virtual-models", payload)
	if code != 200 {
		t.Fatalf("create vm: %d %s", code, body)
	}
	var vm struct {
		ID uint `json:"id"`
	}
	json.Unmarshal([]byte(body), &vm)
	return vm.ID
}

func addRealModel(t *testing.T, srv *httptest.Server, token string, vmID uint, payload string) uint {
	t.Helper()
	code, body := adminCall(t, srv, token, http.MethodPost,
		"/api/admin/virtual-models/"+numStr(vmID)+"/real-models", payload)
	if code != 200 {
		t.Fatalf("create rm: %d %s", code, body)
	}
	var rm struct {
		ID uint `json:"id"`
	}
	json.Unmarshal([]byte(body), &rm)
	return rm.ID
}

func TestAdminLoginAndGuards(t *testing.T) {
	srv, _ := newTestServer(t)

	if code, _ := adminCall(t, srv, "", http.MethodPost, "/api/admin/login", `{"username":"admin","password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password must 401, got %d", code)
	}
	if code, _ := adminCall(t, srv, "", http.MethodGet, "/api/admin/virtual-models", ""); code != http.StatusUnauthorized {
		t.Fatalf("missing token must 401, got %d", code)
	}
	token := getToken(t, srv)
	if code, body := adminCall(t, srv, token, http.MethodGet, "/api/admin/me", ""); code != 200 || !strings.Contains(body, "admin") {
		t.Fatalf("me: %d %s", code, body)
	}
}

func TestChatOpenAIUpstreamNonStream(t *testing.T) {
	srv, _ := newTestServer(t)
	token := getToken(t, srv)
	key, _ := createKey(t, srv, token)
	vmID := createVirtualModel(t, srv, token, `{"name":"free","pick_mode":"fastest"}`)

	up, upCh := fakeOpenAI(t, 0, false)
	addRealModel(t, srv, token, vmID, `{"name":"gpt-x","base_url":"`+up.URL+`","api_key":"sk-up","protocol":"openai","weight":3}`)

	code, respBody := postJSON(t, srv.URL+"/v1/chat/completions", key,
		`{"model":"free","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}]}`)
	if code != 200 {
		t.Fatalf("chat: %d %s", code, respBody)
	}
	var completion map[string]any
	json.Unmarshal([]byte(respBody), &completion)
	if completion["model"] != "free" || completion["object"] != "chat.completion" {
		t.Fatalf("bad envelope: %v", completion)
	}
	msg := completion["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "你好世界" || msg["role"] != "assistant" {
		t.Fatalf("bad message: %v", msg)
	}
	if completion["usage"] == nil {
		t.Fatal("usage should be relayed")
	}

	upReq := <-upCh
	if upReq.Body["model"] != "gpt-x" {
		t.Fatalf("upstream must see real model: %v", upReq.Body["model"])
	}
	if !strings.Contains(upReq.Header.Get("Authorization"), "sk-up") {
		t.Fatalf("missing upstream auth: %v", upReq.Header)
	}
}

func TestChatOpenAIUpstreamStream(t *testing.T) {
	srv, _ := newTestServer(t)
	token := getToken(t, srv)
	key, _ := createKey(t, srv, token)
	vmID := createVirtualModel(t, srv, token, `{"name":"free","pick_mode":"fastest"}`)

	up, _ := fakeOpenAI(t, 0, true)
	addRealModel(t, srv, token, vmID, `{"name":"gpt-x","base_url":"`+up.URL+`","api_key":"sk-up","protocol":"openai","weight":3}`)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"free","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("bad stream response: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	raw, _ := io.ReadAll(res.Body)
	s := string(raw)
	if !strings.Contains(s, `"chat.completion.chunk"`) ||
		!strings.Contains(s, `"content":"你好"`) ||
		!strings.Contains(s, `"content":"世界"`) ||
		!strings.Contains(s, `"role":"assistant"`) ||
		!strings.Contains(s, "data: [DONE]") {
		t.Fatalf("bad stream body: %s", s)
	}
}

func TestChatGeminiUpstreamNonStream(t *testing.T) {
	srv, _ := newTestServer(t)
	token := getToken(t, srv)
	key, _ := createKey(t, srv, token)
	vmID := createVirtualModel(t, srv, token, `{"name":"gmix","pick_mode":"fastest"}`)

	up, upCh := fakeGemini(t)
	addRealModel(t, srv, token, vmID, `{"name":"gm-x","base_url":"`+up.URL+`","api_key":"gkey","protocol":"gemini","weight":3}`)

	code, respBody := postJSON(t, srv.URL+"/v1/chat/completions", key,
		`{"model":"gmix","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}],"max_tokens":100}`)
	if code != 200 {
		t.Fatalf("chat: %d %s", code, respBody)
	}
	var completion map[string]any
	json.Unmarshal([]byte(respBody), &completion)
	if completion["model"] != "gmix" || completion["object"] != "chat.completion" {
		t.Fatalf("bad envelope: %v", completion)
	}
	choice := completion["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Fatalf("bad finish: %v", choice["finish_reason"])
	}
	if choice["message"].(map[string]any)["content"] != "来自Gemini的回答" {
		t.Fatalf("bad content: %v", choice)
	}
	if completion["usage"].(map[string]any)["total_tokens"] != float64(9) {
		t.Fatalf("bad usage: %v", completion["usage"])
	}

	upReq := <-upCh
	if !strings.Contains(upReq.Path, "/v1beta/models/gm-x:generateContent") {
		t.Fatalf("bad gemini path: %s", upReq.Path)
	}
	if upReq.Header.Get("x-goog-api-key") != "gkey" {
		t.Fatalf("bad gemini auth header: %v", upReq.Header)
	}
	first := upReq.Body["contents"].([]any)[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("bad role: %v", first)
	}
	if first["parts"].([]any)[0].(map[string]any)["text"] != "hi" {
		t.Fatalf("bad parts: %v", first["parts"])
	}
	sys := upReq.Body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if sys["text"] != "sys" {
		t.Fatalf("bad systemInstruction: %v", sys)
	}
	cfg := upReq.Body["generationConfig"].(map[string]any)
	if cfg["maxOutputTokens"] != float64(100) {
		t.Fatalf("bad generationConfig: %v", cfg)
	}
}

func TestChatGeminiUpstreamStream(t *testing.T) {
	srv, _ := newTestServer(t)
	token := getToken(t, srv)
	key, _ := createKey(t, srv, token)
	vmID := createVirtualModel(t, srv, token, `{"name":"gmix","pick_mode":"fastest"}`)

	up, _ := fakeGemini(t)
	addRealModel(t, srv, token, vmID, `{"name":"gm-x","base_url":"`+up.URL+`","api_key":"gkey","protocol":"gemini","weight":3}`)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"gmix","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("stream status %d", res.StatusCode)
	}
	raw, _ := io.ReadAll(res.Body)
	s := string(raw)
	if !strings.Contains(s, `"content":"来自"`) ||
		!strings.Contains(s, `"content":"Gemini"`) ||
		!strings.Contains(s, `"role":"assistant"`) ||
		!strings.Contains(s, `"finish_reason":"stop"`) ||
		!strings.Contains(s, "data: [DONE]") {
		t.Fatalf("bad converted chunks: %s", s)
	}
}

func TestChatFailures(t *testing.T) {
	srv, _ := newTestServer(t)
	token := getToken(t, srv)
	key, _ := createKey(t, srv, token)

	if code, _ := postJSON(t, srv.URL+"/v1/chat/completions", key, `{"model":"nope","messages":[]}`); code != http.StatusNotFound {
		t.Fatalf("unknown model must 404, got %d", code)
	}

	createVirtualModel(t, srv, token, `{"name":"empty"}`)
	if code, _ := postJSON(t, srv.URL+"/v1/chat/completions", key, `{"model":"empty","messages":[]}`); code != http.StatusServiceUnavailable {
		t.Fatalf("empty vm must 503, got %d", code)
	}

	vmID := createVirtualModel(t, srv, token, `{"name":"slow","batch_wait_ms":100,"pick_mode":"fastest"}`)
	up, _ := fakeOpenAI(t, 1*time.Second, false)
	addRealModel(t, srv, token, vmID, `{"name":"gpt-x","base_url":"`+up.URL+`","api_key":"k","protocol":"openai","weight":1}`)
	start := time.Now()
	if code, _ := postJSON(t, srv.URL+"/v1/chat/completions", key, `{"model":"slow","messages":[]}`); code != http.StatusServiceUnavailable {
		t.Fatalf("slow upstream must 503, got %d", code)
	}
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("503 must fire at window end (~100ms), took %v", el)
	}

	vmID2 := createVirtualModel(t, srv, token, `{"name":"wait","batch_wait_ms":80,"pick_mode":"weight","max_wait_ms":200}`)
	up2, _ := fakeOpenAI(t, 1*time.Second, false)
	addRealModel(t, srv, token, vmID2, `{"name":"gpt-x","base_url":"`+up2.URL+`","api_key":"k","protocol":"openai","weight":1}`)
	start = time.Now()
	if code, _ := postJSON(t, srv.URL+"/v1/chat/completions", key, `{"model":"wait","messages":[]}`); code != http.StatusServiceUnavailable {
		t.Fatalf("weight keep-wait must eventually 503, got %d", code)
	}
	if el := time.Since(start); el > 1*time.Second {
		t.Fatalf("max_wait_ms must bound keep-waiting, got %v", el)
	}
}

func TestWeightModeAndOverride(t *testing.T) {
	srv, _ := newTestServer(t)
	token := getToken(t, srv)
	key, _ := createKey(t, srv, token)
	vmID := createVirtualModel(t, srv, token, `{"name":"mixed","pick_mode":"weight","batch_wait_ms":300}`)

	// fast but low weight, slow but high weight -> weight mode picks the slow one
	upFast, fastCh := fakeOpenAI(t, 20*time.Millisecond, false)
	upSlow, _ := fakeOpenAI(t, 150*time.Millisecond, false)
	addRealModel(t, srv, token, vmID, `{"name":"m-fast","base_url":"`+upFast.URL+`","api_key":"k","protocol":"openai","weight":1,"override_json":"{\"temperature\":0.3,\"reasoning_effort\":null}"}`)
	addRealModel(t, srv, token, vmID, `{"name":"m-slow","base_url":"`+upSlow.URL+`","api_key":"k","protocol":"openai","weight":10}`)

	code, respBody := postJSON(t, srv.URL+"/v1/chat/completions", key,
		`{"model":"mixed","temperature":0.9,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 {
		t.Fatalf("chat: %d %s", code, respBody)
	}
	if !strings.Contains(respBody, "你好世界") {
		t.Fatalf("winner must be the high-weight (slow) upstream: %s", respBody)
	}

	fastReq := <-fastCh
	if fastReq.Body["temperature"] != float64(0.3) {
		t.Fatalf("override must set temperature=0.3, got %v", fastReq.Body["temperature"])
	}
	if _, exists := fastReq.Body["reasoning_effort"]; exists {
		t.Fatalf("override null must delete reasoning_effort: %v", fastReq.Body)
	}
}
