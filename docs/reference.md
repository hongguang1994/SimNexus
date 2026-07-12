# SimNexus 参考手册

项目结构、页面路由、REST/WebSocket 接口、权限与通知模型的详细说明。功能介绍与部署见根目录 [README.md](../README.md)。

> 后端为 **Go**（gin + gorm），所有 REST 接口前缀 `/api/v1`，WebSocket 前缀 `/ws`（无 `/api/v1`）。

---

## 项目结构

```
SimNexus/
├── backend-go/
│   ├── main.go                     # 入口：初始化日志缓冲、DB、后台服务、路由，监听 :8000
│   ├── config/                     # 环境变量配置加载
│   ├── database/                   # gorm 连接 + AutoMigrate + ensureColumns
│   ├── models/                     # 数据模型（user/role/modem/sms/contact/…）
│   ├── middleware/                 # 认证、CORS、HTTP 日志中间件
│   ├── security/                   # JWT、bcrypt、权限解析
│   ├── router/router.go            # 全部路由注册
│   ├── handlers/                   # HTTP 处理器（每个业务域一个文件）
│   ├── services/
│   │   ├── modem_manager.go        # mmcli 封装（标准 AT 命令设备）
│   │   ├── modem_poller.go         # 后台轮询设备状态（mmcli + ZTE），按 ICCID 识别
│   │   ├── sms_scheduler.go        # 定时任务引擎（cron / 单次）
│   │   ├── zte_http_modem.go       # ZTE 随身 WiFi goform HTTP 驱动
│   │   ├── vowifi_manager.go       # VoWiFi 每卡常驻会话 + 看门狗自愈
│   │   ├── vowifi_service.go       # VoWiFi 业务接入（含 ePDG 动态解析）
│   │   ├── logbuffer.go            # 日志环形缓冲 + slog Handler（供日志页）
│   │   └── vowifi/                 # 自建 VoWiFi 协议栈（IKEv2/EAP-AKA/IMS/ESP）
│   ├── cmd/epdgprobe/              # ePDG DNS 解析独立验证探针
│   └── Dockerfile
├── frontend/
│   ├── src/
│   │   ├── api/                    # Axios 客户端 + 各域接口（含 contacts.ts）
│   │   ├── components/Layout.tsx   # 主布局（侧边栏、顶栏、通知铃铛）
│   │   ├── hooks/useModemSocket.ts # 设备状态 WebSocket
│   │   ├── i18n/{zh,en}.ts         # 中英文案
│   │   ├── pages/                  # 页面（见「页面路由」）
│   │   └── store/                  # Zustand：auth / modem / lang / theme
│   ├── Dockerfile
│   └── nginx.conf                  # 静态托管 + /api、/ws 反代到 backend:8000
├── docs/                           # 本手册、数据库 schema、拓扑图
├── scripts/setup-debian.sh         # 本地开发系统依赖（ModemManager/udev/dialout）
├── deploy.sh                       # 一键部署
└── docker-compose.yml
```

---

## 页面路由

| 路径 | 页面 | 访问要求 |
|------|------|---------|
| `/login` | 登录（含图形验证码） | 公开 |
| `/` | 设备总览 Dashboard | 登录 |
| `/sim-cards` | SIM 卡管理 | `can_view_sim` |
| `/resources` | 资源库（所有卡 + 访问状态） | `can_view_sim` |
| `/modems/:id` | 单卡详情（含 VoWiFi / 飞行模式） | `can_view_sim` |
| `/history` | 消息中心（iMessage 风格收发 + 稍后发送） | 登录 |
| `/contacts` | 通讯录（每用户私有） | 登录 |
| `/admin/tasks` | 任务监控 / 我的任务记录 | 非只读（管理员看全部） |
| `/admin/sim-requests` | SIM 申请审批 | `can_approve_requests` |
| `/support` | 用户咨询管理 | 管理员 / `can_support` |
| `/users` | 用户管理 | 管理员 |
| `/roles` | 角色管理 | 管理员 |
| `/admin/telegram` | Telegram 管理 | 管理员 |
| `/admin/logs` | 系统日志（分类标签页 + WebSocket） | 管理员 |

> 已下线页面：独立「定时任务」页（定时改由消息中心「稍后发送」创建）、「短信模板」页。

---

## REST 接口（前缀 `/api/v1`）

