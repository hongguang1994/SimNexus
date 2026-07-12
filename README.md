# SimNexus

基于 Debian 的多 USB 4G 模块管理系统：多张 SIM 卡统一管理、实时状态监控、流量统计、短信收发、自建 VoWiFi、通讯录、用户权限控制、在线客服与 Telegram 机器人。

> 详细的项目结构、页面路由、REST/WebSocket 接口、权限与通知模型见 **[docs/reference.md](docs/reference.md)**。

## 功能特性

### 设备与通信
- **SIM 卡管理** — 网络状态、信号强度、制式（5G/4G/3G）、注册状态、运营商、流量、在线时长、短信统计
- **多卡监控** — 同时管理多个 USB 4G 模块，自动识别热插拔；卡身份按 **ICCID** 识别（同卡换模组仍是同一张卡）
- **ZTE 随身 WiFi** — 通过 goform HTTP API 管理（无需 mmcli），自动发现、状态轮询、短信收发
- **VoWiFi / Wi-Fi Calling** — 自建 IKEv2/EAP-AKA + IMS 协议栈，让漫游中被拒的 SIM（如 giffgaff 漫游中国移动）**经 ePDG 走 Wi-Fi 收发短信**；按卡开关、独占串口，MT 自动回 RP-ACK、MO 批量摊薄鉴权、心跳看门狗自愈；ePDG 支持 DoH 动态解析（绕过 fake-ip）
- **飞行模式** — 与 VoWiFi 解耦的独立开关：关射频、不在蜂窝基站注册，VoWiFi 照常收发（走 IP 不受影响）
- **实时推送** — 设备状态 WebSocket 每 5 秒刷新；**消息中心 / 客服 / Telegram / 系统日志** 均为 WebSocket 即时推送

### 短信与通讯录
- **消息中心** — iMessage 风格收发界面，按（卡 + 对端号码）分会话、多卡筛选、乐观发送、WebSocket 实时到达
- **稍后发送** — Apple 风格的定时发送（会话内联「待发」气泡，可取消），单次定时复用任务引擎
- **通讯录** — 每个用户私有，Apple 风格拼音首字母索引；消息中心命中号码时显示联系人姓名，「发送信息」一键跳转预填
- **收件同步** — 自动拉取各设备收件箱并入库，按 `mm_sms_index` 去重

### 用户与权限（RBAC）
- **JWT 登录** — 用户名/密码 + 图形验证码（SVG，5 分钟有效）
- **角色管理** — 自定义角色，功能权限 / 只读 / 设备范围三维度；每个用户可分配多个角色，权限取并集
- **卡级访问控制** — 用户申请访问某张卡，审批员审批 / 直接授权；审批员对管理范围内的卡自动有权
- **系统预置角色** — 全功能用户、只读用户、短信操作员、任务管理员、客服

### 管理与运维
- **任务监控** — 查看定时任务的执行状态与历史
- **用户咨询** — 用户与客服/管理员实时聊天（WebSocket），支持文字、图片、文件
- **Telegram 机器人** — 收到短信自动推送；在 Telegram 里 `/modems`、`/send #<卡ID> <号码> <内容>`、`/list` 远程收发；**chat_id 白名单鉴权**
- **系统日志** — 管理员实时日志页：HTTP / VoWiFi / 轮询 / 定时 / Telegram / 系统 分类标签页 + 级别筛选 + WebSocket 推送
- **通知系统** — 铃铛未读数、受众过滤（admin/support/all/user）
- **响应式 + 主题** — 手机 / 平板 / 桌面；浅色 / 深色 / 跟随系统；中文 / 英文

---

## 系统要求

- Debian 11 / 12（或 Ubuntu 20.04+）
- Docker Engine + Docker Compose v2（**推荐部署方式**）
- 宿主机安装并运行 ModemManager 1.18+（供容器内 mmcli 使用）
- USB 4G 模块（EC25、SIM7600 等）或 ZTE 随身 WiFi（CDC Ethernet 模式）
- 后端为 **Go**、前端为 React + Vite；本地开发另需 Go 1.23+ 与 Node.js 18+

> 后端已由早期的 Python/FastAPI 迁移为 Go。生产走 Docker，宿主机无需装 Go/Python。

---

## 快速开始

### 一键部署（推荐）

```bash
git clone https://github.com/hongguang1994/SimNexus.git
cd SimNexus
./deploy.sh
```

`deploy.sh` 自动完成：环境检查 → 生成 `.env`（密钥不入库）→ 首次导入初始数据（账号 `admin` / `admin123`）→ 构建并启动前后端容器。完成后访问 `http://<服务器IP>:8899`。

