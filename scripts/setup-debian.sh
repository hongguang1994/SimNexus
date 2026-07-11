#!/bin/bash
# Debian/Ubuntu 系统依赖初始化（供本地开发或 Docker 宿主机使用）
#   运行: sudo bash scripts/setup-debian.sh
# 说明: 只装系统级依赖（ModemManager / udev / dialout / sqlite3）。后端为 Go、前端为 Node，
#       生产部署请直接用根目录的 ./deploy.sh（Docker）。

set -e

echo "=== 安装系统依赖 ==="
apt-get update
apt-get install -y \
    modemmanager \
    libdbus-1-dev \
    libglib2.0-dev \
    sqlite3 \
    udev

echo "=== 启动 ModemManager 服务 ==="
systemctl enable ModemManager
systemctl start ModemManager

echo "=== 添加当前用户到 dialout 组（允许访问串口）==="
usermod -aG dialout "$SUDO_USER"

echo "=== 配置 udev 规则（USB 4G 模块热插拔）==="
cat > /etc/udev/rules.d/99-usb-modem.rules << 'EOF'
# USB 4G modem - reload ModemManager on plug/unplug
SUBSYSTEM=="tty", ATTRS{idVendor}=="*", ENV{ID_MM_CANDIDATE}="1", TAG+="systemd", ENV{SYSTEMD_WANTS}="ModemManager.service"
EOF
udevadm control --reload-rules

echo ""
echo "✅ 系统依赖已就绪！"
echo ""
echo "本地开发（Go）:   cd backend-go && go run ."
echo "本地开发（前端）: cd frontend && npm install && npm run dev"
echo "生产部署（Docker）: ./deploy.sh"
echo ""
echo "⚠️  请重新登录以使 dialout 组权限生效"
