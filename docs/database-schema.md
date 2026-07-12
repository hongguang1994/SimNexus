# 数据库表结构与关系文档

## 总览

SimNexus 使用 SQLite，分为若干功能域：

| 功能域 | 表 |
|--------|-----|
| 用户与权限 | `users`、`roles`、`user_roles` |
| 设备管理 | `modems`、`sim_access_requests`、`sim_grants` |
| 短信与任务 | `sms_messages`、`sms_scheduled_tasks`、`sms_templates` |
| 通讯录 | `contacts` |
| 通知与客服 | `notifications`、`support_messages` |
| Telegram 集成 | `telegram_messages` |

> 建表 SQL 以 [`schema.sql`](schema.sql) 为准（后端启动时 gorm AutoMigrate + ensureColumns 亦会补齐新表/新列）。`modems` 表含 VoWiFi 相关列：`vowifi_mode`、`vowifi_epdg_ip`、`vowifi_at_port`、`vowifi_airplane`（飞行模式，与 VoWiFi 解耦）。`contacts` 为每个用户私有通讯录（按 `owner_id` 隔离）。

---

## 一、用户与权限域

### `users` — 用户

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| username | VARCHAR(50) UNIQUE | 登录名 |
| password_hash | VARCHAR(200) | bcrypt 哈希（不使用 passlib） |
| role | ENUM | 系统级角色：`admin` \| `user` |
| is_active | BOOLEAN | 账号是否启用 |
| created_at / updated_at | DATETIME | 时间戳 |

> `role=admin` 的用户拥有全部权限，不受 RBAC 角色限制。

---

### `roles` — RBAC 角色

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| name | VARCHAR(64) UNIQUE | 角色名 |
| description | TEXT | 描述 |
| is_system | BOOLEAN | 系统预置角色，不可删除 |
| can_view_sim | BOOLEAN | 可查看 SIM 卡 |
| can_send_sms | BOOLEAN | 可发送短信 |
| can_manage_tasks | BOOLEAN | 可管理定时任务 |
| can_view_history | BOOLEAN | 可查看短信记录 |
| read_only | BOOLEAN | 只读模式（禁止写操作） |
| can_support | BOOLEAN | 可回复用户咨询 |
| allowed_modem_ids | JSON | 可访问的设备 ID 列表，`null` 表示不限制 |
| created_at / updated_at | DATETIME | 时间戳 |

系统预置 5 个角色：全功能用户、只读用户、短信操作员、任务管理员、客服。

---

### `user_roles` — 用户角色关联（多对多）

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | INTEGER FK → users.id | 联合主键，CASCADE 删除 |
| role_id | INTEGER FK → roles.id | 联合主键，CASCADE 删除 |

一个用户可分配多个角色，权限取所有角色的并集：
- 功能权限：任一角色开启即生效（`any()`）
- 只读模式：所有角色均为只读才生效（`all()`）
- 设备范围：任一角色无限制则全部可访问；否则取各角色 ID 的并集

---

### `user_permissions` — 旧版独立权限（兜底）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| user_id | INTEGER FK → users.id UNIQUE | 一对一 |
| can_view_sim | BOOLEAN | 同 roles 字段含义 |
| can_send_sms | BOOLEAN | |
| can_manage_tasks | BOOLEAN | |
| can_view_history | BOOLEAN | |
| read_only | BOOLEAN | |
| allowed_modem_ids | JSON | |

**使用时机**：仅当用户未分配任何 RBAC 角色时，回落到此表的设置。新用户推荐使用 RBAC 角色管理权限。

---

## 二、设备管理域

