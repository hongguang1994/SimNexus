# ── 阶段1：编译 ──────────────────────────────────────────────────────────────
FROM golang:1.23-bookworm AS builder

WORKDIR /build

# 先复制依赖文件，利用 Docker 层缓存（代码变动时不重新下载依赖）
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译（CGO 需要开启，因为 gorm/sqlite 依赖 cgo）
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o simnexus .

# ── 阶段1.5：从源码编译 libqmi 1.36 ─────────────────────────────────────────
# bookworm 自带的 qmicli 是 1.32，缺 --uim-open-logical-channel/--uim-send-apdu，
# 无法走 QMI 版 USIM AKA（AT 抖动时的备选）。这里从源码编译 1.36 以获得这些 UIM 操作。
FROM debian:bookworm-slim AS qmi-builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates wget xz-utils pkg-config meson ninja-build gcc \
    libglib2.0-dev python3 gettext \
    && rm -rf /var/lib/apt/lists/*
ARG LIBQMI_VER=1.36.0
# 关掉 mbim_qmux/udev/qrtr/introspection/man 等，砍掉 libmbim/libgudev/gobject-introspection 依赖，
# 只留 qmicli + libqmi-glib（collection=full 保证含 UIM 逻辑通道/Send APDU 消息）。
RUN wget -q https://gitlab.freedesktop.org/mobile-broadband/libqmi/-/archive/${LIBQMI_VER}/libqmi-${LIBQMI_VER}.tar.gz \
    && tar xf libqmi-${LIBQMI_VER}.tar.gz && cd libqmi-${LIBQMI_VER} \
    && meson setup build --prefix=/usr --libdir=lib/x86_64-linux-gnu \
       -Dcollection=full -Dintrospection=false -Dman=false -Dgtk_doc=false \
       -Dbash_completion=false -Dqrtr=false -Dmbim_qmux=false -Dfirmware_update=false \
       -Dudev=false -Dmm_runtime_check=false -Drmnet=false \
    && ninja -C build && DESTDIR=/qmi-install ninja -C build install

# ── 阶段2：运行时镜像 ────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# 安装运行时依赖：
#   modemmanager / mmcli — 与宿主机 ModemManager 通信
#   ca-certificates      — HTTPS 请求（Telegram API）
#   libglib2.0-0 — 新编译 qmicli 的运行时依赖（mbim/udev 已在编译期关闭，无需其运行时库）
RUN apt-get update && apt-get install -y --no-install-recommends \
    modemmanager \
    ca-certificates \
    libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

# 装入从源码编译的 libqmi 1.36（qmicli + libqmi-glib .so），刷新动态库缓存
COPY --from=qmi-builder /qmi-install/ /
RUN ldconfig

# UTF-8 locale：否则 mmcli(GLib) 会拒绝含中文的参数(--messaging-create-sms=...text=中文)
# 报 "no actions specified"，导致中文短信发不出去。
ENV LANG=C.UTF-8 LC_ALL=C.UTF-8

WORKDIR /app

COPY --from=builder /build/simnexus .

# 上传文件存储目录
RUN mkdir -p /opt/simnexus/uploads

EXPOSE 8000

CMD ["./simnexus"]
