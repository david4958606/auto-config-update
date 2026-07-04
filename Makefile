# Makefile —— 出包脚本(计划 §0)。产出 CentOS6/内核2.6.32 可跑的【单个静态二进制】。
#
#   make            交叉编译 + 本机构建全部目标 (等价旧 build.sh all)
#   make native     本机二进制(自测用)
#   make cross      交叉编译 linux/386 静态
#   make win64      交叉编译 windows/amd64 exe
#   make tar        打包 tar.gz
#   make clean      清理构建产物
#
# 约束：工具链必须是 Go 1.23.x(1.24+ 的 runtime 要内核 3.2)。CGO_ENABLED=0 → 纯静态、
# 无需 gcc/glibc。依赖已 vendor 进仓库，可离线构建(GOPROXY=off)。

OUT     := auto-config-update
OUT32   := auto-config-update-32
OUTWIN  := auto-config-update.exe
LDFLAGS := -s -w

# 离线 + 静态 构建环境。
BUILD_ENV := CGO_ENABLED=0 GOFLAGS=-mod=vendor GOPROXY=off
GO_BUILD  := go build -trimpath -ldflags="$(LDFLAGS)"

DATE := $(shell date +%Y%m%d-%H%M%S)

.PHONY: all native cross win64 tar clean check-go

all: native cross win64

# 校验工具链主版本为 1.23.x。
check-go:
	@ver=$$(go env GOVERSION 2>/dev/null || echo unknown); \
	case "$$ver" in \
	  go1.23.*) : ;; \
	  *) echo "警告: 当前工具链 $$ver 非 go1.23.x —— 目标机 CentOS6/内核2.6.32 要求 go1.23.x" >&2 ;; \
	esac

native: check-go
	@echo "本机构建 -> $(OUT)"
	$(BUILD_ENV) $(GO_BUILD) -o "$(OUT)" .
	@echo "----"
	@file "$(OUT)" || true

cross: check-go
	@echo "交叉编译 linux/386 静态 -> $(OUT32)"
	$(BUILD_ENV) GOOS=linux GOARCH=386 $(GO_BUILD) -o "$(OUT32)" .
	@echo "----"
	@file "$(OUT32)" || true

win64: check-go
	@echo "交叉编译 windows/amd64 -> $(OUTWIN)"
	$(BUILD_ENV) GOOS=windows GOARCH=amd64 $(GO_BUILD) -o "$(OUTWIN)" .
	@echo "----"
	@file "$(OUTWIN)" || true

tar:
	@echo "打包 tar.gz -> auto-config-update-$(DATE).tar.gz"
	tar -czf auto-config-update-$(DATE).tar.gz "$(OUT)" "$(OUT32)" "$(OUTWIN)" features
	@echo "----"
	@ls -lh auto-config-update-$(DATE).tar.gz || true

clean:
	@echo "清理构建产物"
	rm -f "$(OUT)" "$(OUT32)" "$(OUTWIN)" auto-config-update-*.tar.gz
