package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"gkd-api/internal/auth"
	"gkd-api/internal/db"
)

type Handler struct {
	DB     *gorm.DB
	Secret func() string
}

func Register(g gin.IRouter, database *gorm.DB, secret func() string) {
	h := &Handler{DB: database, Secret: secret}
	g.POST("/login", h.login)
	authed := g.Group("", h.jwtAuth())
	authed.GET("/me", h.me)
	authed.POST("/change-password", h.changePassword)

	authed.GET("/virtual-models", h.listVirtualModels)
	authed.POST("/virtual-models", h.createVirtualModel)
	authed.GET("/virtual-models/:id", h.getVirtualModel)
	authed.PUT("/virtual-models/:id", h.updateVirtualModel)
	authed.DELETE("/virtual-models/:id", h.deleteVirtualModel)
	authed.POST("/virtual-models/:id/real-models", h.createRealModel)
	authed.PUT("/real-models/:id", h.updateRealModel)
	authed.DELETE("/real-models/:id", h.deleteRealModel)

	authed.GET("/api-keys", h.listAPIKeys)
	authed.POST("/api-keys", h.createAPIKey)
	authed.PUT("/api-keys/:id", h.updateAPIKey)
	authed.DELETE("/api-keys/:id", h.deleteAPIKey)

	authed.GET("/logs", h.listLogs)
}

// bindPayload reads the request body once and decodes it into both a typed
// payload and a generic map (used for field-presence detection).
func bindPayload(c *gin.Context, p any) (map[string]any, error) {
	raw, err := c.GetRawData()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, p); err != nil {
		return nil, err
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m, nil
}

// ---------- auth ----------

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	var admin db.Admin
	if err := h.DB.Where("username = ?", req.Username).First(&admin).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "用户名或密码错误"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "用户名或密码错误"})
		return
	}
	token, err := auth.IssueToken(h.Secret(), admin.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "签发令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "username": admin.Username})
}

func (h *Handler) jwtAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		t := strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer ")
		if t == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "未登录"})
			return
		}
		claims, err := auth.ParseToken(h.Secret(), t)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "登录已过期，请重新登录"})
			return
		}
		c.Set("admin_id", claims.AdminID)
		c.Next()
	}
}

func (h *Handler) me(c *gin.Context) {
	id := c.GetUint("admin_id")
	var admin db.Admin
	if err := h.DB.First(&admin, id).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "账号不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": admin.Username})
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) changePassword(c *gin.Context) {
	var req changePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	if len(req.NewPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "新密码至少 6 位"})
		return
	}
	id := c.GetUint("admin_id")
	var admin db.Admin
	if err := h.DB.First(&admin, id).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "账号不存在"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(req.OldPassword), []byte(admin.PasswordHash)) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "旧密码错误"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "加密失败"})
		return
	}
	if err := h.DB.Model(&admin).Update("password_hash", string(hash)).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- virtual models ----------

type vmPayload struct {
	Name        string `json:"name"`
	BatchSize   *int   `json:"batch_size"`
	BatchWaitMs *int   `json:"batch_wait_ms"`
	PickMode    string `json:"pick_mode"`
	MaxWaitMs   *int   `json:"max_wait_ms"`
	Enabled     *bool  `json:"enabled"`
}

func (h *Handler) listVirtualModels(c *gin.Context) {
	var vms []db.VirtualModel
	if err := h.DB.Order("id").Find(&vms).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	var reals []db.RealModel
	h.DB.Order("weight desc, id").Find(&reals)
	byVM := map[uint][]db.RealModel{}
	for _, rm := range reals {
		byVM[rm.VirtualModelID] = append(byVM[rm.VirtualModelID], rm)
	}
	items := make([]gin.H, 0, len(vms))
	for _, vm := range vms {
		items = append(items, gin.H{
			"id": vm.ID, "name": vm.Name, "batch_size": vm.BatchSize,
			"batch_wait_ms": vm.BatchWaitMs, "pick_mode": vm.PickMode,
			"max_wait_ms": vm.MaxWaitMs, "enabled": vm.Enabled,
			"real_models": byVM[vm.ID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) createVirtualModel(c *gin.Context) {
	var p vmPayload
	if _, err := bindPayload(c, &p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "模型名称不能为空"})
		return
	}
	if p.PickMode == "" {
		p.PickMode = "fastest"
	}
	if !validPickMode(p.PickMode) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "pick_mode 只能是 fastest 或 weight"})
		return
	}
	vm := db.VirtualModel{
		Name:        p.Name,
		BatchSize:   derefOr(p.BatchSize, 0),
		BatchWaitMs: derefOr(p.BatchWaitMs, 3000),
		PickMode:    p.PickMode,
		MaxWaitMs:   derefOr(p.MaxWaitMs, 60000),
		Enabled:     derefOr(p.Enabled, true),
	}
	if err := h.DB.Create(&vm).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "创建失败，名称可能已存在"})
		return
	}
	c.JSON(http.StatusOK, vm)
}

