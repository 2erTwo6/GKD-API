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

type GeminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

type GeminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *GeminiInline           `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
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
	Tools             []GeminiTool            `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig       `json:"toolConfig,omitempty"`
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

var imgClient = &http.Client{Timeout: 30 * time.Second}

// OpenAIToGemini converts an OpenAI chat request (as generic map) to a Gemini request.
func OpenAIToGemini(raw map[string]any) (*GeminiRequest, error) {
	tools, err := convertTools(raw["tools"])
	if err != nil {
		return nil, err
	}

	msgs, ok := raw["messages"].([]any)
	if !ok {
		return nil, errors.New("请求缺少 messages 字段")
	}

	req := &GeminiRequest{}
	cfg := &GeminiGenerationConfig{}
	hasCfg := false
	var sysTexts []string
	toolCallNames := map[string]string{} // tool_call_id -> function name

	for _, mi := range msgs {
		m, ok := mi.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "system", "developer":
			if text, _, err := messageTextAndParts(m); err == nil && text != "" {
				sysTexts = append(sysTexts, text)
			}
		case "tool", "function":
			fr, err := toolMessageToFunctionResponse(m, toolCallNames)
			if err != nil {
				return nil, err
			}
			// Merge consecutive tool responses into one user content
			// (Gemini expects parallel function responses in a single turn).
			if n := len(req.Contents); n > 0 && req.Contents[n-1].Role == "user" {
				req.Contents[n-1].Parts = append(req.Contents[n-1].Parts, GeminiPart{FunctionResponse: &fr})
			} else {
				req.Contents = append(req.Contents, GeminiContent{
					Role:  "user",
					Parts: []GeminiPart{{FunctionResponse: &fr}},
				})
			}
		case "user":
			parts, err := messageParts(m)
			if err != nil {
				return nil, err
			}
			req.Contents = appendContent(req.Contents, "user", parts)
		case "assistant":
			parts := []GeminiPart{}
			if tcs, ok := m["tool_calls"].([]any); ok {
				for _, tci := range tcs {
					tc, ok := tci.(map[string]any)
					if !ok {
						continue
					}
					part, err := toolCallToFunctionCallPart(tc, toolCallNames)
					if err != nil {
						return nil, err
					}
					parts = append(parts, part)
				}
			}
			_, cparts, err := messageTextAndParts(m)
			if err != nil {
				return nil, err
			}
			parts = append(parts, cparts...)
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

	if len(tools) > 0 {
		req.Tools = tools
		if tc, ok := raw["tool_choice"]; ok && tc != nil {
			req.ToolConfig = convertToolChoice(tc)
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
	if mt, ok := maxTokens(raw); ok {
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

// messageParts converts a user/assistant message content into Gemini parts.
func messageParts(m map[string]any) ([]GeminiPart, error) {
	if m["content"] == nil {
		return nil, nil
	}
	switch c := m["content"].(type) {
	case string:
		if c == "" {
			return nil, nil
		}
		return []GeminiPart{{Text: c}}, nil
	case []any:
		var parts []GeminiPart
		for _, pi := range c {
			p, ok := pi.(map[string]any)
			if !ok {
				continue
			}
			switch p["type"] {
			case "text":
				s, _ := p["text"].(string)
				if s == "" {
					continue
				}
				parts = append(parts, GeminiPart{Text: s})
			case "image_url":
				iu, _ := p["image_url"].(map[string]any)
				u, _ := iu["url"].(string)
				mime, data, err := resolveImage(u)
				if err != nil {
					return nil, fmt.Errorf("图片处理失败: %w", err)
				}
				parts = append(parts, GeminiPart{InlineData: &GeminiInline{MimeType: mime, Data: data}})
			default:
				return nil, fmt.Errorf("不支持的内容类型: %v", p["type"])
			}
		}
		return parts, nil
	default:
		b, _ := json.Marshal(c)
		return []GeminiPart{{Text: string(b)}}, nil
	}
}

// messageTextAndParts returns the plain text of a message plus its parts.
func messageTextAndParts(m map[string]any) (string, []GeminiPart, error) {
	parts, err := messageParts(m)
	if err != nil {
		return "", nil, err
	}
	var texts []string
	for _, p := range parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n"), parts, nil
}

func toolCallToFunctionCallPart(tc map[string]any, names map[string]string) (GeminiPart, error) {
	fn, _ := tc["function"].(map[string]any)
	name, _ := fn["name"].(string)
	if name == "" {
		return GeminiPart{}, errors.New("tool_call 缺少 function.name")
	}
	args := map[string]any{}
	if s, _ := fn["arguments"].(string); s != "" && s != "null" {
		if err := json.Unmarshal([]byte(s), &args); err != nil || args == nil {
			return GeminiPart{}, fmt.Errorf("工具 %s 的 arguments 不是合法 JSON 对象: %s", name, s)
		}
	}
	id, _ := tc["id"].(string)
	if id != "" {
		names[id] = name
	}
	return GeminiPart{FunctionCall: &GeminiFunctionCall{Name: name, Args: args, ID: id}}, nil
}

func toolMessageToFunctionResponse(m map[string]any, toolCallNames map[string]string) (GeminiFunctionResponse, error) {
	name := ""
	if n, ok := m["name"].(string); ok && n != "" {
		name = n
	} else if id, ok := m["tool_call_id"].(string); ok && id != "" {
		name = toolCallNames[id]
	}
	if name == "" {
		return GeminiFunctionResponse{}, errors.New("tool 消息无法确定函数名（tool_call_id 无匹配，且未提供 name）")
	}
	var resp map[string]any
	switch c := m["content"].(type) {
	case string:
		resp = toolContentToObject(c)
	default:
		if b, err := json.Marshal(c); err == nil {
			resp = toolContentToObject(string(b))
		} else {
			resp = map[string]any{"content": fmt.Sprint(c)}
		}
	}
	fr := GeminiFunctionResponse{Name: name, Response: resp}
	if id, ok := m["tool_call_id"].(string); ok && id != "" {
		fr.ID = id
	}
	return fr, nil
}

func toolContentToObject(s string) map[string]any {
	if s == "" {
		return map[string]any{"content": ""}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err == nil && obj != nil {
		return obj
	}
	var arr []any
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		return map[string]any{"result": arr}
	}
	return map[string]any{"content": s}
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

// ---------- tools conversion (OpenAI -> Gemini) ----------

func convertTools(v any) ([]GeminiTool, error) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil, nil
	}
	var decls []GeminiFunctionDeclaration
	for _, ti := range arr {
		tool, ok := ti.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := tool["type"].(string); t != "function" {
			return nil, fmt.Errorf("不支持的工具类型: %v（Gemini 仅支持 function）", t)
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			return nil, errors.New("工具缺少 function 定义")
		}
		name, _ := fn["name"].(string)
		if name == "" {
			return nil, errors.New("工具缺少 function.name")
		}
		decl := GeminiFunctionDeclaration{Name: name}
		if desc, ok := fn["description"].(string); ok {
			decl.Description = desc
		}
		if params, ok := fn["parameters"].(map[string]any); ok && len(params) > 0 {
			cp, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var pm map[string]any
			if err := json.Unmarshal(cp, &pm); err != nil {
				return nil, fmt.Errorf("工具 %s 的 parameters 解析失败: %w", name, err)
			}
			// Gemini rejects an empty properties object; drop parameters instead.
			if props, has := pm["properties"].(map[string]any); has && len(props) == 0 {
				decls = append(decls, GeminiFunctionDeclaration{Name: name, Description: decl.Description})
			} else {
				cleaned, _ := cleanGeminiSchema(pm, 0).(map[string]any)
				decl.Parameters = cleaned
				decls = append(decls, decl)
			}
		} else {
			decls = append(decls, GeminiFunctionDeclaration{Name: name, Description: decl.Description})
		}
	}
	if len(decls) == 0 {
		return nil, nil
	}
	return []GeminiTool{{FunctionDeclarations: decls}}, nil
}

var geminiSchemaAllowedFields = map[string]struct{}{
	"anyOf": {}, "default": {}, "description": {}, "enum": {}, "example": {}, "format": {},
	"items": {}, "maxItems": {}, "maxLength": {}, "maxProperties": {}, "maximum": {},
	"minItems": {}, "minLength": {}, "minProperties": {}, "minimum": {}, "nullable": {},
	"pattern": {}, "properties": {}, "propertyOrdering": {}, "required": {}, "title": {},
	"type": {},
}

func cleanGeminiSchema(v any, depth int) any {
	if v == nil || depth > 64 {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(t))
		for k, val := range t {
			if _, ok := geminiSchemaAllowedFields[k]; ok {
				cleaned[k] = val
			}
		}
		normalizeGeminiSchemaType(cleaned)
		if props, ok := cleaned["properties"].(map[string]any); ok {
			for name, sub := range props {
				props[name] = cleanGeminiSchema(sub, depth+1)
			}
		}
		if items, ok := cleaned["items"].(map[string]any); ok {
			cleaned["items"] = cleanGeminiSchema(items, depth+1)
		}
		if itemsArr, ok := cleaned["items"].([]any); ok && len(itemsArr) > 0 {
			cleaned["items"] = cleanGeminiSchema(itemsArr[0], depth+1)
		}
		if anyOf, ok := cleaned["anyOf"].([]any); ok {
			for i, item := range anyOf {
				anyOf[i] = cleanGeminiSchema(item, depth+1)
			}
		}
		return cleaned
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = cleanGeminiSchema(item, depth+1)
		}
		return out
	default:
		return v
	}
}

// normalizeGeminiSchemaType uppercases the type keyword and converts JSON-Schema
// nullability ("null" type / type arrays) into Gemini's nullable flag.
func normalizeGeminiSchemaType(schema map[string]any) {
	raw, ok := schema["type"]
	if !ok || raw == nil {
		return
	}
	normalize := func(t string) (string, bool) {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "object":
			return "OBJECT", false
		case "array":
			return "ARRAY", false
		case "string":
			return "STRING", false
		case "integer":
			return "INTEGER", false
		case "number":
			return "NUMBER", false
		case "boolean":
			return "BOOLEAN", false
		case "null":
			return "", true
		default:
			return t, false
		}
	}
	switch tv := raw.(type) {
	case string:
		normalized, isNull := normalize(tv)
		if isNull {
			schema["nullable"] = true
			delete(schema, "type")
			return
		}
		schema["type"] = normalized
	case []any:
		nullable := false
		chosen := ""
		for _, item := range tv {
			s, ok := item.(string)
			if !ok {
				continue
			}
			normalized, isNull := normalize(s)
			if isNull {
				nullable = true
				continue
			}
			if chosen == "" {
				chosen = normalized
			}
		}
		if nullable {
			schema["nullable"] = true
		}
		if chosen != "" {
			schema["type"] = chosen
		} else {
			delete(schema, "type")
		}
	}
}

func convertToolChoice(v any) *GeminiToolConfig {
	switch tc := v.(type) {
	case string:
		mode := "AUTO"
		switch tc {
		case "none":
			mode = "NONE"
		case "required":
			mode = "ANY"
		}
		return &GeminiToolConfig{FunctionCallingConfig: &GeminiFunctionCallingConfig{Mode: mode}}
	case map[string]any:
		if t, _ := tc["type"].(string); t == "function" {
			cfg := &GeminiFunctionCallingConfig{Mode: "ANY"}
			if fn, ok := tc["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					cfg.AllowedFunctionNames = []string{name}
				}
			}
			return &GeminiToolConfig{FunctionCallingConfig: cfg}
		}
	}
	return nil
}

