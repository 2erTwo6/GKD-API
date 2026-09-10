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

func TestOpenAIToGeminiNonFunctionToolRejected(t *testing.T) {
	raw := map[string]any{
		"messages": []any{},
		"tools":    []any{map[string]any{"type": "code_interpreter"}},
	}
	if _, err := OpenAIToGemini(raw); err == nil {
		t.Fatal("want error for non-function tool")
	}
}

// ---------- tools -> functionDeclarations ----------

func weatherToolRaw(params map[string]any) map[string]any {
	fn := map[string]any{"name": "get_weather", "description": "查询天气"}
	if params != nil {
		fn["parameters"] = params
	}
	return map[string]any{"type": "function", "function": fn}
}

func TestOpenAIToGeminiToolsToFunctionDeclarations(t *testing.T) {
	raw := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{
			weatherToolRaw(map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"city": map[string]any{"type": "string", "description": "城市", "x-internal": true}},
				"required":             []any{"city"},
				"$schema":              "http://json-schema.org/draft-07/schema#",
				"additionalProperties": false,
			}),
		},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Tools) != 1 || len(g.Tools[0].FunctionDeclarations) != 1 {
		b, _ := json.Marshal(g)
		t.Fatalf("bad tools: %s", b)
	}
	decl := g.Tools[0].FunctionDeclarations[0]
	if decl.Name != "get_weather" || decl.Description != "查询天气" {
		t.Fatalf("bad decl: %+v", decl)
	}
	p := decl.Parameters
	if p["type"] != "OBJECT" {
		t.Fatalf("type must be OBJECT, got %v", p["type"])
	}
	if _, exists := p["$schema"]; exists {
		t.Fatal("$schema must be stripped")
	}
	if _, exists := p["additionalProperties"]; exists {
		t.Fatal("additionalProperties must be stripped")
	}
	props := p["properties"].(map[string]any)
	city := props["city"].(map[string]any)
	if city["type"] != "STRING" {
		t.Fatalf("prop type must be STRING, got %v", city["type"])
	}
	if _, exists := city["x-internal"]; exists {
		t.Fatal("unknown prop fields must be stripped")
	}
	req := p["required"].([]any)
	if len(req) != 1 || req[0] != "city" {
		t.Fatalf("required kept: %v", req)
	}
}

func TestOpenAIToGeminiSchemaNullability(t *testing.T) {
	raw := map[string]any{
		"messages": []any{},
		"tools": []any{weatherToolRaw(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": []any{"string", "null"}},
				"b": map[string]any{"type": "null"},
			},
		})},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	props := g.Tools[0].FunctionDeclarations[0].Parameters["properties"].(map[string]any)
	a := props["a"].(map[string]any)
	if a["type"] != "STRING" || a["nullable"] != true {
		t.Fatalf("a: %+v", a)
	}
	b := props["b"].(map[string]any)
	if _, hasType := b["type"]; hasType || b["nullable"] != true {
		t.Fatalf("b: %+v", b)
	}
}

func TestOpenAIToGeminiEmptyPropertiesDropped(t *testing.T) {
	raw := map[string]any{
		"messages": []any{},
		"tools":    []any{weatherToolRaw(map[string]any{"type": "object", "properties": map[string]any{}})},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	decl := g.Tools[0].FunctionDeclarations[0]
	if decl.Parameters != nil {
		t.Fatalf("empty properties must drop parameters: %+v", decl.Parameters)
	}
}

func TestOpenAIToGeminiToolChoiceVariants(t *testing.T) {
	cases := []struct {
		choice any
		mode   string
		names  []string
	}{
		{"auto", "AUTO", nil},
		{"none", "NONE", nil},
		{"required", "ANY", nil},
		{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}, "ANY", []string{"get_weather"}},
	}
	for _, c := range cases {
		raw := map[string]any{
			"messages":    []any{},
			"tools":       []any{weatherToolRaw(nil)},
			"tool_choice": c.choice,
		}
		g, err := OpenAIToGemini(raw)
		if err != nil {
			t.Fatal(err)
		}
		if g.ToolConfig == nil {
			t.Fatalf("tool_choice %v: missing toolConfig", c.choice)
		}
		fcc := g.ToolConfig.FunctionCallingConfig
		if fcc.Mode != c.mode {
			t.Fatalf("tool_choice %v: want mode %s got %s", c.choice, c.mode, fcc.Mode)
		}
		if len(fcc.AllowedFunctionNames) != len(c.names) {
			t.Fatalf("tool_choice %v: names %v", c.choice, fcc.AllowedFunctionNames)
		}
		for i, n := range c.names {
			if fcc.AllowedFunctionNames[i] != n {
				t.Fatalf("tool_choice %v: names %v", c.choice, fcc.AllowedFunctionNames)
			}
		}
	}
}

