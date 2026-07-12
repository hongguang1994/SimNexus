-- SimNexus 数据库初始化脚本
-- 用法：sqlite3 sim_manager.db < docs/schema.sql
-- 初始账号：admin / admin123
--
-- 表依赖顺序：
--   users → roles → role_modem_scope / user_roles
--   modems → sim_access_requests → sim_grants
--   modems → sms_scheduled_tasks → sms_messages
--   sms_templates / notifications / support_messages（无外部依赖）

-- ─────────────────────────────────────────────
-- 用户表
-- role: 系统级角色，枚举值 ADMIN | USER
--       细粒度权限由 RBAC roles 表控制，此字段仅区分超级管理员
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER      NOT NULL,
    username      VARCHAR(50)  NOT NULL,
    password_hash VARCHAR(200) NOT NULL,  -- bcrypt hash，不使用 passlib（Python 3.13 不兼容）
    role          VARCHAR(5)   NOT NULL,  -- 'ADMIN' | 'USER'
    is_active     BOOLEAN,
    created_at    DATETIME,
    updated_at    DATETIME,
    PRIMARY KEY (id),
    UNIQUE (username)
);
CREATE INDEX IF NOT EXISTS ix_users_id       ON users (id);
CREATE INDEX IF NOT EXISTS ix_users_username ON users (username);

-- ─────────────────────────────────────────────
-- RBAC 角色表
-- 权限标志均为布尔值，多角色时取并集（read_only 取交集）
-- SIM 卡访问范围由 role_modem_scope 关联表控制，
--   modem_scope 为空 = 不限制（对 can_approve_requests 角色意味着全局权限）
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS roles (
    id                    INTEGER     NOT NULL,
    name                  VARCHAR(64) NOT NULL,
    description           TEXT,
    is_system             BOOLEAN,                -- 系统预置角色，不允许删除
    can_view_sim          BOOLEAN,                -- 可访问资源库 / SIM 卡管理页
    can_approve_requests  BOOLEAN,                -- 可审批 SIM 申请（审批员）
    can_view_history      BOOLEAN,                -- 可查看短信记录
    read_only             BOOLEAN,                -- 只读模式，禁止发送短信 / 创建任务
    can_support           BOOLEAN,                -- 可处理客服会话
    created_at            DATETIME,
    updated_at            DATETIME,
    PRIMARY KEY (id),
    UNIQUE (name)
);
CREATE INDEX IF NOT EXISTS ix_roles_id ON roles (id);

-- ─────────────────────────────────────────────
-- 角色 SIM 卡管理范围（多对多）
-- 替代原 roles.allowed_modem_ids JSON 字段，提供外键级联完整性
-- 审批员角色：scope 内的卡自动拥有使用权，无需提交申请
-- 普通角色：scope 内的卡自动授予使用权（无需审批流程）
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS role_modem_scope (
    role_id  INTEGER NOT NULL,
    modem_id INTEGER NOT NULL,
    PRIMARY KEY (role_id, modem_id),
    FOREIGN KEY (role_id)  REFERENCES roles (id)  ON DELETE CASCADE,
    FOREIGN KEY (modem_id) REFERENCES modems (id) ON DELETE CASCADE
);

-- ─────────────────────────────────────────────
-- 用户 ↔ 角色 多对多关联
-- 一个用户可拥有多个角色，权限取并集
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_roles (
    user_id INTEGER NOT NULL,
    role_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, role_id),
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE
);