// ---------- Gemini -> OpenAI (response) ----------

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

// toolCallsFromParts converts functionCall parts into OpenAI tool_calls entries
// (JSON-string arguments; the call id is kept or synthesized).
func toolCallsFromParts(parts []GeminiPart) []any {
	var out []any
	for _, p := range parts {
		if p.FunctionCall == nil {
			continue
		}
		argsJSON := []byte("{}")
		if p.FunctionCall.Args != nil {
			if b, err := json.Marshal(p.FunctionCall.Args); err == nil {
				argsJSON = b
			}
		}
		id := strings.TrimSpace(p.FunctionCall.ID)
		if id == "" {
			id = "call-" + newChatID()[9:]
		}
		out = append(out, map[string]any{
			"id":   id,
			"type": "function",
			"function": map[string]any{
				"name":      p.FunctionCall.Name,
				"arguments": string(argsJSON),
			},
		})
	}
	return out
}

// GeminiResponseToOpenAI converts a non-streaming Gemini response to an OpenAI
// chat.completion response body (as generic map, so callers can tweak fields).
func GeminiResponseToOpenAI(vname string, g *GeminiResponse) map[string]any {
	content := ""
	var reasoning []string
	var toolCalls []any
	finish := "stop"
	if len(g.Candidates) > 0 {
		c := g.Candidates[0]
		var sb strings.Builder
		if c.Content != nil {
			for _, p := range c.Content.Parts {
				switch {
				case p.InlineData != nil:
					if strings.HasPrefix(p.InlineData.MimeType, "image") {
						sb.WriteString("\n![image](data:" + p.InlineData.MimeType + ";base64," + p.InlineData.Data + ")\n")
					}
				case p.Thought:
					if p.Text != "" {
						reasoning = append(reasoning, p.Text)
					}
				default:
					sb.WriteString(p.Text)
				}
			}
		}
		content = sb.String()
		toolCalls = toolCallsFromParts(partsOf(c))
		if len(toolCalls) > 0 {
			finish = "tool_calls"
		} else if c.FinishReason != "" {
			finish = mapFinishReason(c.FinishReason)
		}
	} else if g.PromptFeedback != nil && g.PromptFeedback.BlockReason != "" {
		finish = "content_filter"
	}
	message := map[string]any{"role": "assistant", "content": content}
	if len(reasoning) > 0 {
		message["reasoning_content"] = strings.Join(reasoning, "\n")
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	choice := map[string]any{
		"index":         0,
		"message":       message,
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

func partsOf(c GeminiCandidate) []GeminiPart {
	if c.Content == nil {
		return nil
	}
	return c.Content.Parts
}

// ---------- OpenAI chunk types ----------

type openaiDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function openaiToolCallFn `json:"function"`
}

type openaiToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
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

// StreamConverter converts a Gemini SSE stream into OpenAI chunks. It is
// stateful: the whole stream shares one completion id/created, tool-call
// indexes are monotonic across chunks, and the finish_reason is emitted on a
// terminal chunk (Gemini function calls arrive atomically; OpenAI clients must
// keep collecting tool deltas until the stream ends).
type StreamConverter struct {
	id      string
	created int64
	model   string

	roleSent      bool
	nextToolIndex int
	sawToolCall   bool
	finishEmitted bool
	usage         *openaiUsage
}

func NewStreamConverter(model string) *StreamConverter {
	return &StreamConverter{id: newChatID(), created: time.Now().Unix(), model: model}
}

func marshalChunk(ch openaiChunk) []byte {
	b, _ := json.Marshal(ch)
	return b
}

// Convert converts one Gemini SSE payload. Returns the OpenAI chunk JSON lines
// (optional content chunk, optional trailing finish chunk) and whether the
// stream is finished.
func (s *StreamConverter) Convert(g *GeminiResponse) ([][]byte, bool) {
	if s.finishEmitted {
		return nil, true
	}
	delta := openaiDelta{}
	var text, reasoning strings.Builder
	if len(g.Candidates) > 0 {
		c := g.Candidates[0]
		if c.Content != nil {
			for _, p := range c.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					argsJSON := []byte("{}")
					if p.FunctionCall.Args != nil {
						if b, err := json.Marshal(p.FunctionCall.Args); err == nil {
							argsJSON = b
						}
					}
					id := strings.TrimSpace(p.FunctionCall.ID)
					if id == "" {
						id = "call-" + newChatID()[9:]
					}
					s.sawToolCall = true
					delta.ToolCalls = append(delta.ToolCalls, openaiToolCall{
						Index:    s.nextToolIndex,
						ID:       id,
						Type:     "function",
						Function: openaiToolCallFn{Name: p.FunctionCall.Name, Arguments: string(argsJSON)},
					})
					s.nextToolIndex++
				case p.InlineData != nil && strings.HasPrefix(p.InlineData.MimeType, "image"):
					text.WriteString("\n![image](data:" + p.InlineData.MimeType + ";base64," + p.InlineData.Data + ")\n")
				case p.Thought:
					reasoning.WriteString(p.Text)
				default:
					text.WriteString(p.Text)
				}
			}
		}
		if g.UsageMetadata != nil {
			s.usage = &openaiUsage{
				PromptTokens:     g.UsageMetadata.PromptTokenCount,
				CompletionTokens: g.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      g.UsageMetadata.TotalTokenCount,
			}
		}
	}
	if !s.roleSent {
		delta.Role = "assistant"
		s.roleSent = true
	}
	delta.Content = text.String()
	delta.ReasoningContent = reasoning.String()

	var chunks [][]byte
	if delta.Role != "" || delta.Content != "" || delta.ReasoningContent != "" || len(delta.ToolCalls) > 0 {
		chunk := openaiChunk{
			ID:      s.id,
			Object:  "chat.completion.chunk",
			Created: s.created,
			Model:   s.model,
			Choices: []openaiChunkChoice{{Index: 0, Delta: delta, FinishReason: nil}},
		}
		chunks = append(chunks, marshalChunk(chunk))
	}

	// Gemini reports its finish reason (usually STOP, on the same payload as
	// tool calls): emit a separate terminal finish chunk so clients keep
	// aggregating tool deltas until the stream ends.
	if len(g.Candidates) > 0 && g.Candidates[0].FinishReason != "" {
		finish := mapFinishReason(g.Candidates[0].FinishReason)
		if s.sawToolCall {
			finish = "tool_calls"
		}
		chunks = append(chunks, marshalChunk(s.terminal(finish)))
		return chunks, true
	}
	return chunks, false
}

// Finish emits a terminal chunk when the upstream stream ends without a
// finish reason.
func (s *StreamConverter) Finish() ([]byte, bool) {
	if s.finishEmitted {
		return nil, true
	}
	finish := "stop"
	if s.sawToolCall {
		finish = "tool_calls"
	}
	s.finishEmitted = true
	return marshalChunk(s.terminal(finish)), true
}

func (s *StreamConverter) terminal(finish string) openaiChunk {
	return openaiChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openaiChunkChoice{{Index: 0, FinishReason: &finish}},
		Usage:   s.usage,
	}
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
