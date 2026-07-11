# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SimNexus is a multi-USB 4G modem management system. It manages multiple SIM cards, handles real-time modem status monitoring via WebSocket, and provides SMS sending (manual and scheduled), SMS templates with variable substitution, RBAC-based user/role management, card-level access control (apply → approve → use), a notification system, a support chat feature, and Telegram bot integration for SMS push notifications and remote commands. The system runs on Debian/Ubuntu and communicates with modems through the Linux `ModemManager` daemon via the `mmcli` CLI tool. It also supports ZTE portable WiFi devices via their HTTP goform API (`services/zte_http_modem.py`).

## Commands

### Backend

```bash
# Initial setup (Debian/Ubuntu only — installs ModemManager, udev rules, Python venv, npm deps)
sudo bash scripts/setup-debian.sh

# Run backend (from repo root)
cd backend && .venv/bin/uvicorn app.main:app --host 0.0.0.0 --port 8000

# Install/update Python dependencies
backend/.venv/bin/pip install -r backend/requirements.txt

# Run one-time schema migration (existing SQLite DB only)
cd backend && python migrate.py
```

### Frontend

```bash
cd frontend
npm install        # install dependencies
npm run dev        # dev server at http://localhost:5173
npm run build      # tsc + vite build (type-check + bundle)
npm run preview    # preview production build
```

### Docker (Linux host only)

```bash
docker compose up -d   # frontend at http://localhost:80
```

Both containers run on bridge networking. The backend container accesses the host's ModemManager by mounting `/run/dbus/system_bus_socket` — the host must have ModemManager running. USB devices are bind-mounted at `/dev/bus/usb`.

There are no automated tests in this codebase.

## Architecture

### Backend (FastAPI + SQLAlchemy)

The backend is a single FastAPI app with four concurrently running components:

1. **HTTP API** (`app/api/`) — REST endpoints for all features. All routes are prefixed with `/api`. Modules:
   - `auth.py` — login, `/auth/me` (returns user + RBAC roles)
   - `captcha.py` — SVG captcha generation and JWT-signed answer verification
   - `modems.py` — device CRUD; `GET /modems/available` returns all modems for browsing (resource library)
   - `sms.py` — send, history, scheduled tasks (user-scoped); `/admin/tasks` returns all tasks for admin, own tasks for regular users
   - `users.py` — user CRUD, password reset
   - `roles.py` — RBAC role CRUD and user-role assignment (`PUT /roles/users/{user_id}/roles`)
   - `notifications.py` — audience-filtered notification list, unread count, mark-read
   - `support.py` — support chat messages, file upload, conversation list (staff only)
   - `sim_requests.py` — SIM card access request workflow (apply / approve / reject / direct grant)
   - `ws.py` — WebSocket push of modem state

2. **Modem Poller** (`app/services/modem_poller.py`) — An `asyncio` background task (started in `lifespan`) that calls `modem_manager.list_modems()` every `MODEM_POLL_INTERVAL` seconds, then also calls `zte_http_modem.get_modem_info()` to poll any ZTE device at `192.168.0.1`. It upserts modem state into the DB keyed on `mm_object_path` and ingests any new inbound SMS, deduplicating by `mm_sms_index`. ZTE modems use synthetic path `zte:192.168.0.1`.

3. **SMS Scheduler** (`app/services/sms_scheduler.py`) — An `APScheduler AsyncIOScheduler` that runs scheduled SMS tasks. It reloads active tasks from the DB every 60 seconds and registers them as APScheduler jobs with either a `CronTrigger` or `DateTrigger`. One-time tasks transition to `COMPLETED` (partial failure) or `FAILED` (all recipients failed) after firing. `reload_tasks` skips single-shot tasks whose `send_once_at` has already passed to prevent double-execution.

4. **Notification service** (`app/services/notify.py`) — helper functions that push `Notification` rows to DB. Called by the poller, scheduler, and support router. Each notification has an `audience` field; `_visible_filter()` in `notifications.py` applies per-user filtering at query time.

