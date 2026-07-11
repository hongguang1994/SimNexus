#!/usr/bin/env bash
# SimNexus 一键部署脚本（Docker）
#   用法：在项目根目录执行  ./deploy.sh
#   作用：环境检查 → 准备 .env / 目录 → 首次部署自动导入初始数据 → 构建并启动容器
set -euo pipefail
cd "$(dirname "$0")"

info()  { printf '\033[36m%s\033[0m\n' "$*"; }
warn()  { printf '\033[33m⚠️  %s\033[0m\n' "$*"; }
ok()    { printf '\033[32m✅ %s\033[0m\n' "$*"; }
die()   { printf '\033[31m❌ %s\033[0m\n' "$*" >&2; exit 1; }

# ── 1. 依赖检查 ──────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || die "未安装 Docker，请先安装 Docker Engine"
docker compose version >/dev/null 2>&1 || die "未安装 Docker Compose v2（docker compose）"

# ── 2. 宿主机 ModemManager（mmcli 短信/状态依赖它；纯 ZTE/VoWiFi 可忽略）──
if command -v systemctl >/dev/null 2>&1 && ! systemctl is-active --quiet ModemManager 2>/dev/null; then
  warn "宿主机 ModemManager 未运行 —— mmcli 方式的短信/状态将不可用（纯 ZTE 或 VoWiFi 场景可忽略）"
fi

# ── 3. 数据/上传目录 ─────────────────────────────────────────
mkdir -p data uploads

# ── 4. .env（密钥不进仓库，从示例生成）──────────────────────
if [ ! -f .env ]; then
  cp .env.example .env
  warn "已从 .env.example 生成 .env，请按需填入 TELEGRAM_BOT_TOKEN / TELEGRAM_CHAT_ID（留空则禁用 Telegram）"
fi

# ── 5. 首次部署：导入初始数据（admin/admin123 + 系统角色）──
FRESH=0
[ -f data/sim_manager.db ] || FRESH=1
if [ "$FRESH" = 1 ]; then
  info "🌱 首次部署，导入初始数据…"
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 data/sim_manager.db < docs/schema.sql
  else
    # 宿主机无 sqlite3 时，用一次性容器导入
    docker run --rm -i -v "$PWD/data:/data" -v "$PWD/docs:/docs" alpine \
      sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /data/sim_manager.db < /docs/schema.sql'
  fi
  ok "初始数据已导入（账号 admin / admin123）"
fi

# ── 6. 构建并启动 ───────────────────────────────────────────
info "🚀 构建并启动容器（首次约 2–3 分钟）…"
docker compose up -d --build

echo
ok "部署完成！"
IP=$(hostname -I 2>/dev/null | awk '{print $1}'); [ -n "${IP:-}" ] || IP="<本机IP>"
echo "   访问：       http://${IP}:8899"
[ "$FRESH" = 1 ] && echo "   初始账号：   admin / admin123  （请登录后尽快修改密码）"
echo "   查看日志：   docker compose logs -f backend"
echo "   停止：       docker compose down"
