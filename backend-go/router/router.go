package router

import (
	"simnexus-go/config"
	"simnexus-go/handlers"
	"simnexus-go/middleware"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Setup 注册所有路由到 gin.Engine。
func Setup(r *gin.Engine, cfg *config.Config) {
	r.Use(middleware.SlogLogger())
	r.Use(middleware.CORS(cfg.CorsOrigins))

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// WebSocket：每 5 秒推送所有设备状态
	r.GET("/ws/modems", handlers.ModemStatusWS)
	// WebSocket：新短信（MT收/MO发）实时推送给消息中心
	r.GET("/ws/messages", handlers.MessageWS)
	// WebSocket：Telegram 消息（收/发）实时推送给管理端 Telegram 页面
	r.GET("/ws/telegram", handlers.TelegramWS)
	// WebSocket：客服会话消息实时推送给用户咨询页
	r.GET("/ws/support", handlers.SupportWS)
	// WebSocket：后端各类日志实时推送给日志页（仅管理员）
	r.GET("/ws/logs", handlers.LogsWS)

	api := r.Group("/api/v1")
	registerPublic(api)
	registerAuth(api)
}

// registerPublic 注册无需认证的公开接口。
func registerPublic(api *gin.RouterGroup) {
	// 健康检查
	api.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// 用户登录，返回 JWT
	api.POST("/auth/login", handlers.Login)
	// 获取图形验证码（SVG + JWT 签名答案）
	api.GET("/auth/captcha", handlers.GetCaptcha)

	// 附件文件下载（无需认证，文件名为 UUID 不可枚举）
	api.GET("/support/files/:filename", handlers.SupportServeFile)
	// Telegram 文件代理下载（JWT 通过 ?token= 传入，内部自行验证）
	api.GET("/telegram/file/*file_id", handlers.TelegramProxyFile)
}

// registerAuth 注册需要登录的接口（统一挂 AuthRequired 中间件）。
func registerAuth(api *gin.RouterGroup) {
	auth := api.Group("")
	auth.Use(middleware.AuthRequired())

	// 获取当前登录用户信息及 RBAC 角色
	auth.GET("/auth/me", handlers.GetMe)

	registerUsers(auth)
	registerRoles(auth)
	registerModems(auth)
	registerSMS(auth)
	registerSimRequests(auth)
	registerNotifications(auth)
	registerSupport(auth)
	registerTelegram(auth)
	registerContacts(auth)

	// 获取仪表盘统计数据（设备数、短信量等）
	auth.GET("/dashboard/stats", handlers.DashboardStats)
	// 实时推送后端日志（SSE，仅管理员）
	auth.GET("/admin/logs/stream", handlers.LogsSSE)
}

// registerUsers 用户管理路由（仅管理员）。
func registerUsers(auth *gin.RouterGroup) {
	users := auth.Group("/users")
	users.GET("/", middleware.RequireAdmin(), handlers.ListUsers)                        // 获取用户列表
	users.POST("/", middleware.RequireAdmin(), handlers.CreateUser)                      // 创建新用户
	users.PATCH("/:id", middleware.RequireAdmin(), handlers.UpdateUser)                  // 修改用户信息
	users.DELETE("/:id", middleware.RequireAdmin(), handlers.DeleteUser)                 // 删除用户
	users.POST("/:id/reset-password", middleware.RequireAdmin(), handlers.ResetPassword) // 管理员重置用户密码
	users.POST("/me/change-password", handlers.ChangePassword)                           // 当前用户修改自己的密码
}

// registerRoles 角色管理路由（仅管理员）。
func registerRoles(auth *gin.RouterGroup) {
	roles := auth.Group("/roles")
	roles.GET("/", middleware.RequireAdmin(), handlers.ListRoles)                   // 获取角色列表
	roles.POST("/", middleware.RequireAdmin(), handlers.CreateRole)                 // 创建新角色
	roles.PATCH("/:id", middleware.RequireAdmin(), handlers.UpdateRole)             // 修改角色权限
	roles.DELETE("/:id", middleware.RequireAdmin(), handlers.DeleteRole)            // 删除角色（系统角色不可删）
	roles.PUT("/users/:id/roles", middleware.RequireAdmin(), handlers.SetUserRoles) // 设置用户的 RBAC 角色
}

// registerModems 设备管理路由。
func registerModems(auth *gin.RouterGroup) {
	modems := auth.Group("/modems")
	modems.GET("/available", handlers.ListAvailableModems)  // 资源库：获取所有设备（含访问状态）
	modems.GET("/", handlers.ListModems)                    // 获取当前用户有权限的设备列表
	modems.GET("/:id", handlers.GetModem)                   // 获取单个设备基本信息
	modems.PATCH("/:id", handlers.UpdateModem)              // 修改设备别名等属性
	modems.PATCH("/:id/vowifi", handlers.SetVowifiMode)     // 切换该卡的 VoWiFi 模式（仅管理员）
	modems.PATCH("/:id/airplane", handlers.SetAirplaneMode) // 切换该卡的飞行模式（仅管理员）
	modems.GET("/:id/detail", handlers.GetModemDetail)      // 获取设备详情（含实时信号、流量等）
	modems.POST("/:id/refresh", handlers.RefreshModem)      // 手动触发单个设备立即刷新
}

// registerSMS 短信相关路由。
func registerSMS(auth *gin.RouterGroup) {
	sms := auth.Group("/sms")
	sms.POST("/send", handlers.SendSMS)                                          // 立即发送短信
	sms.GET("/messages", middleware.RequireViewHistory(), handlers.ListMessages) // 获取短信收发记录（需要查看历史权限）
	sms.DELETE("/messages/:id", handlers.DeleteMessage)                          // 删除单条短信记录
	sms.POST("/messages/batch-delete", handlers.BatchDeleteMessages)             // 批量删除短信记录
	sms.GET("/templates", handlers.ListTemplates)                                // 获取短信模板列表
	sms.POST("/templates", handlers.CreateTemplate)                              // 创建短信模板
	sms.DELETE("/templates/:id", handlers.DeleteTemplate)                        // 删除短信模板
	sms.GET("/tasks", handlers.ListTasks)                                        // 获取当前用户的定时任务列表
	sms.POST("/tasks", handlers.CreateTask)                                      // 创建定时发送任务
	sms.PATCH("/tasks/:id", handlers.UpdateTask)                                 // 修改定时任务
	sms.DELETE("/tasks/:id", handlers.DeleteTask)                                // 删除定时任务
	sms.POST("/tasks/:id/run-now", handlers.RunTaskNow)                          // 立即执行一次定时任务
	sms.GET("/admin/tasks", handlers.AdminListTasks)                             // 管理员查看所有用户的任务
	sms.GET("/admin/tasks/stats", handlers.AdminTaskStats)                       // 管理员获取任务统计数据
	sms.GET("/admin/tasks/:id/history", handlers.AdminTaskHistory)               // 管理员查看任务执行历史
}

// registerSimRequests SIM 卡访问申请路由。
func registerSimRequests(auth *gin.RouterGroup) {
	sr := auth.Group("/sim-requests")
	sr.POST("/", handlers.CreateSimRequest)                                               // 用户申请访问某张 SIM 卡
	sr.GET("/my", handlers.MyRequests)                                                    // 获取当前用户的申请记录
	sr.GET("/my-grants", handlers.MyGrants)                                               // 获取当前用户已获批的授权列表
	sr.GET("/", middleware.RequireApproveRequests(), handlers.ListRequests)               // 审批员查看待审申请列表
	sr.PUT("/:id/approve", middleware.RequireApproveRequests(), handlers.ApproveRequest)  // 审批通过申请
	sr.PUT("/:id/reject", middleware.RequireApproveRequests(), handlers.RejectRequest)    // 拒绝申请
	sr.POST("/batch-approve", middleware.RequireApproveRequests(), handlers.BatchApprove) // 批量审批通过
	sr.POST("/grant", middleware.RequireApproveRequests(), handlers.DirectGrant)          // 直接授权（无需申请流程）
	sr.DELETE("/grants/:id", middleware.RequireApproveRequests(), handlers.RevokeGrant)   // 撤销已授权的访问
}

// registerNotifications 通知路由。
func registerNotifications(auth *gin.RouterGroup) {
	notif := auth.Group("/notifications")
	notif.GET("", handlers.ListNotifications)        // 获取当前用户的通知列表（按 audience 过滤）
	notif.GET("/unread-count", handlers.UnreadCount) // 获取未读通知数量
	notif.POST("/read-all", handlers.MarkAllRead)    // 一键标记所有通知为已读
	notif.POST("/:id/read", handlers.MarkOneRead)    // 标记单条通知为已读
}

// registerSupport 客服支持路由。
func registerSupport(auth *gin.RouterGroup) {
	support := auth.Group("/support")
	support.POST("/upload", handlers.SupportUpload)              // 上传附件（图片/文件），返回访问 URL
	support.POST("/messages", handlers.SupportSendMessage)       // 发送支持消息（用户或客服）
	support.GET("/messages", handlers.SupportGetMessages)        // 获取与指定用户的消息记录
	support.POST("/messages/read", handlers.SupportMarkRead)     // 标记消息为已读
	support.GET("/unread", handlers.SupportUnread)               // 获取未读消息数量
	support.GET("/conversations", handlers.SupportConversations) // 客服获取所有会话列表
}

// registerTelegram Telegram 管理路由（仅管理员）。
func registerTelegram(auth *gin.RouterGroup) {
	tg := auth.Group("/telegram")
	tg.GET("/messages", middleware.RequireAdmin(), handlers.TelegramListMessages)     // 获取 Telegram 消息记录
	tg.POST("/send", middleware.RequireAdmin(), handlers.TelegramSend)                // 向 Telegram 发送文字消息
	tg.POST("/send-file", middleware.RequireAdmin(), handlers.TelegramSendFile)       // 向 Telegram 发送图片或文件
	tg.DELETE("/messages", middleware.RequireAdmin(), handlers.TelegramClearMessages) // 清空 Telegram 消息记录
	tg.GET("/config", middleware.RequireAdmin(), handlers.TelegramConfig)             // 查看 Bot 配置状态
	// Bot 可视化设置（token/chat/白名单，DB 保存 + 热重启）
	tg.GET("/settings", middleware.RequireAdmin(), handlers.GetTelegramSettings)
	tg.PUT("/settings", middleware.RequireAdmin(), handlers.SaveTelegramSettings)
	// 自定义命令 CRUD
	tg.GET("/commands", middleware.RequireAdmin(), handlers.ListTelegramCommands)
	tg.POST("/commands", middleware.RequireAdmin(), handlers.CreateTelegramCommand)
	tg.PATCH("/commands/:id", middleware.RequireAdmin(), handlers.UpdateTelegramCommand)
	tg.DELETE("/commands/:id", middleware.RequireAdmin(), handlers.DeleteTelegramCommand)
	// 生成 Telegram 绑定码（任意登录用户，用于 /bind 绑定自己的账号）
	tg.POST("/bind-code", handlers.GenTelegramBindCode)
	// 列出/解绑当前账号名下的 Telegram 绑定（任意登录用户）
	tg.GET("/binds", handlers.ListMyTelegramBinds)
	tg.DELETE("/binds/:id", handlers.DeleteMyTelegramBind)
}

// registerContacts 通讯录路由（每个用户私有）。
func registerContacts(auth *gin.RouterGroup) {
	ct := auth.Group("/contacts")
	ct.GET("/", handlers.ListContacts)        // 获取当前用户的通讯录
	ct.POST("/", handlers.CreateContact)      // 新建联系人
	ct.PATCH("/:id", handlers.UpdateContact)  // 修改联系人
	ct.DELETE("/:id", handlers.DeleteContact) // 删除联系人
}