### `modems` — 调制解调器

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| mm_object_path | VARCHAR(200) UNIQUE | ModemManager D-Bus 路径，**唯一业务键** |
| device_path | VARCHAR(100) | 如 `/dev/ttyUSB0` |
| imei | VARCHAR(20) UNIQUE | 设备 IMEI |
| manufacturer / model | VARCHAR | 厂商/型号 |
| phone_number | VARCHAR(30) | SIM 卡号码 |
| operator | VARCHAR(100) | 运营商 |
| signal_quality | INTEGER | 信号强度 0-100 |
| status | ENUM | `connected` \| `disconnected` \| `error` \| `unknown` |
| alias | VARCHAR(100) | 用户自定义别名 |
| is_active | BOOLEAN | 是否当前在线 |
| last_seen | DATETIME | 最后一次轮询发现时间 |
| access_technologies | VARCHAR(100) | 网络制式，如 `lte`、`umts` |
| registration_state | VARCHAR(50) | 注册状态，如 `home`、`roaming` |
| tx_bytes / rx_bytes | INTEGER | 发送/接收流量（字节） |
| connection_duration | INTEGER | 连接时长（秒） |
| created_at | DATETIME | 首次发现时间 |

> `mm_object_path` 是唯一业务标识，不要用 `imei` 或 `device_path` 做唯一键（两者可能缺失或变化）。

---

## 三、短信与任务域

### `sms_messages` — 短信记录

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| modem_id | INTEGER FK → modems.id | 所属设备 |
| created_by_id | INTEGER FK → users.id | 发送者（可为空，收件无发送者） |
| scheduled_task_id | INTEGER FK → sms_scheduled_tasks.id | 来源定时任务（可为空） |
| direction | ENUM | `inbound`（收件）\| `outbound`（发件） |
| phone_number | VARCHAR(30) | 对端号码 |
| content | TEXT | 短信内容 |
| status | ENUM | `pending` \| `sent` \| `failed` \| `received` |
| error_message | TEXT | 失败原因 |
| mm_sms_index | VARCHAR(20) | mmcli SMS 对象索引，收件去重用 |
| sent_at / received_at | DATETIME | 发送/接收时间 |
| created_at | DATETIME | 入库时间 |

**收件去重**：以 `(modem_id, mm_sms_index, direction=inbound)` 联合去重，避免轮询重复入库。

---

### `sms_scheduled_tasks` — 定时任务

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| modem_id | INTEGER FK → modems.id | 使用的设备 |
| created_by_id | INTEGER FK → users.id | 创建者 |
| name | VARCHAR(100) | 任务名称 |
| recipients | JSON | 收件号码列表，如 `["+8613800138000"]` |
| content | TEXT | 短信内容 |
| cron_expression | VARCHAR(100) | Cron 表达式（循环任务），如 `0 9 * * *` |
| send_once_at | DATETIME | 单次发送时间（与 cron 二选一） |
| status | ENUM | `active` \| `paused` \| `completed` \| `failed` |
| run_count | INTEGER | 累计执行次数 |
| last_run_at / next_run_at | DATETIME | 上次/下次执行时间 |
| created_at / updated_at | DATETIME | 时间戳 |

---

### `sms_templates` — 短信模板（独立）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| name | VARCHAR(100) | 模板名称 |
| content | TEXT | 模板内容 |
| variables | JSON | 变量名列表，如 `["name", "code"]` |
| created_at / updated_at | DATETIME | 时间戳 |

> 此表无外键，独立存在，不与任务或消息关联。

---

## 四、通知与客服域

### `notifications` — 系统通知

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| type | VARCHAR(32) | 通知类型，如 `modem_online`、`sms_failed` |
| title | VARCHAR(128) | 标题 |
| body | TEXT | 正文 |
| is_read | BOOLEAN | 已读状态 |
| audience | VARCHAR(16) | 可见范围（见下表） |
| target_user_id | INTEGER | 软引用 users.id，`audience=user` 时使用 |
| created_at | DATETIME | 创建时间 |

**audience 路由规则：**

| audience 值 | 可见用户 |
|-------------|---------|
| `admin` | 仅 `role=admin` 的管理员 |
| `support` | 管理员 + 拥有 `can_support=true` 角色的用户 |
| `all` | 所有已登录用户 |
| `user` | 仅 `id = target_user_id` 的用户 |

> `target_user_id` 是软引用，无数据库外键约束，用户被删除后通知仍保留。

---

