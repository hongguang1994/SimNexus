# SimNexus

基于 Debian 的多 USB 4G 模块管理系统，支持多张 SIM 卡统一管理、实时状态监控、流量统计、短信收发、定时任务、用户权限控制与在线客服。

## 功能特性

### 设备与通信
- **SIM 卡管理** — 列出所有 SIM 卡的网络状态、信号强度、网络制式（5G/4G/3G）、注册状态、运营商、流量统计、在线时长、短信统计
- **多卡监控** — 同时管理多个 USB 4G 调制解调器，自动识别热插拔
- **ZTE 随身 WiFi 支持** — 通过 HTTP goform API 管理 ZTE 随身 WiFi（无需 mmcli），自动发现、状态轮询、短信收发，IMSI 推断运营商名称
- **VoWiFi / Wi-Fi Calling** — 自建 IKEv2/EAP-AKA + IMS 协议栈，让漫游中被拒的 SIM（如 giffgaff 漫游中国移动）**经 ePDG 走 Wi-Fi 收发短信**；按卡开关，独占该卡串口，MT 收信自动回 RP-ACK、MO 发送批量摊薄鉴权、心跳看门狗自愈
- **实时推送** — 设备状态经 WebSocket 每 5 秒刷新；**短信 / 客服 / Telegram 三个消息界面为 WebSocket 即时推送**（收发秒级到达，轮询仅作兜底）
- **卡身份按 ICCID 识别** — 以 SIM 的 ICCID 为主键：同卡换模组仍是同一张卡，同模组换卡则视为新卡
- **手动发送** — 选定 SIM 卡后立即发送短信，支持从模板选择内容
- **短信模板** — 创建可复用模板，支持 `{变量名}` 占位符，发送时逐一填写变量值并实时预览
- **收件同步** — 自动拉取各设备收件箱并入库，按 `mm_sms_index` 去重
- **定时任务** — 支持 Cron 表达式（循环）和指定时间（单次）两种模式，支持群发多个号码

### 用户与权限（RBAC）
- **JWT 登录认证** — 用户名/密码 + 图形验证码（SVG，5 分钟有效期）
- **角色管理** — 可自定义角色，每个角色独立配置功能权限
- **多角色分配** — 每个用户可同时分配多个角色，权限取所有角色的并集
- **权限维度**
  - 功能模块：查看 SIM 卡 / 发送短信 / 管理定时任务 / 查看短信记录 / 客服回复
  - 操作类型：读写 / 只读
  - 设备范围：全部设备 / 指定设备 ID
- **系统预置角色** — 全功能用户、只读用户、短信操作员、任务管理员、客服
- **用户管理** — 创建/禁用用户、重置密码、分配角色

### 通知系统
- **实时通知** — 铃铛图标轮询未读数，支持全部标记已读
- **通知受众** — `admin`（管理员）、`support`（管理员+客服）、`all`（所有用户）、`user`（指定用户）
- **通知类型** — 设备上线/离线、短信发送失败、定时任务失败、新用户注册、用户咨询、客服回复

### 管理功能
- **任务监控** — 管理员专属页面，查看所有用户的定时任务、执行统计（运行中/已暂停/已完成/失败）、历史记录
- **消息中心** — iMessage 风格的短信收发界面，按（卡 + 对端号码）分会话、多卡筛选、乐观发送、WebSocket 实时到达
- **用户咨询** — 用户与客服/管理员实时聊天（WebSocket），支持文字、图片、文件附件
- **Telegram 机器人** — 收到短信自动推送到 Telegram；在 Telegram 里用 `/modems`、`/send #<卡ID> <号码> <内容>`、`/list` 远程收发短信；**chat_id 白名单鉴权**，非授权用户发任何消息一律忽略
- **响应式 + 主题** — 适配手机 / 平板 / 桌面；浅色 / 深色 / 跟随系统；中文 / 英文

---

## 系统要求

- Debian 11 / 12（或 Ubuntu 20.04+）
- Docker Engine + Docker Compose v2（**推荐部署方式**）
- 宿主机安装并运行 ModemManager 1.18+（供容器内 mmcli 使用）
- USB 4G 模块（EC25、SIM7600 等主流模组）或 ZTE 随身 WiFi（CDC Ethernet 模式）
- 后端为 **Go**（`backend-go/`）、前端为 React + Vite；本地开发另需 Go 1.22+ 与 Node.js 18+

