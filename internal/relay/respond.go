package relay

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gkd-api/internal/engine"
	"gkd-api/internal/translate"
)

// respond forwards the winning upstream response to the client, converting
// from the upstream protocol (openai passthrough / gemini translate) as needed.
func respond(c *gin.Context, virtualName string, res *engine.Result) {
	defer res.Body.Close()
	if res.Cand.Stream {
		respondStream(c, virtualName, res)
		return
	}
	respondJSON(c, virtualName, res)
}

func respondStream(c *gin.Context, virtualName string, res *engine.Result) {
	ct := res.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/event-stream"
	}
	c.Header("Content-Type", ct)
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	switch res.Cand.Real.Protocol {
	case "gemini":
		pumpGeminiStream(c, virtualName, res.Reader)
	default:
		pumpRaw(c, res.Reader)
	}
}

func respondJSON(c *gin.Context, virtualName string, res *engine.Result) {
	data, err := io.ReadAll(res.Reader)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "读取上游响应失败: " + err.Error(), "type": "gkd_api_error",
		}})
		return
	}
	switch res.Cand.Real.Protocol {
	case "gemini":
		var g translate.GeminiResponse
		if err := json.Unmarshal(data, &g); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
				"message": "上游响应解析失败: " + err.Error(), "type": "gkd_api_error",
			}})
			return
		}
		if g.Error != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
				"message": "上游错误: " + g.Error.Message, "type": "gkd_api_error",
			}})
			return
		}
		c.JSON(http.StatusOK, translate.GeminiResponseToOpenAI(virtualName, &g))
	default:
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			m["model"] = virtualName
			c.JSON(http.StatusOK, m)
			return
		}
		ct := res.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		c.Data(http.StatusOK, ct, data)
	}
}

// pumpRaw relays an OpenAI-compatible SSE stream byte-for-byte.
func pumpRaw(c *gin.Context, rd *bufio.Reader) {
	flusher, _ := c.Writer.(http.Flusher)
	buf := make([]byte, 8192)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			if _, werr := c.Writer.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
	}
}

// pumpGeminiStream converts a Gemini SSE stream (data: {...}) into OpenAI
// chat.completion.chunk format, terminating with "data: [DONE]".
func pumpGeminiStream(c *gin.Context, virtualName string, rd *bufio.Reader) {
	flusher, _ := c.Writer.(http.Flusher)
	roleSent := false

	emit := func(payload []byte) bool {
		if _, err := c.Writer.Write([]byte("data: ")); err != nil {
			return false
		}
		if _, err := c.Writer.Write(payload); err != nil {
			return false
		}
		if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	handleLine := func(line string) bool {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			return true
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			return true
		}
		if payload == "[DONE]" {
			return false
		}
		var g translate.GeminiResponse
		if err := json.Unmarshal([]byte(payload), &g); err != nil {
			return true
		}
		jb, done, err := translate.GeminiChunkToOpenAI(virtualName, &g, &roleSent)
		if err != nil {
			return true
		}
		if !emit(jb) {
			return false
		}
		if done {
			emit([]byte("[DONE]"))
			return false
		}
		return true
	}

	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			if !handleLine(line) {
				return
			}
		}
		if err != nil {
			break
		}
		if c.Request.Context().Err() != nil {
			return
		}
	}
	if roleSent {
		emit([]byte("[DONE]"))
	}
}