-- ─────────────────────────────────────────────
-- 调制解调器（SIM 卡）表
-- mm_object_path：ModemManager D-Bus 路径，是设备的唯一标识
--   标准 mmcli 设备格式：/org/freedesktop/ModemManager1/Modem/<n>
--   ZTE 便携 WiFi 使用合成路径：zte:192.168.0.1
-- 不应使用 device_path 或 imei 作为唯一键（可能为空或变更）
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS modems (
    id                  INTEGER      NOT NULL,
    mm_object_path      VARCHAR(200) NOT NULL,  -- D-Bus 路径或 ZTE 合成路径，唯一键
    device_path         VARCHAR(100),            -- /dev/ttyUSB* 等，可为空
    imei                VARCHAR(20),
    manufacturer        VARCHAR(100),
    model               VARCHAR(100),
    phone_number        VARCHAR(30),
    operator            VARCHAR(100),
    signal_quality      INTEGER,                 -- 0–100，由 mmcli / ZTE API 返回
    status              VARCHAR(12),             -- 'connected' | 'disconnected' | 'error' | 'unknown'
    alias               VARCHAR(100),            -- 管理员自定义名称
    is_active           BOOLEAN,
    last_seen           DATETIME,                -- 轮询器最后一次成功获取到此设备的时间
    created_at          DATETIME,
    access_technologies TEXT,                    -- 接入技术，如 '4G LTE'
    registration_state  TEXT,                    -- 网络注册状态
    tx_bytes            INTEGER DEFAULT 0,
    rx_bytes            INTEGER DEFAULT 0,
    connection_duration INTEGER DEFAULT 0,       -- 连接累计秒数
    vowifi_mode         NUMERIC DEFAULT 0,       -- 该卡是否走自建 VoWiFi 协议栈发短信
    vowifi_epdg_ip      TEXT    DEFAULT '',      -- ePDG IPv4（留空用环境变量）
    vowifi_at_port      TEXT    DEFAULT '',      -- AT 串口，如 /dev/ttyUSB2
    vowifi_airplane     NUMERIC DEFAULT 1,       -- 飞行模式：关射频、不在蜂窝注册（与 VoWiFi 解耦）
    PRIMARY KEY (id),
    UNIQUE (mm_object_path),
    UNIQUE (imei)
);
CREATE INDEX IF NOT EXISTS ix_modems_id ON modems (id);

