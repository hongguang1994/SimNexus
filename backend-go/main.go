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
	"simnexus-go/handlers"
	"simnexus-go/middleware"
	"simnexus-go/services"

	"github.com/gin-gonic/gin"
	ginSwagger "github.com/swaggo/gin-swagger"
	swaggerFiles "github.com/swaggo/files"
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
	r.Use(middleware.SlogLogger())
	r.Use(middleware.CORS(cfg.CorsOrigins))

	api := r.Group("/api/v1")

	// 健康检查（无需认证）
	api.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// ── 公开接口（无需登录）──────────────────────────────────────────
	api.POST("/auth/login", handlers.Login)       // 用户登录，返回 JWT
	api.GET("/auth/captcha", handlers.GetCaptcha) // 获取图形验证码（SVG + JWT 签名答案）

	// ── 需要登录的接口组 ─────────────────────────────────────────────
	auth := api.Group("")
	auth.Use(middleware.AuthRequired())

	auth.GET("/auth/me", handlers.GetMe) // 获取当前登录用户信息及 RBAC 角色

	// ── 用户管理（仅管理员）─────────────────────────────────────────
	users := auth.Group("/users")
	{
		users.GET("/", middleware.RequireAdmin(), handlers.ListUsers)                    // 获取用户列表
		users.POST("/", middleware.RequireAdmin(), handlers.CreateUser)                  // 创建新用户
		users.PATCH("/:id", middleware.RequireAdmin(), handlers.UpdateUser)              // 修改用户信息
		users.DELETE("/:id", middleware.RequireAdmin(), handlers.DeleteUser)             // 删除用户
		users.POST("/:id/reset-password", middleware.RequireAdmin(), handlers.ResetPassword) // 管理员重置用户密码
		users.POST("/me/change-password", handlers.ChangePassword)                      // 当前用户修改自己的密码
	}

	// ── 角色管理（仅管理员）─────────────────────────────────────────
	roles := auth.Group("/roles")
	{
		roles.GET("/", middleware.RequireAdmin(), handlers.ListRoles)                    // 获取角色列表
		roles.POST("/", middleware.RequireAdmin(), handlers.CreateRole)                  // 创建新角色
		roles.PATCH("/:id", middleware.RequireAdmin(), handlers.UpdateRole)              // 修改角色权限
		roles.DELETE("/:id", middleware.RequireAdmin(), handlers.DeleteRole)             // 删除角色（系统角色不可删）
		roles.PUT("/users/:id/roles", middleware.RequireAdmin(), handlers.SetUserRoles)  // 设置用户的 RBAC 角色
	}

	// ── 设备管理 ────────────────────────────────────────────────────
	modems := auth.Group("/modems")
	{
		modems.GET("/available", handlers.ListAvailableModems)         // 资源库：获取所有设备（含访问状态）
		modems.GET("/", handlers.ListModems)                           // 获取当前用户有权限的设备列表
		modems.GET("/:id", handlers.GetModem)                         // 获取单个设备基本信息
		modems.PATCH("/:id", handlers.UpdateModem)                    // 修改设备别名等属性
		modems.GET("/:id/detail", handlers.GetModemDetail)            // 获取设备详情（含实时信号、流量等）
		modems.POST("/:id/refresh", handlers.RefreshModem)            // 手动触发单个设备立即刷新
	}

	// ── 短信 ────────────────────────────────────────────────────────
	sms := auth.Group("/sms")
	{
		sms.POST("/send", handlers.SendSMS)                                              // 立即发送短信
		sms.GET("/messages", middleware.RequireViewHistory(), handlers.ListMessages)     // 获取短信收发记录（需要查看历史权限）
		sms.DELETE("/messages/:id", handlers.DeleteMessage)                              // 删除单条短信记录
		sms.POST("/messages/batch-delete", handlers.BatchDeleteMessages)                 // 批量删除短信记录
		sms.GET("/templates", handlers.ListTemplates)                                    // 获取短信模板列表
		sms.POST("/templates", handlers.CreateTemplate)                                  // 创建短信模板
		sms.DELETE("/templates/:id", handlers.DeleteTemplate)                            // 删除短信模板
		sms.GET("/tasks", handlers.ListTasks)                                            // 获取当前用户的定时任务列表
		sms.POST("/tasks", handlers.CreateTask)                                          // 创建定时发送任务
		sms.PATCH("/tasks/:id", handlers.UpdateTask)                                     // 修改定时任务
		sms.DELETE("/tasks/:id", handlers.DeleteTask)                                    // 删除定时任务
		sms.POST("/tasks/:id/run-now", handlers.RunTaskNow)                              // 立即执行一次定时任务
		sms.GET("/admin/tasks", handlers.AdminListTasks)                                 // 管理员查看所有用户的任务
		sms.GET("/admin/tasks/stats", handlers.AdminTaskStats)                           // 管理员获取任务统计数据
		sms.GET("/admin/tasks/:id/history", handlers.AdminTaskHistory)                   // 管理员查看任务执行历史
	}

	// ── SIM 卡访问申请 ───────────────────────────────────────────────
	sr := auth.Group("/sim-requests")
	{
		sr.POST("/", handlers.CreateSimRequest)                                          // 用户申请访问某张 SIM 卡
		sr.GET("/my", handlers.MyRequests)                                               // 获取当前用户的申请记录
		sr.GET("/my-grants", handlers.MyGrants)                                          // 获取当前用户已获批的授权列表
		sr.GET("/", middleware.RequireApproveRequests(), handlers.ListRequests)           // 审批员查看待审申请列表
		sr.PUT("/:id/approve", middleware.RequireApproveRequests(), handlers.ApproveRequest)  // 审批通过申请
		sr.PUT("/:id/reject", middleware.RequireApproveRequests(), handlers.RejectRequest)    // 拒绝申请
		sr.POST("/batch-approve", middleware.RequireApproveRequests(), handlers.BatchApprove) // 批量审批通过
		sr.POST("/grant", middleware.RequireApproveRequests(), handlers.DirectGrant)          // 直接授权（无需申请流程）
		sr.DELETE("/grants/:id", middleware.RequireApproveRequests(), handlers.RevokeGrant)   // 撤销已授权的访问
	}

	// ── 通知 ────────────────────────────────────────────────────────
	notif := auth.Group("/notifications")
	{
		notif.GET("", handlers.ListNotifications)           // 获取当前用户的通知列表（按 audience 过滤）
		notif.GET("/unread-count", handlers.UnreadCount)    // 获取未读通知数量
		notif.POST("/read-all", handlers.MarkAllRead)       // 一键标记所有通知为已读
		notif.POST("/:id/read", handlers.MarkOneRead)       // 标记单条通知为已读
	}

	// ── 客服支持 ─────────────────────────────────────────────────────
	support := auth.Group("/support")
	{
		support.POST("/upload", handlers.SupportUpload)              // 上传附件（图片/文件），返回访问 URL
		support.POST("/messages", handlers.SupportSendMessage)       // 发送支持消息（用户或客服）
		support.GET("/messages", handlers.SupportGetMessages)        // 获取与指定用户的消息记录
		support.POST("/messages/read", handlers.SupportMarkRead)     // 标记消息为已读
		support.GET("/unread", handlers.SupportUnread)               // 获取未读消息数量
		support.GET("/conversations", handlers.SupportConversations) // 客服获取所有会话列表
	}
	// 附件文件下载（无需认证，文件名为 UUID 不可枚举）
	api.GET("/support/files/:filename", handlers.SupportServeFile)

	// ── 仪表盘 ───────────────────────────────────────────────────────
	auth.GET("/dashboard/stats", handlers.DashboardStats) // 获取仪表盘统计数据（设备数、短信量等）

	// ── Telegram 管理（仅管理员）────────────────────────────────────
	tg := auth.Group("/telegram")
	{
		tg.GET("/messages", middleware.RequireAdmin(), handlers.TelegramListMessages)     // 获取 Telegram 消息记录
		tg.POST("/send", middleware.RequireAdmin(), handlers.TelegramSend)                // 向 Telegram 发送文字消息
		tg.POST("/send-file", middleware.RequireAdmin(), handlers.TelegramSendFile)       // 向 Telegram 发送图片或文件
		tg.DELETE("/messages", middleware.RequireAdmin(), handlers.TelegramClearMessages) // 清空 Telegram 消息记录
		tg.GET("/config", middleware.RequireAdmin(), handlers.TelegramConfig)             // 查看 Bot 配置状态
	}
	// Telegram 文件代理下载（JWT 通过 ?token= 传入，内部自行验证）
	api.GET("/telegram/file/*file_id", handlers.TelegramProxyFile)

	// ── 系统日志 SSE 流（JWT 通过 ?token= 传入）────────────────────
	auth.GET("/admin/logs/stream", handlers.LogsSSE) // 实时推送后端日志（SSE，仅管理员）

	// ── Swagger UI ──────────────────────────────────────────────────
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// ── WebSocket ───────────────────────────────────────────────────
	r.GET("/ws/modems", handlers.ModemStatusWS) // WebSocket：每 5 秒推送所有设备状态

	slog.Info("backend starting", "app", cfg.AppName, "addr", ":8000")
	if err := r.Run("0.0.0.0:8000"); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