> **密钥**：`.env` 由 `.env.example` 生成，`TELEGRAM_*` 按需填写（留空则禁用 Telegram）；`.env` 已被 `.gitignore` 忽略。

### 手动步骤（等价于一键脚本）

```bash
cp .env.example .env
sqlite3 data/sim_manager.db < docs/schema.sql   # 仅首次：导入初始数据
docker compose up -d --build
```

### 本地开发（非 Docker）

```bash
sudo bash scripts/setup-debian.sh               # 系统依赖：ModemManager / udev / dialout

cd backend-go
sqlite3 ./data/sim_manager.db < ../docs/schema.sql   # 仅首次
go run .                                              # 监听 :8000

cd ../frontend
npm install
npm run dev                                           # http://localhost:5173，已代理 /api、/ws → :8000
```

默认管理员：`admin` / `admin123`（登录后请立即改密码）。

---

## Docker 部署细节

**前提**：宿主机已装并运行 ModemManager，并已安装 Docker / Compose v2。

```bash
sudo apt install modemmanager
sudo systemctl enable --now ModemManager
docker compose up -d          # 首次自动构建镜像，约 2–3 分钟
```

**网络架构**：两个容器运行在 Docker bridge 网络。

```
浏览器 :8899 → simnexus-frontend（nginx）
  ├── /、/login…  → React SPA 静态文件
  ├── /api/*      → proxy → simnexus-backend:8000
  └── /ws/*       → proxy → simnexus-backend:8000（WebSocket）
```

backend 容器挂载宿主机 D-Bus socket（`/run/dbus/system_bus_socket`）与宿主机 ModemManager 通信，**不在容器内启动 ModemManager**。

**ZTE 随身 WiFi**：插入后宿主机出现 CDC Ethernet 网卡，需手动配 IP：

```bash
ip link set enx344b50000000 up
ip addr add 192.168.0.100/24 dev enx344b50000000
```

**数据持久化**：SQLite 库与上传文件存于宿主机 `./data/`，容器重建不丢。

**常用运维**

```bash
docker compose ps                    # 状态
docker compose logs -f backend       # 日志
docker compose restart               # 重启
git pull && docker compose build && docker compose up -d   # 更新
```

---

## 配置

后端读取环境变量 / `.env`：

| 变量 | 默认 | 说明 |
|------|------|------|
| `MODEM_POLL_INTERVAL` | `10` | 设备轮询间隔（秒） |
| `TELEGRAM_BOT_TOKEN` | 空 | Telegram Bot Token（留空禁用） |
| `TELEGRAM_CHAT_ID` | 空 | 推送目标 Chat ID |
| `TELEGRAM_ALLOWED_CHAT_IDS` | 空 | 允许发命令的额外 chat_id（逗号分隔） |
| `VOWIFI_VERBOSE` | `1` | VoWiFi 详细日志（排障用） |
| `VOWIFI_AKA_ORDER` | `at` | USIM AKA 顺序（`at`=AT 优先 QMI 兜底，`qmi`=反之） |
| `VOWIFI_EPDG_DNS` | `1` | ePDG 用 DoH 动态解析（失败回退配置 IP） |

数据库默认 `sqlite:///./data/sim_manager.db`；上传文件存 `/opt/simnexus/uploads/`（UUID 命名）。

---

## 常见问题

**设备未被识别**
```bash
systemctl status ModemManager && mmcli -L && lsusb
```

**串口权限不足**
```bash
sudo usermod -aG dialout $USER   # 重新登录后生效
```

**短信发送失败（蜂窝）**
```bash
mmcli -m 0 --messaging-create-sms="number=+8613800138000,text=test"
```

---

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go + gin + gorm（SQLite/CGO） |
| 认证 | JWT + bcrypt |
| 调制解调器 | ModemManager（`mmcli`）+ ZTE goform HTTP + 自建 VoWiFi（IKEv2/EAP-AKA/IMS） |
| 前端 | React 18 + TypeScript + Vite + Tailwind CSS + Zustand |
| 实时通信 | WebSocket（消息 / 客服 / Telegram / 日志 / 设备状态） |
| 容器化 | Docker + Docker Compose |

## 文档

| 文件 | 说明 |
|------|------|
| [docs/reference.md](docs/reference.md) | **项目结构、页面路由、REST/WebSocket 接口、权限与通知模型** |
| [docs/database-schema.md](docs/database-schema.md) | 数据库表结构与关系 |
| [docs/schema.sql](docs/schema.sql) | 建表 SQL（可直接在空库执行） |
| [docs/schema-er.svg](docs/schema-er.svg) | 数据表 ER 图 |
| [docs/network-model.svg](docs/network-model.svg) | Docker 网络拓扑图 |

## License

MIT