func (h *Handler) getVirtualModel(c *gin.Context) {
	var vm db.VirtualModel
	if err := h.DB.First(&vm, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "虚拟模型不存在"})
		return
	}
	var reals []db.RealModel
	h.DB.Where("virtual_model_id = ?", vm.ID).Order("weight desc, id").Find(&reals)
	vm.RealModels = reals
	c.JSON(http.StatusOK, vm)
}

func (h *Handler) updateVirtualModel(c *gin.Context) {
	var vm db.VirtualModel
	if err := h.DB.First(&vm, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "虚拟模型不存在"})
		return
	}
	var p vmPayload
	m, err := bindPayload(c, &p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	updates := map[string]any{}
	if _, ok := m["name"]; ok && strings.TrimSpace(p.Name) != "" {
		updates["name"] = strings.TrimSpace(p.Name)
	}
	if _, ok := m["batch_size"]; ok {
		if p.BatchSize == nil || *p.BatchSize < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "batch_size 不能为负数"})
			return
		}
		updates["batch_size"] = *p.BatchSize
	}
	if _, ok := m["batch_wait_ms"]; ok {
		if p.BatchWaitMs == nil || *p.BatchWaitMs <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "batch_wait_ms 必须大于 0"})
			return
		}
		updates["batch_wait_ms"] = *p.BatchWaitMs
	}
	if _, ok := m["pick_mode"]; ok {
		if !validPickMode(p.PickMode) {
			c.JSON(http.StatusBadRequest, gin.H{"message": "pick_mode 只能是 fastest 或 weight"})
			return
		}
		updates["pick_mode"] = p.PickMode
	}
	if _, ok := m["max_wait_ms"]; ok {
		if p.MaxWaitMs == nil || *p.MaxWaitMs <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "max_wait_ms 必须大于 0"})
			return
		}
		updates["max_wait_ms"] = *p.MaxWaitMs
	}
	if _, ok := m["enabled"]; ok {
		if p.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "enabled 参数错误"})
			return
		}
		updates["enabled"] = *p.Enabled
	}
	if len(updates) > 0 {
		if err := h.DB.Model(&vm).Updates(updates).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "更新失败，名称可能已存在"})
			return
		}
	}
	h.DB.First(&vm, vm.ID)
	c.JSON(http.StatusOK, vm)
}

func (h *Handler) deleteVirtualModel(c *gin.Context) {
	var vm db.VirtualModel
	if err := h.DB.First(&vm, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "虚拟模型不存在"})
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("virtual_model_id = ?", vm.ID).Delete(&db.RealModel{}).Error; err != nil {
			return err
		}
		return tx.Delete(&vm).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- real models ----------

type rmPayload struct {
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Protocol     string `json:"protocol"`
	Weight       *int   `json:"weight"`
	OverrideJSON string `json:"override_json"`
	Enabled      *bool  `json:"enabled"`
}

func (h *Handler) createRealModel(c *gin.Context) {
	var vm db.VirtualModel
	if err := h.DB.First(&vm, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "虚拟模型不存在"})
		return
	}
	var p rmPayload
	m, err := bindPayload(c, &p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	if msg := validateReal(&p, m, true); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	if p.Protocol == "" {
		p.Protocol = "openai"
	}
	rm := db.RealModel{
		VirtualModelID: vm.ID,
		Name:           strings.TrimSpace(p.Name),
		BaseURL:        strings.TrimSuffix(strings.TrimSpace(p.BaseURL), "/"),
		APIKey:         p.APIKey,
		Protocol:       p.Protocol,
		Weight:         derefOr(p.Weight, 1),
		OverrideJSON:   p.OverrideJSON,
		Enabled:        derefOr(p.Enabled, true),
	}
	if err := h.DB.Create(&rm).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rm)
}

