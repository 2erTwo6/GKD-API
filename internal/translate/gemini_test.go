package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIToGeminiBasic(t *testing.T) {
	raw := map[string]any{
		"model":    "free",
		"messages": []any{map[string]any{"role": "user", "content": "你好"}},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Contents) != 1 || g.Contents[0].Role != "user" || g.Contents[0].Parts[0].Text != "你好" {
		b, _ := json.Marshal(g)
		t.Fatalf("bad: %s", b)
	}
	if g.SystemInstruction != nil {
		t.Fatal("unexpected system instruction")
	}
}

func TestOpenAIToGeminiSystemAndRoles(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "be nice"},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": "hello"},
			map[string]any{"role": "user", "content": "ok"},
		},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	if g.SystemInstruction == nil || g.SystemInstruction.Parts[0].Text != "be nice" {
		t.Fatal("system instruction missing")
	}
	if len(g.Contents) != 3 {
		t.Fatalf("want 3 contents, got %d", len(g.Contents))
	}
	if g.Contents[1].Role != "model" {
		t.Fatal("assistant should map to model role")
	}
	if g.Contents[2].Role != "user" {
		t.Fatal("role mismatch")
	}
}

func TestOpenAIToGeminiGenerationConfig(t *testing.T) {
	raw := map[string]any{
		"messages":        []any{},
		"temperature":     0.7,
		"top_p":           0.9,
		"max_tokens":      100,
		"stop":            []any{"END"},
		"response_format": map[string]any{"type": "json_object"},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := g.GenerationConfig
	if cfg == nil || cfg.Temperature != 0.7 || cfg.TopP != 0.9 || cfg.MaxOutputTokens != 100 {
		t.Fatalf("bad config: %+v", cfg)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "END" {
		t.Fatalf("bad stop: %+v", cfg.StopSequences)
	}
	if cfg.ResponseMIMEType != "application/json" {
		t.Fatalf("bad mime: %s", cfg.ResponseMIMEType)
	}
}

func TestOpenAIToGeminiMaxCompletionTokensPreferred(t *testing.T) {
	raw := map[string]any{
		"messages":              []any{},
		"max_completion_tokens": 55,
		"max_tokens":            100,
	}
	g, _ := OpenAIToGemini(raw)
	if g.GenerationConfig.MaxOutputTokens != 55 {
		t.Fatalf("want 55, got %d", g.GenerationConfig.MaxOutputTokens)
	}
}

func TestOpenAIToGeminiImageDataURL(t *testing.T) {
	raw := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "看图"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,QUJD"}},
		}}},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	parts := g.Contents[0].Parts
	if len(parts) != 2 || parts[1].InlineData == nil {
		t.Fatalf("bad parts: %+v", parts)
	}
	if parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != "QUJD" {
		t.Fatalf("bad inline data: %+v", parts[1].InlineData)
	}
}

func TestOpenAIToGeminiToolsRejected(t *testing.T) {
	raw := map[string]any{
		"messages": []any{},
		"tools":    []any{map[string]any{"type": "function"}},
	}
	if _, err := OpenAIToGemini(raw); err == nil {
		t.Fatal("want error for tools")
	}
}

func TestGeminiResponseToOpenAI(t *testing.T) {
	g := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content:      &GeminiContent{Parts: []GeminiPart{{Text: "回答"}, {Text: "内容"}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 3, CandidatesTokenCount: 5, TotalTokenCount: 8},
	}
	out := GeminiResponseToOpenAI("free", g)
	if out["model"] != "free" || out["object"] != "chat.completion" {
		t.Fatalf("bad envelope: %+v", out)
	}
	choices := out["choices"].([]any)
	ch := choices[0].(map[string]any)
	if ch["finish_reason"] != "stop" {
		t.Fatalf("bad finish: %v", ch["finish_reason"])
	}
	msg := ch["message"].(map[string]any)
	if msg["content"] != "回答内容" || msg["role"] != "assistant" {
		t.Fatalf("bad message: %+v", msg)
	}
	usage := out["usage"].(map[string]any)
	if usage["total_tokens"] != int64(8) {
		t.Fatalf("bad usage: %+v", usage)
	}
}

func TestGeminiChunkToOpenAI(t *testing.T) {
	roleSent := false
	// first chunk: content + no finish
	g1 := &GeminiResponse{Candidates: []GeminiCandidate{{
		Content: &GeminiContent{Parts: []GeminiPart{{Text: "你"}}},
	}}}
	b1, done, err := GeminiChunkToOpenAI("free", g1, &roleSent)
	if err != nil || done {
		t.Fatalf("chunk1 err=%v done=%v", err, done)
	}
	var c1 map[string]any
	json.Unmarshal(b1, &c1)
	if c1["object"] != "chat.completion.chunk" {
		t.Fatalf("bad object: %v", c1["object"])
	}
	d1 := c1["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if d1["role"] != "assistant" {
		t.Fatalf("first chunk must carry role: %v", d1)
	}

	// finish chunk
	g2 := &GeminiResponse{
		Candidates:    []GeminiCandidate{{Content: &GeminiContent{Parts: []GeminiPart{{Text: "好"}}}, FinishReason: "MAX_TOKENS"}},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 1, CandidatesTokenCount: 2, TotalTokenCount: 3},
	}
	b2, done, err := GeminiChunkToOpenAI("free", g2, &roleSent)
	if err != nil || !done {
		t.Fatalf("chunk2 err=%v done=%v", err, done)
	}
	var c2 map[string]any
	json.Unmarshal(b2, &c2)
	choice := c2["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "length" {
		t.Fatalf("want length, got %v", choice["finish_reason"])
	}
	if c2["usage"] == nil {
		t.Fatal("final chunk should include usage")
	}
}

func TestGeminiURL(t *testing.T) {
	if got := GeminiURL("https://x.com", "m", false); got != "https://x.com/v1beta/models/m:generateContent" {
		t.Fatal(got)
	}
	if got := GeminiURL("https://x.com/", "m", true); got != "https://x.com/v1beta/models/m:streamGenerateContent?alt=sse" {
		t.Fatal(got)
	}
	if got := GeminiURL("https://x.com/v1", "m", false); got != "https://x.com/v1/models/m:generateContent" {
		t.Fatal(got)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{"STOP": "stop", "MAX_TOKENS": "length", "SAFETY": "content_filter", "": "stop", "WHAT": "stop"}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}
	if !strings.Contains("x", "x") {
		t.Fatal("sanity")
	}
}