**ModemManager integration** (`app/services/modem_manager.py`) — All standard modem communication is done by shelling out to `mmcli` (the ModemManager CLI). JSON output (`-J` flag) is parsed directly. SMS send is a two-step mmcli call: create SMS object → send it → delete it. The modem is identified by its numeric index extracted from the D-Bus path (e.g. `/org/freedesktop/ModemManager1/Modem/0` → index `0`).

**ZTE HTTP driver** (`app/services/zte_http_modem.py`) — Manages ZTE portable WiFi devices via their `goform` HTTP API at `http://192.168.0.1`. Device discovery uses three strategies: USB VID matching (`19d2`), existing `192.168.0.x` IP on a non-loopback interface, or ZTE MAC OUI (`34:4b:50`). When running inside Docker (where the host's network interface is not visible), the driver connects directly to `192.168.0.1` via the container's routing table. SMS routing in `sms.py` and `sms_scheduler.py` checks if `mm_object_path.startswith("zte:")` and calls this driver instead of mmcli. IMSI-to-operator inference is used when the device has no network service (`_MCC_MNC_MAP`). `send_once_at` is stored in UTC — the frontend converts local time via `.toISOString()` before submitting.

**Database** — SQLite by default (`sim_manager.db` in the working directory). Tables are created on startup via `Base.metadata.create_all`. There is no Alembic migration workflow in active use; `backend/migrate.py` is a one-off script for a specific past schema change.

**WebSocket** (`app/api/ws.py`) — `/ws/modems?token=<JWT>` pushes all modem state from DB every 5 seconds to all connected clients. State comes from DB (written by the poller), not live from mmcli.

**Startup initialization** (`app/main.py` `lifespan`) — Only runs `Base.metadata.create_all`. Initial data (admin user + system roles) is no longer seeded from code; it lives in `docs/schema.sql`. To initialize a fresh database: `sqlite3 sim_manager.db < docs/schema.sql`.

### Security layer (`app/core/security.py`)

- **JWT auth** — `python-jose` with HS256; `bcrypt` directly (NOT `passlib` — passlib fails on Python 3.13)
- **`get_current_user`** — dependency that decodes JWT and loads user from DB with `rbac_roles` joined
- **`require_admin`** — dependency that raises 403 unless `user.role == UserRole.ADMIN`
- **`require_approve_requests`** — dependency that raises 403 unless admin or role has `can_approve_requests=True`
- **`require_support_staff`** — dependency that raises 403 unless `is_support_staff(user)` is true
- **`_perm(user)`** — resolves effective permissions:
  1. If `role == ADMIN` → full access
  2. If user has RBAC roles → merge by union (positive flags: `any()`; `read_only`: `all()`; device scope: union of IDs, or unrestricted if any role has `allowed_modem_ids=None`)
  3. Else → no permissions
- **`get_user_modem_grants(user_id, db, level, user)`** — returns modem IDs the user has access to. If `user` has `can_approve_requests`, their managed cards (`allowed_modem_ids`) are automatically included at use-level without requiring explicit grants.
- **`is_support_staff(user)`** — returns True if admin OR any RBAC role has `can_support=True`

### Card-level access control (`app/api/sim_requests.py`)

Users apply for access to individual SIM cards; approvers review and grant/reject. Key concepts:

- **`SimAccessRequest`** table — tracks each user's request per modem with `status` (pending/approved/rejected), `requested_level`, `granted_level` (`view` | `use`), optional `expires_at`
- **Approver scope** — `_approver_modem_scope(approver)` returns the modem IDs an approver can manage (from `allowed_modem_ids`). `None` = unrestricted. Approvers can only approve/reject requests for modems within their scope.
- **Approver auto-access** — Approvers automatically have use-level access to all cards in their managed scope without needing to submit requests. This is enforced in `get_user_modem_grants()`.
- **Direct grant** — `POST /sim-requests/grant` allows approvers to grant access directly without requiring a prior application.
- **Endpoints**: `POST /` (apply), `GET /my` (own requests), `GET /` (approver list, scoped), `PUT /{id}/approve`, `PUT /{id}/reject`, `POST /batch-approve`, `POST /grant`

### Data models

**`models/user.py`**
```python
user_roles = Table("user_roles", Base.metadata,
    Column("user_id", ForeignKey("users.id", ondelete="CASCADE"), primary_key=True),
    Column("role_id", ForeignKey("roles.id", ondelete="CASCADE"), primary_key=True),
)

class User(Base):
    role = Column(Enum(UserRole))   # system-level: admin | user
    rbac_roles = relationship("Role", secondary=user_roles, lazy="joined")
```

**`models/role.py`**
```python
class Role(Base):
    __tablename__ = "roles"
    name, description, is_system
    can_view_sim, can_approve_requests, can_manage_tasks, can_view_history  # boolean flags
    read_only, can_support                                                   # boolean flags
    allowed_modem_ids = Column(JSON, nullable=True)  # None = unrestricted scope
```

**`models/sim_request.py`**
```python
class SimAccessRequest(Base):
    user_id, modem_id
    status = Column(Enum(RequestStatus))          # pending | approved | rejected
    requested_level = Column(Enum(PermissionLevel))  # view | use
    granted_level = Column(Enum(PermissionLevel), nullable=True)
    reason, admin_note
    expires_at = Column(DateTime, nullable=True)  # None = permanent
```

**`models/notification.py`** — `audience` values: `"admin"`, `"support"`, `"all"`, `"user"` (paired with `target_user_id`)

**`models/support.py`** — `SupportMessage`: `user_id`, `sender_role` (`"user"` | `"staff"`), `content`, `file_url`, `file_type`, `is_read`

### CAPTCHA (`app/api/captcha.py`)

SVG captcha with no server-side session. The answer is JWT-signed (5 min TTL) and returned as `token` to the client. Login sends both `captcha_token` and `captcha_code`; the backend verifies the JWT and compares the answer. Characters exclude 0/O/1/I/l to avoid ambiguity.

### Notification audience routing

| `audience` value | Visible to |
|---|---|
| `"admin"` | Only `role=admin` users |
| `"support"` | Admins + users with any role having `can_support=True` |
| `"all"` | All authenticated users |
| `"user"` | Only the user with `id == target_user_id` |

Support chat messages push `audience="support"` to notify staff, and `audience="user"` with `target_user_id` to notify the originating user of replies.

### Frontend (React 18 + TypeScript + Vite + Tailwind)

**Stores (Zustand with `persist` middleware)**:
- `authStore.ts` — `token`, `user` (includes `rbac_roles[]`), computed `perm()`, `canSupport()`, `canApprove()`
- `modemStore.ts` — modem list, updated by WebSocket hook
- `langStore.ts` — current language (`zh` | `en`), persisted to localStorage
- `themeStore.ts` — current theme (`light` | `dark` | `system`), persisted to localStorage

**`authStore.perm()` logic** (mirrors backend `_perm()`):
- Admin → full access
- Has RBAC roles → merge: `some()` for positive flags, `every()` for `read_only`, union of `allowed_modem_ids`
- No roles → no permissions

**`authStore.canSupport()`**: admin always true; else `rbac_roles.some(r => r.can_support)`.

**`authStore.canApprove()`**: admin always true; else `rbac_roles.some(r => r.can_approve_requests)`.

**i18n** — `src/i18n/zh.ts` and `en.ts` export flat key→string maps. `useT()` hook reads `langStore` and returns the lookup function. System role names (stored in Chinese in DB) are translated client-side via a hardcoded map in `Roles.tsx`.

**Layout.tsx** — calls `getMeApi()` on mount to refresh user data (including latest RBAC roles) into `authStore`. Nav links visibility:
- 资源库/SIM卡管理 → `can_view_sim`
- 发送短信/短信模板/定时任务 → `!read_only`
- 短信记录 → all authenticated users
- 我的任务记录/任务监控 → `!read_only` (admin sees all users' tasks; others see own tasks only)
- SIM申请审批 → `canApprove()`
- 用户管理/角色管理 → admin only
- 用户咨询 → `canSupport()`

**Header role display** — shows RBAC role names (e.g. "审批员 · 客服") when user has roles; falls back to "管理员" or "普通用户".

**Route guards**:
- `RequireAuth` — redirects to `/login` if no token
- `RequireAdmin` — redirects to `/` if not admin
- `RequireSupport` — redirects to `/` if `!canSupport()`
- `RequireApprove` — redirects to `/` if `!canApprove()`

**Pages**:
| Page | Path | Guard |
|------|------|-------|
| Login | `/login` | public |
| Dashboard | `/` | RequireAuth |
| ResourceLibrary | `/resources` | RequireAuth + `can_view_sim` |
| SimCards | `/sim-cards` | RequireAuth + `can_view_sim` |
| SimDetail | `/modems/:id` | RequireAuth + `can_view_sim` |
| SmsSend | `/send` | RequireAuth + `!read_only` |
| Templates | `/templates` | RequireAuth + `!read_only` |
| SmsHistory | `/history` | RequireAuth |
| ScheduledTasks | `/tasks` | RequireAuth + `!read_only` |
| AdminTasks | `/admin/tasks` | RequireAuth + `!read_only` |
| Users | `/users` | RequireAdmin |
| Roles | `/roles` | RequireAdmin |
| SupportAdmin | `/support` | RequireSupport |
| SimRequests | `/admin/sim-requests` | RequireApprove |

**ResourceLibrary (`src/pages/ResourceLibrary.tsx`)** — shows all SIM cards; effective access status is computed by `getEffectiveStatus()`:
```typescript
// Single source of truth for card access status per user
function getEffectiveStatus(modemId, isAdmin, approverScope, requests): AccessStatus {
  if (isAdmin) return 'use'
  if (approverScope === 'all' || approverScope?.has(modemId)) return 'use'
  return getRequestStatus(modemId, requests)  // from SimAccessRequest records
}
// approverScope: null = not an approver, 'all' = unrestricted, Set<number> = managed IDs
```

**SmsSend / ScheduledTasks modem filtering** — admins and unrestricted approvers see all modems; restricted approvers see their managed cards + explicitly granted cards; regular users see only use-level granted cards.

**API clients** (`src/api/`): `client.ts` is the Axios base with JWT `Authorization` header injected automatically; each domain module exports typed API functions.

Vite proxies `/api` and `/ws` to `localhost:8000` in dev; nginx handles routing in the Docker image.

### Configuration

Backend reads from environment variables or `backend/.env`:

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `sqlite:///./sim_manager.db` | SQLAlchemy DB URL |
| `MODEM_POLL_INTERVAL` | `10` | Seconds between modem polls |
| `SMS_SCHEDULER_INTERVAL` | `30` | (currently unused in scheduler loop) |
| `CORS_ORIGINS` | `["http://localhost:3000","http://localhost:5173"]` | Allowed origins |
| `TELEGRAM_BOT_TOKEN` | `""` | Telegram Bot Token（从 @BotFather 获取） |
| `TELEGRAM_CHAT_ID` | `""` | 推送目标 Chat ID（个人或群组） |

File uploads stored at `/opt/simnexus/uploads/` (UUID-named, served without auth via `GET /api/support/files/{filename}`).

### Telegram Bot Integration (`app/services/telegram_bot.py`)

Bot 通过长轮询（`getUpdates`）接收消息，通过 `sendMessage` / `sendPhoto` / `sendDocument` 发送消息。

**启动流程**：`main.py` lifespan 中调用 `telegram_bot.start_polling()`，启动 `asyncio.Task` 运行 `_poll_loop()`。

**收件推送**：`modem_poller.py` 的 `_ingest_inbox` / `_ingest_zte_inbox` 检测到新收件时，调用 `asyncio.create_task(telegram_bot.push_inbound_sms(label, sender, content))`。

**Bot 命令**（`_handle_command()`）：

| 命令 | 说明 |
|------|------|
| `/modems` | 列出所有在线设备（ID、别名、号码、运营商） |
| `/list [#id]` | 最近 10 条收件，可按设备 ID 过滤 |
| `/send <号码> <内容>` | 自动选择单一在线设备发送；多卡时提示指定 ID |
| `/send #<id> <号码> <内容>` | 指定设备发送 |
| `/help` | 显示帮助 |

**REST API** (`app/api/telegram.py`)：

| 端点 | 说明 |
|------|------|
| `GET /telegram/messages` | 消息列表（管理员） |
| `POST /telegram/send` | 发文字消息 |
| `POST /telegram/send-file` | 发图片/文件（multipart），自动选 sendPhoto/sendDocument |
| `DELETE /telegram/messages` | 清空记录 |
| `GET /telegram/config` | Bot 状态（token 是否配置、轮询是否运行） |
| `GET /telegram/file/{file_id}` | 代理 Telegram 文件下载，JWT 通过 `?token=` 传入（同 WebSocket 模式） |

**前端页面**（`/admin/telegram`，仅管理员）：聊天界面，支持收发文字、图片、文件，内联预览，点击大图，5 秒自动刷新。

### Key constraints

- The backend **must run on Linux** with ModemManager installed and the user in the `dialout` group. `mmcli` calls will fail on macOS or in Docker without host network + USB passthrough.
- **Modem identity = the SIM's `iccid`** (the printed SIM serial, follows the SIM across modules). The Go poller (`backend-go/services/modem_poller.go`) matches detected modems by `iccid` first (preferring `vowifi_mode` then most-recent when an iccid has duplicate rows), falling back to `mm_object_path` only when iccid is unreadable (sim-missing / ZTE has no iccid). Consequences: same SIM moved to a different EC25 module = same card (history/permissions/VoWiFi follow the SIM); a different SIM in the same module = a different card. Do **not** use `imei` (identifies the module, not the SIM) or `device_path` as the identity key. `mm_object_path` is still stored and used for the mmcli index regex and `zte:` routing, but it is no longer the identity key.
- Inbound SMS deduplication uses `(modem_id, mm_sms_index, direction=inbound)` — `mm_sms_index` is the mmcli SMS object index, not a global ID.
- Do **not** use `passlib` for password hashing — it fails on Python 3.13. Use `bcrypt` directly.
- `rbac_roles` is loaded with `lazy="joined"` on `User`; always available after `get_current_user`. Access via `getattr(user, "rbac_roles", None)` in security helpers that may receive users loaded without the relationship.
- System roles (`is_system=True`) cannot be deleted via the API.
- ZTE device `mm_object_path` is `zte:192.168.0.1` (synthetic, not a D-Bus path). All code that extracts the mmcli index via regex `/Modem/(\d+)$` must first check `if obj_path.startswith("zte:")` and route to the ZTE driver.
- `send_once_at` is stored in UTC. The frontend must convert local datetime-local input to ISO UTC string via `new Date(val).toISOString()` before submitting. APScheduler's `DateTrigger` interprets naive datetimes as UTC.
- SMS templates store variable names as a JSON list in `sms_templates.variables`. The frontend auto-detects `{var}` placeholders from content using a regex and presents a fill-in dialog before sending.
- `navigator.clipboard` requires HTTPS; the frontend uses `document.execCommand('copy')` as fallback for HTTP deployments.
- Approvers automatically have use-level access to their managed cards (`allowed_modem_ids`) without needing to submit requests. This is enforced in both backend (`get_user_modem_grants`) and frontend (modem list filtering). `allowed_modem_ids=null` on an approver role means unrestricted access to all cards.
- Modal components must use `createPortal(…, document.body)` to avoid CSS stacking context issues caused by ancestor elements with CSS animations (`transform`/`opacity`). Do not render modals as children of animated wrappers.