> 后端已由早期的 Python/FastAPI 迁移为 Go。生产部署走 Docker，无需在宿主机装 Go/Python。

---

## 快速开始

### 一键部署（推荐）

在装好 Docker 的宿主机上：

```bash
git clone https://github.com/hongguang1994/SimNexus.git
cd SimNexus
./deploy.sh
```

`deploy.sh` 会自动完成：环境检查 → 生成 `.env`（密钥不入库）→ **首次部署导入初始数据（账号 `admin` / `admin123`）** → 构建并启动前后端容器。完成后访问 `http://<服务器IP>:8899`。

> **密钥配置**：`.env` 由 `.env.example` 生成，`TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` 按需填写（留空则禁用 Telegram），`.env` 已被 `.gitignore` 忽略，不会提交。

### 手动步骤（等价于一键脚本）

```bash
cp .env.example .env          # 填入 Telegram 等密钥（可留空）
sqlite3 data/sim_manager.db < docs/schema.sql   # 仅首次：导入初始数据
docker compose up -d --build
```

### 本地开发环境（非 Docker）

先安装系统依赖（ModemManager、udev 规则、dialout 组）：

```bash
sudo bash scripts/setup-debian.sh
```

**后端（Go）**

```bash
cd backend-go
sqlite3 ./data/sim_manager.db < ../docs/schema.sql   # 仅首次：导入初始数据
go run .                                              # 监听 :8000
```

默认管理员账号：`admin` / `admin123`（请登录后立即修改密码）。

**前端**（新终端）

```bash
cd frontend
npm install
npm run dev                                           # http://localhost:5173，已代理 /api、/ws 到 :8000
```

---

## Docker 部署细节

**前提条件**

- 宿主机已安装并运行 ModemManager：

  ```bash
  sudo apt install modemmanager
  sudo systemctl enable --now ModemManager
  systemctl status ModemManager   # 确认 active (running)
  ```

- 宿主机已安装 Docker 和 Docker Compose：

  ```bash
  docker --version
  docker compose version
  ```

**启动**

```bash
git clone https://github.com/hongguang1994/SimNexus.git
cd SimNexus
docker compose up -d
```

首次启动会自动构建镜像（约 2–3 分钟），之后访问 `http://<服务器IP>:8899`。

**网络架构**

两个容器均运行在 Docker bridge 网络（`simnexus_default`）中：

```
浏览器
  ↓ :80
simnexus-frontend（nginx）
  ├── GET /、/login 等  →  返回静态文件（React SPA）
  ├── GET /api/*        →  proxy_pass → simnexus-backend:8000
  └── WS  /ws/*         →  proxy_pass → simnexus-backend:8000（WebSocket）
```

backend 容器通过挂载宿主机 D-Bus socket（`/run/dbus/system_bus_socket`）与宿主机的 ModemManager 通信，**不在容器内启动 ModemManager**。

**ZTE 随身 WiFi**：插入后宿主机会出现 CDC Ethernet 网卡（如 `enx344b50000000`），需手动配置 IP：

```bash
ip link set enx344b50000000 up
ip addr add 192.168.0.100/24 dev enx344b50000000
```

后端容器通过路由可直接访问 `192.168.0.1`（ZTE 设备网关），无需 `network_mode: host`。

**数据持久化**

SQLite 数据库和上传文件存储在宿主机的 `./data/` 目录，容器重建后数据不丢失：

```
SimNexus/
└── data/
    └── sim_manager.db
```

**常用运维命令**

```bash
# 查看运行状态
docker compose ps

# 查看日志
docker compose logs -f backend
docker compose logs -f frontend

# 重启服务
docker compose restart

# 停止服务
docker compose down

# 更新代码后重新构建
git pull
docker compose build
docker compose up -d
```

**旧版 systemd 服务迁移**

如果服务器之前使用 systemd + 宿主机 nginx 部署，需先停止旧服务再启动 Docker：

```bash
sudo systemctl stop nginx simnexus
sudo systemctl disable nginx simnexus
docker compose up -d
```