### `support_messages` — 用户咨询消息

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| user_id | INTEGER FK → users.id | 咨询会话所属用户（永远是普通用户） |
| sender_id | INTEGER FK → users.id | 实际发送者（用户本人或客服/管理员） |
| content | TEXT | 消息内容 |
| is_from_user | BOOLEAN | `true` = 用户发，`false` = 客服发 |
| is_read | BOOLEAN | 已读状态 |
| attachment_url | TEXT | 附件路径（UUID 命名文件） |
| attachment_name | TEXT | 原始文件名 |
| attachment_type | TEXT | `image` \| `file` |
| created_at | DATETIME | 发送时间 |

> `user_id` 标识"这条消息属于哪个会话（用户）"，`sender_id` 标识"谁发的"。客服回复时 `user_id` 是用户 ID，`sender_id` 是客服 ID。

---

## 五、Telegram 集成域

### `telegram_messages` — Telegram 消息记录

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 主键 |
| chat_id | VARCHAR(64) | Telegram chat/group ID |
| username | VARCHAR(128) | 发送者用户名（收件时填写，发件为 `SimNexus`） |
| direction | VARCHAR(8) | `in` = bot 收到 \| `out` = bot 发出 |
| text | TEXT | 消息文本；媒体消息存说明文字或文件名 |
| is_command | BOOLEAN | 是否为 bot 命令（`/send`、`/list`、`/modems`、`/help`） |
| file_id | VARCHAR(256) | Telegram 文件 ID，通过 `/api/telegram/file/<id>` 代理 |
| file_type | VARCHAR(32) | `photo` \| `document` \| `video` \| `sticker` \| `voice` |
| created_at | DATETIME | 入库时间 |

> 此表无外键，独立存在。文件通过 Telegram Bot API 代理访问，前端携带 JWT token（`?token=`）请求 `/api/telegram/file/<file_id>`。

**Bot 命令说明：**

| 命令 | 功能 |
|------|------|
| `/modems` | 列出所有在线设备 |
| `/list [#id]` | 查看最近 10 条收到的短信，可按设备过滤 |
| `/send <号码> <内容>` | 通过单卡或自动选择设备发送短信 |
| `/send #<设备ID> <号码> <内容>` | 指定设备发送短信 |
| `/help` | 显示帮助信息 |

---

## 外键关系汇总

```
users ──────────────────────────────────────────────┐
  │ (M:N via user_roles)                             │
  ├──► roles                                         │
  │      └──► role_modem_scope (M:N via modems)      │
  │                                                  │
  │ created_by_id (可选)                             │
  ├──► sms_messages                                  │
  │                                                  │
  │ created_by_id (可选)                             │
  ├──► sms_scheduled_tasks                           │
  │                                                  │
  │ user_id / sender_id                              │
  ├──► support_messages                              │
  │                                                  │
  │ user_id                                          │
  └──► sim_access_requests                           │
                                                     │
modems                                              │
  ├──► sms_messages (modem_id)                      │
  ├──► sms_scheduled_tasks (modem_id)               │
  ├──► sim_access_requests (modem_id)               │
  └──► role_modem_scope (modem_id)                  │
                                                     │
sms_scheduled_tasks                                 │
  └──► sms_messages (scheduled_task_id, 可选)        │
                                                     │
notifications                                       │
  └── target_user_id → users.id (软引用，无 FK)     ┘

sms_templates    （独立，无外键）
telegram_messages（独立，无外键）
```

---

## 权限解析优先级

```
1. user.role == admin   → 全部权限（跳过后续步骤）
2. user.rbac_roles 不为空 → 合并所有角色权限
     正向权限（can_*）：any()  — 任一角色开启即生效
     read_only：all()          — 所有角色均只读才生效
     设备范围：union(role_modem_scope) — 取并集；任一角色无 scope 则无限制
3. user.rbac_roles 为空  → 无权限
```

## SIM 卡访问控制流程

```
用户申请 (sim_access_requests, status=pending)
    ↓ 审批员审批
approved → granted_level: view | use
    ↓
get_user_modem_grants() 查询有效授权
    ↑
审批员自动拥有其 role_modem_scope 内所有卡的使用权（无需申请）
```
