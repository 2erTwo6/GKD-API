package main

import (
	"log"
	"net/http"

	"gkd-api/internal/config"
	"gkd-api/internal/db"
	"gkd-api/internal/server"
)

func main() {
	cfg := config.Load()

	g, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	if err := db.Bootstrap(g, cfg.AdminPassword); err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}

	r, err := server.New(g)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	log.Printf("GKD-API 已启动: http://127.0.0.1:%s  (管理页面: /)", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务启动失败: %v", err)
	}
}