> Docker backend 容器如果设置了 `network_mode: host`，会与宿主机 8000 端口冲突。当前版本已改为 bridge 网络，不存在此问题。

---

## 项目结构

```
SimNexus/
├── backend/
│   ├── app/
│   │   ├── api/
│   │   │   ├── auth.py            # 登录、获取当前用户
│   │   │   ├── captcha.py         # SVG 验证码生成与校验
│   │   │   ├── modems.py          # 设备管理接口
│   │   │   ├── notifications.py   # 通知读取与已读标记
│   │   │   ├── roles.py           # 角色 CRUD、用户角色分配
│   │   │   ├── sms.py             # 短信发送、记录、定时任务
│   │   │   ├── support.py         # 用户咨询消息、文件上传
│   │   │   ├── users.py           # 用户管理、权限设置
│   │   │   └── ws.py              # WebSocket 实时推送
│   │   ├── core/
│   │   │   ├── config.py          # 配置项
│   │   │   ├── database.py        # SQLAlchemy 连接
│   │   │   └── security.py        # JWT、密码哈希、权限依赖
│   │   ├── models/
│   │   │   ├── modem.py           # 调制解调器模型
│   │   │   ├── notification.py    # 通知模型
│   │   │   ├── permission.py      # 旧版独立权限（兼容保留）
│   │   │   ├── role.py            # RBAC 角色模型
│   │   │   ├── sms.py             # 短信、模板、定时任务模型
│   │   │   ├── support.py         # 用户咨询消息模型
│   │   │   └── user.py            # 用户模型 + user_roles 中间表
│   │   ├── schemas/               # Pydantic 请求/响应结构
│   │   ├── services/
│   │   │   ├── modem_manager.py   # mmcli 封装（标准 AT 命令设备）
│   │   │   ├── modem_poller.py    # 后台轮询设备状态（mmcli + ZTE）
│   │   │   ├── notify.py          # 通知推送工具函数
│   │   │   ├── sms_scheduler.py   # APScheduler 定时任务引擎
│   │   │   └── zte_http_modem.py  # ZTE 随身 WiFi goform HTTP 驱动
│   │   └── main.py                # 应用入口，create_all 建表
│   ├── requirements.txt
│   └── Dockerfile
├── frontend/
│   ├── src/
│   │   ├── api/
│   │   │   ├── auth.ts            # 登录、用户、权限接口
│   │   │   ├── client.ts          # Axios 基础配置（自动附 JWT）
│   │   │   ├── modems.ts          # 设备接口
│   │   │   ├── notifications.ts   # 通知接口
│   │   │   ├── roles.ts           # 角色接口
│   │   │   ├── sms.ts             # 短信、定时任务接口
│   │   │   └── support.ts         # 用户咨询接口
│   │   ├── components/
│   │   │   ├── Layout.tsx         # 主布局（侧边栏、顶栏、通知铃铛）
│   │   │   ├── ModemCard.tsx      # 设备卡片
│   │   │   └── SupportChat.tsx    # 用户端咨询聊天框
│   │   ├── hooks/
│   │   │   └── useModemSocket.ts  # WebSocket 连接管理
│   │   ├── i18n/
│   │   │   ├── zh.ts              # 中文翻译
│   │   │   └── en.ts              # 英文翻译
│   │   ├── pages/
│   │   │   ├── AdminTasks.tsx     # 管理员任务监控
│   │   │   ├── Dashboard.tsx      # 设备总览
│   │   │   ├── Login.tsx          # 登录页（含验证码）
│   │   │   ├── Roles.tsx          # 角色管理
│   │   │   ├── ScheduledTasks.tsx # 定时任务（用户视图）
│   │   │   ├── SimCards.tsx       # SIM 卡列表
│   │   │   ├── SimDetail.tsx      # 单卡详情
│   │   │   ├── SmsHistory.tsx     # 短信记录（支持展开全文/复制）
│   │   │   ├── SmsSend.tsx        # 手动发送（支持模板选择）
│   │   │   ├── SupportAdmin.tsx   # 管理员/客服咨询管理
│   │   │   ├── Templates.tsx      # 短信模板管理
│   │   │   └── Users.tsx          # 用户管理
│   │   └── store/
│   │       ├── authStore.ts       # 认证状态（token、用户、权限计算）
│   │       ├── langStore.ts       # 语言切换
│   │       ├── modemStore.ts      # 设备列表
│   │       └── themeStore.ts      # 主题切换
│   ├── Dockerfile
│   └── nginx.conf
├── scripts/
│   └── setup-debian.sh
└── docker-compose.yml
```