-- ─────────────────────────────────────────────
-- SIM 卡访问申请记录（仅工作流历史，不存储当前授权状态）
-- 当前有效授权存储在 sim_grants 表
-- status: 'pending' | 'approved' | 'rejected'
-- requested_level: 'view'（仅查看）| 'use'（可发短信）
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sim_access_requests (
    id               INTEGER    NOT NULL,
    user_id          INTEGER    NOT NULL,
    modem_id         INTEGER    NOT NULL,
    status           VARCHAR(8) NOT NULL DEFAULT 'pending',
    requested_level  VARCHAR(4) NOT NULL DEFAULT 'use',  -- 'view' | 'use'
    reason           TEXT,       -- 申请人填写的申请理由
    admin_note       TEXT,       -- 审批员批注
    created_at       DATETIME,
    updated_at       DATETIME,
    PRIMARY KEY (id),
    FOREIGN KEY (user_id)  REFERENCES users (id) ON DELETE CASCADE,
    FOREIGN KEY (modem_id) REFERENCES modems (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ix_sim_access_requests_id      ON sim_access_requests (id);
CREATE INDEX IF NOT EXISTS ix_sim_access_requests_user_id ON sim_access_requests (user_id);

-- ─────────────────────────────────────────────
-- SIM 卡授权记录（当前有效授权，单用户单卡唯一）
-- 审批通过或直接授权时 UPSERT 到此表
-- 撤销时删除对应行；expires_at 为 NULL 表示永久有效
-- granted_level: 'view' | 'use'
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sim_grants (
    id              INTEGER    NOT NULL,
    user_id         INTEGER    NOT NULL,
    modem_id        INTEGER    NOT NULL,
    granted_level   VARCHAR(4) NOT NULL DEFAULT 'use',  -- 'view' | 'use'
    expires_at      DATETIME,                            -- NULL = 永久，否则到期自动失效
    granted_by_id   INTEGER,                             -- 授权操作人（管理员或审批员）
    request_id      INTEGER,                             -- 关联的申请记录，直接授权时为 NULL
    created_at      DATETIME,
    updated_at      DATETIME,
    PRIMARY KEY (id),
    UNIQUE (user_id, modem_id),                          -- 每用户每卡只保留一条有效授权
    FOREIGN KEY (user_id)       REFERENCES users (id) ON DELETE CASCADE,
    FOREIGN KEY (modem_id)      REFERENCES modems (id) ON DELETE CASCADE,
    FOREIGN KEY (granted_by_id) REFERENCES users (id),
    FOREIGN KEY (request_id)    REFERENCES sim_access_requests (id)
);
CREATE INDEX IF NOT EXISTS ix_sim_grants_id      ON sim_grants (id);
CREATE INDEX IF NOT EXISTS ix_sim_grants_user_id ON sim_grants (user_id);

-- ─────────────────────────────────────────────
-- 定时短信任务
-- cron_expression 与 send_once_at 二选一：
--   cron_expression 非空 → 周期任务（APScheduler CronTrigger）
--   send_once_at    非空 → 单次任务（APScheduler DateTrigger，UTC 存储）
-- status: 'active' | 'paused' | 'completed' | 'failed'
-- recipients: JSON 数组，如 ["+8613800138000", "+8613900139000"]
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sms_scheduled_tasks (
    id              INTEGER      NOT NULL,
    name            VARCHAR(100) NOT NULL,
    modem_id        INTEGER      NOT NULL,
    created_by_id   INTEGER,
    recipients      JSON         NOT NULL,
    content         TEXT         NOT NULL,
    cron_expression VARCHAR(100),  -- 与 send_once_at 互斥
    send_once_at    DATETIME,      -- UTC，前端提交前须转换
    status          VARCHAR(9),
    run_count       INTEGER,
    last_run_at     DATETIME,
    next_run_at     DATETIME,
    created_at      DATETIME,
    updated_at      DATETIME,
    PRIMARY KEY (id),
    FOREIGN KEY (modem_id)      REFERENCES modems (id),
    FOREIGN KEY (created_by_id) REFERENCES users (id)
);
CREATE INDEX IF NOT EXISTS ix_sms_scheduled_tasks_id ON sms_scheduled_tasks (id);

-- ─────────────────────────────────────────────
-- 短信记录
-- direction: 'inbound'（收到）| 'outbound'（发出）
-- mm_sms_index：mmcli SMS 对象索引，用于去重（非全局 ID）
--   去重键：(modem_id, mm_sms_index, direction='inbound')
-- status: 'sent' | 'failed' | 'received'
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sms_messages (
    id                INTEGER     NOT NULL,
    modem_id          INTEGER     NOT NULL,
    created_by_id     INTEGER,
    scheduled_task_id INTEGER,
    direction         VARCHAR(8)  NOT NULL,  -- 'inbound' | 'outbound'
    channel           VARCHAR(16) DEFAULT 'cellular', -- 'cellular'（蜂窝/mmcli）| 'vowifi'（自建 VoWiFi）
    phone_number      VARCHAR(30) NOT NULL,
    content           TEXT        NOT NULL,
    status            VARCHAR(8),            -- 'sent' | 'failed' | 'received'
    error_message     TEXT,
    mm_sms_index      VARCHAR(20),           -- mmcli SMS 对象索引，用于收件去重
    sent_at           DATETIME,
    received_at       DATETIME,
    created_at        DATETIME,
    PRIMARY KEY (id),
    FOREIGN KEY (modem_id)          REFERENCES modems (id),
    FOREIGN KEY (created_by_id)     REFERENCES users (id),
    FOREIGN KEY (scheduled_task_id) REFERENCES sms_scheduled_tasks (id)
);
CREATE INDEX IF NOT EXISTS ix_sms_messages_id ON sms_messages (id);

-- ─────────────────────────────────────────────
-- 短信模板
-- variables: JSON 数组，存储变量名列表，如 ["name", "code"]
--   前端从 content 中自动检测 {var} 占位符，发送前弹窗填写
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sms_templates (
    id         INTEGER      NOT NULL,
    name       VARCHAR(100) NOT NULL,
    content    TEXT         NOT NULL,
    variables  JSON,        -- 变量名列表，如 ["name","amount"]
    created_at DATETIME,
    updated_at DATETIME,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_sms_templates_id ON sms_templates (id);

-- ─────────────────────────────────────────────
-- 通知表
-- audience 控制可见范围：
--   'admin'   → 仅 role=ADMIN 用户
--   'support' → 管理员 + can_support=true 的角色
--   'all'     → 所有登录用户
--   'user'    → 仅 target_user_id 指定的用户
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notifications (
    id             INTEGER      NOT NULL,
    type           VARCHAR(32)  NOT NULL,
    title          VARCHAR(128) NOT NULL,
    body           TEXT         NOT NULL,
    is_read        BOOLEAN      NOT NULL,
    audience       VARCHAR(16)  NOT NULL,  -- 'admin' | 'support' | 'all' | 'user'
    target_user_id INTEGER,                -- audience='user' 时指定目标用户
    created_at     DATETIME     NOT NULL,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_notifications_id ON notifications (id);

-- ─────────────────────────────────────────────
-- 客服消息表
-- user_id：发起咨询的用户（会话归属）
-- sender_id：实际发送人（用户本人或客服人员）
-- is_from_user：true = 用户发送，false = 客服回复
-- attachment_*：文件消息，存储路径为 /opt/simnexus/uploads/<UUID>
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS support_messages (
    id              INTEGER  NOT NULL,
    user_id         INTEGER  NOT NULL,  -- 会话所属用户
    sender_id       INTEGER  NOT NULL,  -- 实际发送人
    content         TEXT     NOT NULL,
    is_from_user    BOOLEAN  NOT NULL,
    is_read         BOOLEAN  NOT NULL,
    created_at      DATETIME NOT NULL,
    attachment_url  TEXT,               -- 文件 URL，格式：/api/support/files/<uuid>
    attachment_name TEXT,               -- 原始文件名
    attachment_type TEXT,               -- MIME 类型，如 'image/png'
    PRIMARY KEY (id),
    FOREIGN KEY (user_id)   REFERENCES users (id),
    FOREIGN KEY (sender_id) REFERENCES users (id)
);
CREATE INDEX IF NOT EXISTS ix_support_messages_id ON support_messages (id);

-- ─────────────────────────────────────────────
-- Telegram 消息记录表
-- direction: 'in' = 从 Telegram 收到（用户发给 bot）
--            'out' = 从系统发出（bot 发给用户）
-- file_type: photo | document | video | sticker | voice（媒体消息）
-- file_id:   Telegram 文件 ID，通过 /api/telegram/file/<id> 代理下载
-- is_command: 是否为 bot 命令（/send /list /modems /help）
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS telegram_messages (
    id         INTEGER      NOT NULL,
    chat_id    VARCHAR(64)  NOT NULL,   -- Telegram chat/group ID
    username   VARCHAR(128),            -- 发送者用户名（收件时填写）
    direction  VARCHAR(8)   NOT NULL,   -- 'in' | 'out'
    text       TEXT         NOT NULL,   -- 消息文本（媒体消息存描述或文件名）
    is_command BOOLEAN,                 -- 是否为 bot 命令
    file_id    VARCHAR(256),            -- Telegram 文件 ID（媒体消息）
    file_type  VARCHAR(32),             -- photo | document | video | sticker | voice
    created_at DATETIME,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_telegram_messages_id      ON telegram_messages (id);
CREATE INDEX IF NOT EXISTS ix_telegram_messages_chat_id ON telegram_messages (chat_id);

-- ─────────────────────────────────────────────
-- 通讯录（每个用户私有，按 owner_id 隔离）
-- ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS contacts (
    id         INTEGER  NOT NULL,
    owner_id   INTEGER  NOT NULL,          -- 归属用户
    name       TEXT     NOT NULL DEFAULT '',
    phone      TEXT     NOT NULL DEFAULT '',
    company    TEXT     NOT NULL DEFAULT '',
    note       TEXT     NOT NULL DEFAULT '',
    created_at DATETIME,
    updated_at DATETIME,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS ix_contacts_owner_id ON contacts (owner_id);

-- ─────────────────────────────────────────────
-- 初始数据
-- ─────────────────────────────────────────────

-- 初始管理员账号 admin / admin123
INSERT OR IGNORE INTO users (id, username, password_hash, role, is_active, created_at, updated_at)
VALUES (1, 'admin', '$2b$12$J.PGYM99.G2T1/1wseKWI.HTcy.JskTr.4O5/ihgYUHnSU.NUefai', 'ADMIN', 1, datetime('now'), datetime('now'));

-- 系统预置角色（is_system=1，不可通过 API 删除）
-- SIM 卡管理范围通过 role_modem_scope 关联，初始为空（审批员默认全局权限）
INSERT OR IGNORE INTO roles (name, description, is_system, can_view_sim, can_approve_requests, can_view_history, read_only, can_support, created_at, updated_at) VALUES
    ('审批员',   '可查看SIM卡、审批申请、查看记录',         1, 1, 1, 1, 0, 0, datetime('now'), datetime('now')),
    ('普通用户', '可查看已授权SIM卡',                       1, 1, 0, 0, 0, 0, datetime('now'), datetime('now')),
    ('只读用户', '只读，不可发送',                           1, 1, 0, 1, 1, 0, datetime('now'), datetime('now')),
    ('客服',     '处理客服会话，可查看SIM卡',               1, 1, 0, 0, 0, 1, datetime('now'), datetime('now')),
    ('访客',     '无任何权限，需管理员手动分配角色',         1, 0, 0, 0, 0, 0, datetime('now'), datetime('now'));
