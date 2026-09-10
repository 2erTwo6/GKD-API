package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gkd-api/internal/db"
	"gkd-api/internal/engine"
	"gkd-api/internal/merge"
	"gkd-api/internal/translate"
)

type Service struct {
	DB     *gorm.DB
	Client *http.Client
}

func New(db *gorm.DB) *Service {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 32,
	}
	return &Service{DB: db, Client: &http.Client{Transport: tr}}
}

func (s *Service) RegisterRoutes(g gin.IRouter) {
	g.Use(s.apiKeyAuth())
	g.GET("/models", s.listModels)
	g.POST("/chat/completions", s.chatCompletions)
}

func (s *Service) apiKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer ")
		if key == "" {
			key = c.Request.Header.Get("x-goog-api-key")
		}
		if key == "" {
			abortAuth(c, "缺少 API Key")
			return
		}
		var ak db.APIKey
		if err := s.DB.Where("`key` = ?", key).First(&ak).Error; err != nil || !ak.Enabled {
			abortAuth(c, "无效的 API Key")
			return
		}
		c.Set("api_key_name", ak.Name)
		go func(id uint, now time.Time) {
			s.DB.Model(&db.APIKey{}).Where("id = ?", id).Update("last_used_at", &now)
		}(ak.ID, time.Now())
		c.Next()
	}
}

func abortAuth(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{"message": msg, "type": "invalid_request_error", "code": "invalid_api_key"},
	})
}

func (s *Service) listModels(c *gin.Context) {
	var vms []db.VirtualModel
	if err := s.DB.Where("enabled = ?", true).Order("id").Find(&vms).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	data := make([]gin.H, 0, len(vms))
	for _, vm := range vms {
		data = append(data, gin.H{
			"id":       vm.Name,
			"object":   "model",
			"created":  vm.CreatedAt.Unix(),
			"owned_by": "gkd-api",
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func (s *Service) chatCompletions(c *gin.Context) {
	start := time.Now()
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20))
	if err != nil {
		badRequest(c, "读取请求体失败: "+err.Error())
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		badRequest(c, "请求体不是合法的 JSON")
		return
	}
	modelName, _ := raw["model"].(string)
	if modelName == "" {
		badRequest(c, "缺少 model 字段")
		return
	}

	var vm db.VirtualModel
	if err := s.DB.Where("name = ? AND enabled = ?", modelName, true).First(&vm).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"message": "虚拟模型不存在或未启用: " + modelName, "type": "invalid_request_error", "code": "model_not_found",
		}})
		return
	}
	var reals []db.RealModel
	if err := s.DB.Where("virtual_model_id = ? AND enabled = ?", vm.ID, true).Find(&reals).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if len(reals) == 0 {
		s.fail(c, vm, start, "虚拟模型 "+vm.Name+" 没有已启用的真实模型")
		return
	}

	cands := make([]engine.Candidate, 0, len(reals))
	var prepErrs []string
	for i, rm := range reals {
		b, stream, err := s.prepareBody(raw, rm)
		if err != nil {
			prepErrs = append(prepErrs, rm.Name+": "+err.Error())
			continue
		}
		cands = append(cands, engine.Candidate{Idx: i, Real: rm, Body: b, Stream: stream})
	}
	if len(cands) == 0 {
		s.fail(c, vm, start, "所有真实模型请求体准备失败: "+strings.Join(prepErrs, "; "))
		return
	}

	res, err := engine.Run(c.Request.Context(), vm, cands, engine.HTTPDoLauncher(s.Client))
	if err != nil {
		detail := err.Error()
		if len(prepErrs) > 0 {
			detail += "；另有准备失败: " + strings.Join(prepErrs, "; ")
		}
		s.fail(c, vm, start, detail)
		return
	}
	s.writeLog(c, vm, res.Cand.Real.Name, time.Since(start).Milliseconds(), http.StatusOK, "")
	respond(c, vm.Name, res)
}

// prepareBody builds the per-upstream request body: replace model, apply the
// override JSON merge patch, then translate protocol if needed.
// The returned stream flag reflects overrides (Gemini URL depends on it).
func (s *Service) prepareBody(raw map[string]any, rm db.RealModel) (body []byte, stream bool, err error) {
	cp, err := json.Marshal(raw)
	if err != nil {
		return nil, false, err
	}
	var m map[string]any
	if err := json.Unmarshal(cp, &m); err != nil {
		return nil, false, err
	}
	m["model"] = rm.Name
	if oj := strings.TrimSpace(rm.OverrideJSON); oj != "" {
		var patch map[string]any
		if err := json.Unmarshal([]byte(oj), &patch); err != nil {
			return nil, false, fmt.Errorf("override_json 不是合法 JSON 对象: %w", err)
		}
		m = merge.Patch(m, patch)
	}
	switch rm.Protocol {
	case "gemini":
		stream, _ = m["stream"].(bool)
		gr, err := translate.OpenAIToGemini(m)
		if err != nil {
			return nil, false, err
		}
		body, err = json.Marshal(gr)
		return body, stream, err
	default:
		stream, _ = m["stream"].(bool)
		body, err = json.Marshal(m)
		return body, stream, err
	}
}

func (s *Service) fail(c *gin.Context, vm db.VirtualModel, start time.Time, detail string) {
	s.writeLog(c, vm, "", time.Since(start).Milliseconds(), http.StatusServiceUnavailable, detail)
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
		"message": detail, "type": "gkd_api_error", "code": "no_upstream_response",
	}})
}

func (s *Service) writeLog(c *gin.Context, vm db.VirtualModel, winner string, latency int64, status int, detail string) {
	keyName, _ := c.Get("api_key_name")
	name, _ := keyName.(string)
	s.DB.Create(&db.RequestLog{
		KeyName:      name,
		VirtualModel: vm.Name,
		Winner:       winner,
		BatchSize:    vm.BatchSize,
		PickMode:     vm.PickMode,
		LatencyMs:    latency,
		Status:       status,
		Detail:       detail,
	})
}

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": msg, "type": "invalid_request_error"}})
}