// ---------- tool_calls history ----------

func TestOpenAIToGeminiToolCallsHistory(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "北京天气?"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"city":"北京"}`,
					},
				},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": `{"temp":25}`},
		},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(g)
	if len(g.Contents) != 3 {
		t.Fatalf("want 3 contents: %s", b)
	}
	// assistant -> model with functionCall part
	model := g.Contents[1]
	if model.Role != "model" {
		t.Fatalf("bad role: %s", b)
	}
	call := model.Parts[0].FunctionCall
	if call == nil || call.Name != "get_weather" || call.Args["city"] != "北京" {
		t.Fatalf("bad functionCall: %s", b)
	}
	if call.ID != "call_1" {
		t.Fatalf("call id should be preserved: %s", b)
	}
	// tool -> user content with functionResponse
	respContent := g.Contents[2]
	if respContent.Role != "user" {
		t.Fatalf("tool response must be user role: %s", b)
	}
	fr := respContent.Parts[0].FunctionResponse
	if fr == nil || fr.Name != "get_weather" {
		t.Fatalf("bad functionResponse: %s", b)
	}
	if fr.ID != "call_1" {
		t.Fatalf("functionResponse.id must carry tool_call_id: %s", b)
	}
	if fr.Response["temp"] != float64(25) {
		t.Fatalf("bad response body: %s", b)
	}
}

func TestOpenAIToGeminiParallelToolResponsesMerged(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "f1", "arguments": "{}"}},
				map[string]any{"id": "c2", "type": "function", "function": map[string]any{"name": "f2", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": `"结果一"`},
			map[string]any{"role": "tool", "tool_call_id": "c2", "content": `"结果二"`},
		},
	}
	g, err := OpenAIToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Contents) != 2 {
		t.Fatalf("parallel tool responses must merge into one user content, got %d", len(g.Contents))
	}
	resp := g.Contents[1]
	if len(resp.Parts) != 2 {
		t.Fatalf("want 2 functionResponse parts: %+v", resp.Parts)
	}
	if resp.Parts[0].FunctionResponse.Name != "f1" || resp.Parts[1].FunctionResponse.Name != "f2" {
		t.Fatalf("bad names: %+v", resp.Parts)
	}
	if resp.Parts[0].FunctionResponse.Response["content"] != `"结果一"` {
		t.Fatalf("non-object content must be wrapped: %+v", resp.Parts[0].FunctionResponse.Response)
	}
}

func TestOpenAIToGeminiToolMessageUnknownIDRejected(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "tool", "tool_call_id": "ghost", "content": "x"},
		},
	}
	if _, err := OpenAIToGemini(raw); err == nil {
		t.Fatal("unknown tool_call_id must be rejected")
	}
}

// ---------- Gemini response -> OpenAI ----------

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

func TestGeminiResponseToOpenAIToolCalls(t *testing.T) {
	g := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: &GeminiContent{Parts: []GeminiPart{
				{Text: "让我查一下"},
				{FunctionCall: &GeminiFunctionCall{Name: "get_weather", Args: map[string]any{"city": "北京"}, ID: "gemini-call-1"}},
			}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 5, CandidatesTokenCount: 8, TotalTokenCount: 13},
	}
	out := GeminiResponseToOpenAI("free", g)
	ch := out["choices"].([]any)[0].(map[string]any)
	if ch["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason must be tool_calls: %+v", ch)
	}
	msg := ch["message"].(map[string]any)
	if msg["content"] != "让我查一下" {
		t.Fatalf("text alongside tool call must be kept: %v", msg["content"])
	}
	tcs := msg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("want 1 tool_call: %+v", msg)
	}
	tc := tcs[0].(map[string]any)
	if tc["type"] != "function" || tc["id"] != "gemini-call-1" {
		t.Fatalf("bad tool_call: %+v", tc)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Fatalf("bad name: %+v", fn)
	}
	if fn["arguments"] != `{"city":"北京"}` {
		t.Fatalf("arguments must be a JSON string: %v", fn["arguments"])
	}
}

func TestGeminiResponseToOpenAIReasoningContent(t *testing.T) {
	g := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: &GeminiContent{Parts: []GeminiPart{
				{Text: "内部思考", Thought: true},
				{Text: "回答"},
			}},
			FinishReason: "STOP",
		}},
	}
	out := GeminiResponseToOpenAI("free", g)
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "回答" {
		t.Fatalf("content: %v", msg["content"])
	}
	if msg["reasoning_content"] != "内部思考" {
		t.Fatalf("reasoning_content: %v", msg["reasoning_content"])
	}
}

func TestGeminiResponseToOpenAIParallelCalls(t *testing.T) {
	g := &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: &GeminiContent{Parts: []GeminiPart{
				{FunctionCall: &GeminiFunctionCall{Name: "f1", Args: map[string]any{}}},
				{FunctionCall: &GeminiFunctionCall{Name: "f2", Args: map[string]any{}}},
			}},
			FinishReason: "STOP",
		}},
	}
	out := GeminiResponseToOpenAI("free", g)
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	tcs := msg["tool_calls"].([]any)
	if len(tcs) != 2 {
		t.Fatalf("want 2 tool_calls: %+v", msg)
	}
	if tcs[0].(map[string]any)["function"].(map[string]any)["name"] != "f1" ||
		tcs[1].(map[string]any)["function"].(map[string]any)["name"] != "f2" {
		t.Fatal("parallel calls order")
	}
}

// ---------- stream converter ----------

func parseChunk(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("bad chunk json %s: %v", b, err)
	}
	if m["object"] != "chat.completion.chunk" {
		t.Fatalf("bad object: %v", m["object"])
	}
	return m
}

func TestStreamConverterText(t *testing.T) {
	conv := NewStreamConverter("free")

	// first chunk carries role; empty finish payload without content emits nothing extra
	chunks, done := conv.Convert(&GeminiResponse{Candidates: []GeminiCandidate{{
		Content: &GeminiContent{Parts: []GeminiPart{{Text: "你"}}},
	}}})
	if done || len(chunks) != 1 {
		t.Fatalf("first: len=%d done=%v", len(chunks), done)
	}
	c1 := parseChunk(t, chunks[0])
	delta := c1["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if delta["role"] != "assistant" || delta["content"] != "你" {
		t.Fatalf("first delta: %v", delta)
	}
	id1 := c1["id"].(string)

	// second text chunk keeps the same completion id
	chunks, done = conv.Convert(&GeminiResponse{Candidates: []GeminiCandidate{{
		Content: &GeminiContent{Parts: []GeminiPart{{Text: "好"}}},
	}}})
	if done || len(chunks) != 1 {
		t.Fatalf("second: len=%d done=%v", len(chunks), done)
	}
	c2 := parseChunk(t, chunks[0])
	if c2["id"] != id1 {
		t.Fatal("stream must share one completion id")
	}
	if c2["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"] != "好" {
		t.Fatal("second content")
	}

	// finish chunk: no content delta, finish_reason stop, usage attached
	chunks, done = conv.Convert(&GeminiResponse{
		Candidates:    []GeminiCandidate{{Content: &GeminiContent{}, FinishReason: "STOP"}},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 1, CandidatesTokenCount: 2, TotalTokenCount: 3},
	})
	if !done || len(chunks) != 1 {
		t.Fatalf("finish: len=%d done=%v", len(chunks), done)
	}
	cf := parseChunk(t, chunks[0])
	choice := cf["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish: %v", choice["finish_reason"])
	}
	if cf["usage"] == nil {
		t.Fatal("terminal chunk must carry usage")
	}
}

func TestStreamConverterToolCalls(t *testing.T) {
	conv := NewStreamConverter("free")

	// Gemini emits the function call atomically together with STOP.
	chunks, done := conv.Convert(&GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: &GeminiContent{Parts: []GeminiPart{
				{FunctionCall: &GeminiFunctionCall{Name: "get_weather", Args: map[string]any{"city": "北京"}}},
			}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 2, CandidatesTokenCount: 4, TotalTokenCount: 6},
	})
	if !done {
		t.Fatal("finish must terminate the stream")
	}
	if len(chunks) != 2 {
		t.Fatalf("want tool chunk + terminal finish chunk, got %d", len(chunks))
	}
	c1 := parseChunk(t, chunks[0])
	choice1 := c1["choices"].([]any)[0].(map[string]any)
	if choice1["finish_reason"] != nil {
		t.Fatal("tool chunk must not carry finish_reason")
	}
	delta := choice1["delta"].(map[string]any)
	tcs := delta["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("delta tool_calls: %v", delta)
	}
	tc := tcs[0].(map[string]any)
	if tc["type"] != "function" || tc["index"] != float64(0) {
		t.Fatalf("bad tool call: %v", tc)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["arguments"] != `{"city":"北京"}` {
		t.Fatalf("bad function: %v", fn)
	}
	if !strings.HasPrefix(tc["id"].(string), "call-") {
		t.Fatalf("call id must be synthesized: %v", tc)
	}

	cf := parseChunk(t, chunks[1])
	choiceF := cf["choices"].([]any)[0].(map[string]any)
	if choiceF["finish_reason"] != "tool_calls" {
		t.Fatalf("terminal finish must be tool_calls: %v", choiceF["finish_reason"])
	}
	if cf["usage"] == nil {
		t.Fatal("terminal usage missing")
	}
}

func TestStreamConverterToolIndexMonotonic(t *testing.T) {
	conv := NewStreamConverter("free")

	chunks1, done := conv.Convert(&GeminiResponse{Candidates: []GeminiCandidate{{
		Content: &GeminiContent{Parts: []GeminiPart{
			{FunctionCall: &GeminiFunctionCall{Name: "f1", Args: map[string]any{}}},
		}},
	}}})
	if done || len(chunks1) != 1 {
		t.Fatalf("chunk1: len=%d done=%v", len(chunks1), done)
	}

	chunks2, done := conv.Convert(&GeminiResponse{Candidates: []GeminiCandidate{{
		Content: &GeminiContent{Parts: []GeminiPart{
			{FunctionCall: &GeminiFunctionCall{Name: "f2", Args: map[string]any{}}},
		}},
		FinishReason: "STOP",
	}}})
	if !done || len(chunks2) != 2 {
		t.Fatalf("chunk2: len=%d done=%v", len(chunks2), done)
	}

	i1 := parseChunk(t, chunks1[0])["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["index"]
	i2 := parseChunk(t, chunks2[0])["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["index"]
	if i1 != float64(0) || i2 != float64(1) {
		t.Fatalf("tool indexes must be monotonic across chunks: %v %v", i1, i2)
	}
	finish := parseChunk(t, chunks2[1])["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if finish != "tool_calls" {
		t.Fatalf("terminal finish: %v", finish)
	}
}

func TestStreamConverterFinalizeWithoutFinish(t *testing.T) {
	conv := NewStreamConverter("free")
	conv.Convert(&GeminiResponse{Candidates: []GeminiCandidate{{
		Content: &GeminiContent{Parts: []GeminiPart{{Text: "部分"}}},
	}}})
	ch, done := conv.Finish()
	if !done || ch == nil {
		t.Fatalf("finalize must emit terminal chunk: done=%v", done)
	}
	c := parseChunk(t, ch)
	if c["choices"].([]any)[0].(map[string]any)["finish_reason"] != "stop" {
		t.Fatal("finalize finish should be stop")
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
}