### 认证
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/auth/captcha` | 获取 SVG 验证码（token + svg） |
| POST | `/auth/login` | 登录（传 captcha_token + captcha_code） |
| GET | `/auth/me` | 当前用户信息（含 RBAC 角色） |

### 用户 / 角色（管理员）
| 方法 | 路径 | 说明 |
|------|------|------|
| GET/POST | `/users/` | 列表 / 创建 |
| PATCH/DELETE | `/users/:id` | 修改 / 删除 |
| POST | `/users/:id/reset-password` | 重置密码 |
| POST | `/users/me/change-password` | 修改自己的密码 |
| GET/POST | `/roles/` | 列表 / 创建 |
| PATCH/DELETE | `/roles/:id` | 修改 / 删除（系统角色不可删） |
| PUT | `/roles/users/:id/roles` | 设置用户的角色列表 |

### 设备
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/modems/available` | 资源库：所有设备 + 访问状态 |
| GET | `/modems/` | 当前用户有权限的设备 |
| GET | `/modems/:id` `/modems/:id/detail` | 基本信息 / 详情（信号、流量、VoWiFi 运行态） |
| PATCH | `/modems/:id` | 修改别名 |
| PATCH | `/modems/:id/vowifi` | 切换 VoWiFi 模式（管理员） |
| PATCH | `/modems/:id/airplane` | 切换飞行模式（管理员） |
| POST | `/modems/:id/refresh` | 立即刷新 |

### 短信与定时任务
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/sms/send` | 立即发送 |
| GET | `/sms/messages` | 收发记录（需查看历史权限） |
| DELETE | `/sms/messages/:id` · POST `/sms/messages/batch-delete` | 删除 / 批量删除 |
| GET/POST | `/sms/tasks` | 定时任务列表 / 创建（消息中心「稍后发送」即调此） |
| PATCH/DELETE | `/sms/tasks/:id` | 修改 / 删除 |
| POST | `/sms/tasks/:id/run-now` | 立即执行一次 |
| GET | `/sms/admin/tasks` · `/sms/admin/tasks/stats` · `/sms/admin/tasks/:id/history` | 管理员：全部任务 / 统计 / 历史 |
| GET/POST/DELETE | `/sms/templates` `/sms/templates/:id` | 模板接口（保留，前端页已下线） |

### 通讯录（每用户私有）
| 方法 | 路径 | 说明 |
|------|------|------|
| GET/POST | `/contacts/` | 列表 / 新建 |
| PATCH/DELETE | `/contacts/:id` | 修改 / 删除 |

### SIM 申请审批
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/sim-requests/` | 申请访问某卡 |
| GET | `/sim-requests/my` · `/sim-requests/my-grants` | 我的申请 / 我的授权 |
| GET | `/sim-requests/` | 审批员：待审列表 |
| PUT | `/sim-requests/:id/approve` · `/:id/reject` | 通过 / 拒绝 |
| POST | `/sim-requests/batch-approve` · `/grant` | 批量通过 / 直接授权 |
| DELETE | `/sim-requests/grants/:id` | 撤销授权 |

### 通知 / 用户咨询 / Telegram
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/notifications` · `/notifications/unread-count` | 列表 / 未读数 |
| POST | `/notifications/read-all` · `/notifications/:id/read` | 全部已读 / 单条已读 |
| POST/GET | `/support/messages` | 发送 / 拉取咨询消息 |
| POST | `/support/upload` · `/support/messages/read` | 上传附件 / 标记已读 |
| GET | `/support/unread` · `/support/conversations` | 未读数 / 会话列表（客服） |
| GET | `/support/files/:filename` | 附件下载（公开，UUID 名） |
| GET/POST | `/telegram/messages` · `/telegram/send` · `/telegram/send-file` | 消息列表 / 发文字 / 发文件（管理员） |
| DELETE/GET | `/telegram/messages` · `/telegram/config` | 清空 / Bot 状态（管理员） |
| GET | `/telegram/file/*file_id` | 代理下载（JWT 经 `?token=`） |

### 其它
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/dashboard/stats` | 仪表盘统计 |
| GET | `/admin/logs/stream` | 日志 SSE 流（管理员，WebSocket 见下） |

---

## WebSocket（前缀 `/ws`，JWT 经 `?token=` 传入）

| 端点 | 用途 |
|------|------|
| `/ws/modems` | 每 5 秒推送所有设备状态 |
| `/ws/messages` | 短信收发（MT/MO）实时推送给消息中心 |
| `/ws/support` | 客服会话消息实时推送 |
| `/ws/telegram` | Telegram 消息实时推送给管理端 |
| `/ws/logs` | 后端各类日志实时推送给日志页（管理员）；分类：http/vowifi/poller/scheduler/telegram/system |

---

## 权限模型

```
系统角色（admin / user）
    ↓
RBAC 角色列表（可多个，取并集）
```

- **管理员**（`role=admin`）始终拥有全部权限。
- **普通用户**的有效权限由其所有 RBAC 角色合并：
  - 功能权限（`can_view_sim` / `can_approve_requests` / `can_manage_tasks` / `can_view_history` / `can_support`）：任一角色开启即生效
  - 只读（`read_only`）：所有角色均只读才生效
  - 设备范围（`allowed_modem_ids`）：任一角色无限制则无限制，否则取受限角色的设备 ID 并集
- 审批员对其管理范围内的卡自动拥有 use 级访问，无需申请。

---

## 通知受众

| audience | 可见对象 |
|----------|---------|
| `admin` | 仅系统管理员 |
| `support` | 管理员 + 拥有 `can_support` 的角色 |
| `all` | 所有已登录用户 |
| `user` + target_user_id | 仅指定用户 |