func (h *Handler) updateRealModel(c *gin.Context) {
	var rm db.RealModel
	if err := h.DB.First(&rm, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "真实模型不存在"})
		return
	}
	var p rmPayload
	m, err := bindPayload(c, &p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	if msg := validateReal(&p, m, false); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	updates := map[string]any{}
	if _, ok := m["name"]; ok && strings.TrimSpace(p.Name) != "" {
		updates["name"] = strings.TrimSpace(p.Name)
	}
	if _, ok := m["base_url"]; ok && strings.TrimSpace(p.BaseURL) != "" {
		updates["base_url"] = strings.TrimSuffix(strings.TrimSpace(p.BaseURL), "/")
	}
	if _, ok := m["api_key"]; ok {
		updates["api_key"] = p.APIKey
	}
	if _, ok := m["protocol"]; ok {
		updates["protocol"] = p.Protocol
	}
	if _, ok := m["weight"]; ok && p.Weight != nil {
		updates["weight"] = *p.Weight
	}
	if _, ok := m["override_json"]; ok {
		updates["override_json"] = p.OverrideJSON
	}
	if _, ok := m["enabled"]; ok && p.Enabled != nil {
		updates["enabled"] = *p.Enabled
	}
	if len(updates) > 0 {
		if err := h.DB.Model(&rm).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
	}
	h.DB.First(&rm, rm.ID)
	c.JSON(http.StatusOK, rm)
}

func (h *Handler) deleteRealModel(c *gin.Context) {
	res := h.DB.Delete(&db.RealModel{}, c.Param("id"))
	if res.Error != nil || res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "真实模型不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func validateReal(p *rmPayload, m map[string]any, creating bool) string {
	if creating {
		if strings.TrimSpace(p.Name) == "" {
			return "上游模型名不能为空"
		}
		if strings.TrimSpace(p.BaseURL) == "" {
			return "Base URL 不能为空"
		}
	}
	if p.Protocol != "" && p.Protocol != "openai" && p.Protocol != "gemini" {
		return "protocol 只能是 openai 或 gemini"
	}
	if _, ok := m["override_json"]; ok {
		if oj := strings.TrimSpace(p.OverrideJSON); oj != "" {
			var v map[string]any
			if err := json.Unmarshal([]byte(oj), &v); err != nil {
				return "override_json 不是合法的 JSON 对象"
			}
		}
	}
	return ""
}

// ---------- API keys ----------

func (h *Handler) listAPIKeys(c *gin.Context) {
	var keys []db.APIKey
	h.DB.Order("id").Find(&keys)
	c.JSON(http.StatusOK, gin.H{"items": keys})
}

func (h *Handler) createAPIKey(c *gin.Context) {
	var p struct {
		Name string `json:"name"`
	}
	if _, err := bindPayload(c, &p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	key := "sk-gkd-" + hex.EncodeToString(raw)
	ak := db.APIKey{Key: key, Name: strings.TrimSpace(p.Name), Enabled: true}
	if ak.Name == "" {
		ak.Name = "key-" + time.Now().Format("0102150405")
	}
	if err := h.DB.Create(&ak).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ak)
}

func (h *Handler) updateAPIKey(c *gin.Context) {
	var ak db.APIKey
	if err := h.DB.First(&ak, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "API Key 不存在"})
		return
	}
	var p struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	m, err := bindPayload(c, &p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}
	updates := map[string]any{}
	if _, ok := m["name"]; ok {
		updates["name"] = strings.TrimSpace(*p.Name)
	}
	if _, ok := m["enabled"]; ok && p.Enabled != nil {
		updates["enabled"] = *p.Enabled
	}
	if len(updates) > 0 {
		h.DB.Model(&ak).Updates(updates)
	}
	h.DB.First(&ak, ak.ID)
	c.JSON(http.StatusOK, ak)
}

func (h *Handler) deleteAPIKey(c *gin.Context) {
	res := h.DB.Delete(&db.APIKey{}, c.Param("id"))
	if res.Error != nil || res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "API Key 不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- logs ----------

func (h *Handler) listLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int64
	q := h.DB.Model(&db.RequestLog{})
	if vm := c.Query("virtual_model"); vm != "" {
		q = q.Where("virtual_model = ?", vm)
	}
	q.Count(&total)
	var logs []db.RequestLog
	q.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&logs)
	c.JSON(http.StatusOK, gin.H{"total": total, "items": logs})
}

// ---------- helpers ----------

func derefOr[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}

func validPickMode(m string) bool { return m == "fastest" || m == "weight" }