---

## 页面说明

| 路径 | 页面 | 权限 |
|------|------|------|
| `/login` | 登录 | 公开 |
| `/` | 设备总览 | 所有用户 |
| `/sim-cards` | SIM 卡管理 | `can_view_sim` |
| `/modems/:id` | 单卡详情 | `can_view_sim` |
| `/send` | 发送短信 | `can_send_sms` |
| `/history` | 短信记录 | `can_view_history` |
| `/tasks` | 定时任务 | `can_manage_tasks` |
| `/templates` | 短信模板 | `can_send_sms` |
| `/users` | 用户管理 | 管理员 |
| `/roles` | 角色管理 | 管理员 |
| `/support` | 用户咨询管理 | 管理员 / `can_support` |
| `/admin/tasks` | 任务监控 | 管理员 |

---

## API 接口

### 认证

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/auth/captcha` | 获取 SVG 验证码（token + svg） |
| POST | `/api/auth/login` | 登录（需传 captcha_token + captcha_code） |
| GET | `/api/auth/me` | 获取当前用户信息（含角色列表） |

### 用户管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/users/` | 获取用户列表（管理员） |
| POST | `/api/users/` | 创建用户（管理员） |
| PATCH | `/api/users/{id}` | 修改用户角色/状态（管理员） |
| DELETE | `/api/users/{id}` | 删除用户（管理员） |
| POST | `/api/users/{id}/reset-password` | 重置密码（管理员） |
| POST | `/api/users/me/change-password` | 修改自己的密码 |
| GET | `/api/users/{id}/permissions` | 查看独立权限（管理员） |
| PUT | `/api/users/{id}/permissions` | 更新独立权限（管理员） |

### 角色管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/roles/` | 获取角色列表（管理员） |
| POST | `/api/roles/` | 创建角色（管理员） |
| PATCH | `/api/roles/{id}` | 更新角色权限（管理员） |
| DELETE | `/api/roles/{id}` | 删除角色（管理员，系统角色不可删）|
| PUT | `/api/roles/users/{user_id}/roles` | 批量设置用户的角色列表（管理员） |

### 设备

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/modems/` | 获取所有设备列表 |
| GET | `/api/modems/{id}` | 获取单个设备详情 |
| PATCH | `/api/modems/{id}` | 更新设备别名 |
| POST | `/api/modems/{id}/refresh` | 立即刷新设备状态 |

### 短信与定时任务

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/sms/send` | 立即发送短信 |
| GET | `/api/sms/messages` | 查询收发记录 |
| GET | `/api/sms/tasks` | 获取当前用户的定时任务 |
| POST | `/api/sms/tasks` | 创建定时任务 |
| PATCH | `/api/sms/tasks/{id}` | 更新任务 |
| DELETE | `/api/sms/tasks/{id}` | 删除任务 |
| POST | `/api/sms/tasks/{id}/run-now` | 立即执行一次 |
| GET | `/api/sms/admin/tasks` | 获取所有用户的任务（管理员） |
| GET | `/api/sms/admin/tasks/stats` | 任务统计（管理员） |
| GET | `/api/sms/admin/tasks/{id}/history` | 任务执行历史（管理员） |
| GET | `/api/sms/templates` | 获取模板列表 |
| POST | `/api/sms/templates` | 创建模板 |
| DELETE | `/api/sms/templates/{id}` | 删除模板 |

### 通知

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/notifications` | 获取通知列表（按角色过滤） |
| GET | `/api/notifications/unread-count` | 未读数量 |
| POST | `/api/notifications/read-all` | 全部标记已读 |
| POST | `/api/notifications/{id}/read` | 单条标记已读 |

### 用户咨询

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/support/upload` | 上传附件（图片/文件） |
| GET | `/api/support/files/{filename}` | 获取上传文件 |
| POST | `/api/support/messages` | 发送消息 |
| GET | `/api/support/messages` | 获取消息列表 |
| POST | `/api/support/messages/read` | 标记已读 |
| GET | `/api/support/unread` | 未读消息数 |
| GET | `/api/support/conversations` | 会话列表（管理员/客服） |

