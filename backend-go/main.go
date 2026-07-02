// @title SimNexus API
// @version 1.0
// @description 多USB 4G Modem 管理系统 API
// @host localhost:8000
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description 格式: Bearer {token}
package main

import (
	"context"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // 注册 pprof 路由到 http.DefaultServeMux
	"os"

	"simnexus-go/config"
	"simnexus-go/database"
	_ "simnexus-go/docs"
	"simnexus-go/router"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
)

// main 是程序入口：初始化日志缓冲、配置、数据库，启动后台服务，注册路由，监听 :8000。
func main() {
	// 初始化双写日志：同时输出到 stdout 和内存缓冲区（供 SSE 日志流使用）
	inner := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(services.NewBufferedHandler(inner)))

	cfg := config.Load()
	database.Init(cfg)

	// pprof 监听在独立端口 6060，不暴露到主业务端口
	go func() {
		slog.Info("pprof listening", "addr", ":6060")
		if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
			slog.Error("pprof server error", "err", err)
		}
	}()

	// 启动后台服务：短信调度器、设备轮询、Telegram Bot 长轮询
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	services.StartScheduler()
	go services.StartPolling(ctx)
	go services.StartTelegramPolling(ctx)

	r := gin.New()
	r.Use(gin.Recovery())
	router.Setup(r, cfg)

	slog.Info("backend starting", "app", cfg.AppName, "addr", ":8000")
	if err := r.Run("0.0.0.0:8000"); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}

}
