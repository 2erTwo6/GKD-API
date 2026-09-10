package translate

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------- Gemini upstream request types ----------

type GeminiInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiPart struct {
	Text       string        `json:"text,omitempty"`
	InlineData *GeminiInline `json:"inlineData,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiGenerationConfig struct {
	Temperature      float64  `json:"temperature,omitempty"`
	TopP             float64  `json:"topP,omitempty"`
	MaxOutputTokens  int64    `json:"maxOutputTokens,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMIMEType string   `json:"responseMimeType,omitempty"`
}

type GeminiRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

// ---------- Gemini upstream response types ----------

type GeminiUsage struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
}

type GeminiCandidate struct {
	Content      *GeminiContent `json:"content"`
	FinishReason string         `json:"finishReason,omitempty"`
}

type GeminiResponse struct {
	Candidates     []GeminiCandidate `json:"candidates"`
	UsageMetadata  *GeminiUsage      `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// ---------- OpenAI -> Gemini (request) ----------

type oaiPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

type oaiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

var imgClient = &http.Client{Timeout: 30 * time.Second}

// OpenAIToGemini converts an OpenAI chat request (as generic map) to a Gemini request.
func OpenAIToGemini(raw map[string]any) (*GeminiRequest, error) {
	if t, ok := raw["tools"]; ok && t != nil {
		if arr, ok := t.([]any); !ok || len(arr) > 0 {
			return nil, errors.New("Gemini 上游暂不支持 tools 字段")
		}
	}
	msgs, ok := raw["messages"].([]any)
	if !ok {
		return nil, errors.New("请求缺少 messages 字段")
	}
	req := &GeminiRequest{}
	cfg := &GeminiGenerationConfig{}
	hasCfg := false
	var sysTexts []string

	for _, mi := range msgs {
		m, ok := mi.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		text, parts, err := messageTextAndParts(m)
		if err != nil {
			return nil, err
		}
		switch role {
		case "system", "developer":
			if text != "" {
				sysTexts = append(sysTexts, text)
			}
		case "user":
			req.Contents = appendContent(req.Contents, "user", parts)
		case "assistant":
			req.Contents = appendContent(req.Contents, "model", parts)
		default:
			return nil, fmt.Errorf("不支持的消息角色: %s", role)
		}
	}

	if len(sysTexts) > 0 {
		req.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: strings.Join(sysTexts, "\n\n")}},
		}
	}

	if v, ok := num(raw["temperature"]); ok {
		cfg.Temperature = v
		hasCfg = true
	}
	if v, ok := num(raw["top_p"]); ok {
		cfg.TopP = v
		hasCfg = true
	}
	mt, ok := maxTokens(raw)
	if ok {
		cfg.MaxOutputTokens = mt
		hasCfg = true
	}
	if stops := stopSequences(raw); len(stops) > 0 {
		cfg.StopSequences = stops
		hasCfg = true
	}
	if rf, ok := raw["response_format"].(map[string]any); ok {
		if t, _ := rf["type"].(string); t == "json_object" {
			cfg.ResponseMIMEType = "application/json"
			hasCfg = true
		}
	}
	if hasCfg {
		req.GenerationConfig = cfg
	}
	return req, nil
}

func appendContent(contents []GeminiContent, role string, parts []GeminiPart) []GeminiContent {
	if len(parts) == 0 {
		return contents
	}
	// merge consecutive same-role contents (Gemini prefers alternating roles)
	if n := len(contents); n > 0 && contents[n-1].Role == role {
		contents[n-1].Parts = append(contents[n-1].Parts, parts...)
		return contents
	}
	return append(contents, GeminiContent{Role: role, Parts: parts})
}

func messageTextAndParts(m map[string]any) (string, []GeminiPart, error) {
	if m["content"] == nil {
		return "", nil, nil
	}
	switch c := m["content"].(type) {
	case string:
		return c, []GeminiPart{{Text: c}}, nil
	case []any:
		var parts []GeminiPart
		var texts []string
		for _, pi := range c {
			p, ok := pi.(map[string]any)
			if !ok {
				continue
			}
			switch p["type"] {
			case "text":
				s, _ := p["text"].(string)
				texts = append(texts, s)
				parts = append(parts, GeminiPart{Text: s})
			case "image_url":
				iu, _ := p["image_url"].(map[string]any)
				u, _ := iu["url"].(string)
				mime, data, err := resolveImage(u)
				if err != nil {
					return "", nil, fmt.Errorf("图片处理失败: %w", err)
				}
				parts = append(parts, GeminiPart{InlineData: &GeminiInline{MimeType: mime, Data: data}})
			default:
				return "", nil, fmt.Errorf("不支持的内容类型: %v", p["type"])
			}
		}
		return strings.Join(texts, "\n"), parts, nil
	default:
		b, _ := json.Marshal(c)
		return string(b), []GeminiPart{{Text: string(b)}}, nil
	}
}

func resolveImage(u string) (string, string, error) {
	if strings.HasPrefix(u, "data:") {
		rest := u[5:]
		comma := strings.Index(rest, ",")
		if comma < 0 {
			return "", "", errors.New("无效的 data URI")
		}
		meta := rest[:comma]
		data := rest[comma+1:]
		mime := strings.Split(meta, ";")[0]
		if mime == "" {
			mime = "image/png"
		}
		return mime, data, nil
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", fmt.Errorf("无法识别的图片地址: %s", u)
	}
	resp, err := imgClient.Get(u)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return "", "", err
	}
	mime := resp.Header.Get("Content-Type")
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = http.DetectContentType(data[:min(512, len(data))])
	}
	return mime, base64.StdEncoding.EncodeToString(data), nil
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func maxTokens(raw map[string]any) (int64, bool) {
	for _, k := range []string{"max_completion_tokens", "max_tokens"} {
		if v, ok := num(raw[k]); ok {
			return int64(v), true
		}
	}
	return 0, false
}

func stopSequences(raw map[string]any) []string {
	switch s := raw["stop"].(type) {
	case string:
		if s != "" {
			return []string{s}
		}
	case []any:
		var out []string
		for _, v := range s {
			if str, ok := v.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

// ---------- Gemini -> OpenAI (response) ----------

func textOfContent(c *GeminiContent) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range c.Parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

func mapFinishReason(fr string) string {
	switch fr {
	case "STOP", "":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "LANGUAGE", "OTHER":
		return "content_filter"
	default:
		return "stop"
	}
}

func newChatID() string {
	b := make([]byte, 10)
	rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

// GeminiResponseToOpenAI converts a non-streaming Gemini response to an OpenAI
// chat.completion response body (as generic map, so callers can tweak fields).
func GeminiResponseToOpenAI(vname string, g *GeminiResponse) map[string]any {
	content := ""
	finish := "stop"
	if len(g.Candidates) > 0 {
		content = textOfContent(g.Candidates[0].Content)
		finish = mapFinishReason(g.Candidates[0].FinishReason)
	} else if g.PromptFeedback != nil && g.PromptFeedback.BlockReason != "" {
		finish = "content_filter"
	}
	choice := map[string]any{
		"index":         0,
		"message":       map[string]any{"role": "assistant", "content": content},
		"finish_reason": finish,
	}
	out := map[string]any{
		"id":      newChatID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   vname,
		"choices": []any{choice},
	}
	if g.UsageMetadata != nil {
		out["usage"] = map[string]any{
			"prompt_tokens":     g.UsageMetadata.PromptTokenCount,
			"completion_tokens": g.UsageMetadata.CandidatesTokenCount,
			"total_tokens":      g.UsageMetadata.TotalTokenCount,
		}
	}
	return out
}

// ---------- OpenAI chunk types ----------

type openaiDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openaiChunkChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type openaiUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type openaiChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []openaiChunkChoice `json:"choices"`
	Usage   *openaiUsage        `json:"usage,omitempty"`
}

// GeminiChunkToOpenAI converts one streaming Gemini SSE payload into an OpenAI
// chat.completion.chunk JSON line (without the "data: " prefix).
// Returns (jsonBytes, finished, error).
func GeminiChunkToOpenAI(vname string, g *GeminiResponse, roleSent *bool) ([]byte, bool, error) {
	chunk := openaiChunk{
		ID:      newChatID(),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   vname,
	}
	var delta openaiDelta
	var finish *string
	done := false
	if len(g.Candidates) > 0 {
		c := g.Candidates[0]
		text := textOfContent(c.Content)
		if !*roleSent {
			delta.Role = "assistant"
			*roleSent = true
		}
		delta.Content = text
		if c.FinishReason != "" {
			fr := mapFinishReason(c.FinishReason)
			finish = &fr
			done = true
		}
	} else if !*roleSent {
		delta.Role = "assistant"
		*roleSent = true
	}
	if g.UsageMetadata != nil && done {
		chunk.Usage = &openaiUsage{
			PromptTokens:     g.UsageMetadata.PromptTokenCount,
			CompletionTokens: g.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      g.UsageMetadata.TotalTokenCount,
		}
	}
	chunk.Choices = []openaiChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}}
	out, err := json.Marshal(chunk)
	return out, done, err
}

// GeminiURL builds the full upstream URL for a Gemini request.
func GeminiURL(base, model string, stream bool) string {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if !strings.HasSuffix(base, "/v1beta") && !strings.HasSuffix(base, "/v1") {
		base += "/v1beta"
	}
	action := ":generateContent"
	if stream {
		action = ":streamGenerateContent?alt=sse"
	}
	return base + "/models/" + model + action
}

// min for older Go
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