### WebSocket

```
ws://host:8000/ws/modems?token=<JWT>
```

每 5 秒推送一次所有设备的实时状态。

---

## 权限系统说明

SimNexus 使用三层权限模型：

```
系统角色（admin/user）
    ↓
RBAC 角色列表（多个，取并集）
    ↓
旧版独立权限（兜底，向后兼容）
```

**管理员**（`role=admin`）始终拥有全部权限，不受 RBAC 角色限制。

**普通用户**的有效权限由分配的所有 RBAC 角色合并决定：
- 功能权限：任一角色开启即生效
- 只读模式：所有角色均为只读才生效
- 设备范围：任一角色无限制则无限制；否则取所有受限角色的设备 ID 并集

若用户未分配任何 RBAC 角色，则回落到旧版 `UserPermission` 独立权限设置。

---

## 通知受众说明

| audience | 可见对象 |
|----------|---------|
| `admin` | 仅系统管理员 |
| `support` | 管理员 + 拥有 `can_support` 权限的角色 |
| `all` | 所有已登录用户 |
| `user` + target_user_id | 仅指定用户 |

---

## 配置

后端配置通过环境变量或 `backend/.env` 设置：

```env
DATABASE_URL=sqlite:///./sim_manager.db
MODEM_POLL_INTERVAL=10        # 设备轮询间隔（秒）
SMS_SCHEDULER_INTERVAL=30     # 任务调度检查间隔（秒，当前未使用）
CORS_ORIGINS=["http://localhost:3000","http://localhost:5173"]
```

文件上传存储路径：`/opt/simnexus/uploads/`（UUID 命名，无需鉴权即可访问）

---

## 定时任务 Cron 示例

| Cron 表达式 | 说明 |
|-------------|------|
| `0 9 * * *` | 每天早上 9:00 |
| `0 9 * * 1` | 每周一早上 9:00 |
| `*/30 * * * *` | 每 30 分钟 |
| `0 8,12,18 * * *` | 每天 8:00、12:00、18:00 |

---

## 常见问题

**设备未被识别**

```bash
systemctl status ModemManager
mmcli -L
lsusb
```

**串口权限不足**

```bash
groups $USER
sudo usermod -aG dialout $USER  # 重新登录后生效
```

**后端无法启动（端口被占用）**

```bash
# 查找占用 8000 端口的进程
ss -tlnp | grep 8000
# 终止该进程
kill -9 <PID>
```

如果同时运行了 Docker Compose，Docker backend 容器会以 host 网络模式占用 8000 端口。若改用 systemd 管理，需先停止 Docker backend：

```bash
docker stop simnexus-backend-1
docker update --restart=no simnexus-backend-1
```

**短信发送失败**

```bash
mmcli -m 0 --messaging-create-sms="number=+8613800138000,text=test"
```

---

## 技术栈

| 层 | 技术 |
|----|------|
| 后端框架 | FastAPI + Uvicorn |
| 数据库 | SQLite + SQLAlchemy 2.0 |
| 认证 | JWT（python-jose）+ bcrypt |
| 任务调度 | APScheduler |
| 调制解调器控制 | ModemManager（`mmcli`）+ ZTE goform HTTP API |
| 前端框架 | React 18 + TypeScript |
| 构建工具 | Vite |
| 样式 | Tailwind CSS |
| 状态管理 | Zustand |
| 实时通信 | WebSocket |
| 容器化 | Docker + Docker Compose |

## 文档

| 文件 | 说明 |
|------|------|
| [docs/database-schema.md](docs/database-schema.md) | 数据库表结构与关系详细说明 |
| [docs/schema.sql](docs/schema.sql) | 建表 SQL（可直接在空库执行） |
| [docs/schema-er.svg](docs/schema-er.svg) | 数据表 ER 关系图 |
| [docs/network-model.svg](docs/network-model.svg) | Docker 网络拓扑图 |

## License

MIT

> 部署与使用问题见各章节；一键部署用根目录 `./deploy.sh`。
