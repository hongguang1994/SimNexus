# ─────────────────────────────────────────────
# SimNexus Go 后端 Makefile
# 用法：make <target>
# ─────────────────────────────────────────────

# 输出二进制文件名
APP := simnexus

# 编译产物路径
BIN := ./bin/$(APP)

# 自动检测 go 命令：优先用 PATH 中的，找不到则用 Homebrew 路径
GO := $(shell which go 2>/dev/null || echo /opt/homebrew/bin/go)

# swag CLI 路径：优先 PATH，找不到用 ~/go/bin/swag
SWAG := $(shell which swag 2>/dev/null || echo $(HOME)/go/bin/swag)

# 声明伪目标（这些名字不对应实际文件，每次调用都会执行）
.PHONY: all build run clean tidy vet fmt swagger

# 默认目标
all: build

# 编译：生成二进制到 bin/simnexus
build:
	$(GO) build -o $(BIN) .

# 运行：不生成文件，直接启动（开发调试用）
run:
	$(GO) run .

# 整理依赖：更新 go.mod / go.sum，移除未使用的依赖
tidy:
	$(GO) mod tidy

# 静态检查：检测常见错误（不编译不运行）
vet:
	$(GO) vet ./...

# 格式化：自动格式化所有 .go 文件
fmt:
	$(GO) fmt ./...

# 更新 Swagger 文档：扫描 handler 注释，重新生成 docs/
swagger:
	$(SWAG) init --dir . --generalInfo main.go --output docs

# 清理：删除编译产物
clean:
	rm -f $(BIN)
