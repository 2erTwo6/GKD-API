package engine

import "net/http"

import "gkd-api/internal/translate"

// UpstreamURL builds the request URL for a candidate based on its protocol.
func UpstreamURL(c Candidate) string {
	switch c.Real.Protocol {
	case "gemini":
		return translate.GeminiURL(c.Real.BaseURL, c.Real.Name, c.Stream)
	default: // openai
		return c.Real.BaseURL + "/chat/completions"
	}
}

// ApplyAuth sets protocol-specific authentication headers.
func ApplyAuth(req *http.Request, c Candidate) {
	switch c.Real.Protocol {
	case "gemini":
		req.Header.Set("x-goog-api-key", c.Real.APIKey)
		if c.Stream {
			req.Header.Set("Accept", "text/event-stream")
		}
	default: // openai
		req.Header.Set("Authorization", "Bearer "+c.Real.APIKey)
		if c.Stream {
			req.Header.Set("Accept", "text/event-stream")
		}
	}
}
