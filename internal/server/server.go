package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gkd-api/internal/admin"
	"gkd-api/internal/db"
	"gkd-api/internal/relay"
	"gkd-api/web"
)

func New(g *gorm.DB) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), cors())

	secret := func() string {
		s, _ := db.GetSetting(g, "jwt_secret")
		return s
	}
	admin.Register(r.Group("/api/admin"), g, secret)

	svc := relay.New(g)
	svc.RegisterRoutes(r.Group("/v1"))

	registerStatic(r)
	return r, nil
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-goog-api-key")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func registerStatic(r *gin.Engine) {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))
	indexHTML, indexErr := fs.ReadFile(web.Dist, "dist/index.html")
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/") {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "not found", "type": "invalid_request_error"}})
			return
		}
		if f, err := sub.Open(strings.TrimPrefix(p, "/")); err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		if indexErr != nil {
			c.String(http.StatusNotFound, "前端尚未构建，请先执行 make build-web")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
}
