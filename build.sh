#!/bin/sh
# build.sh —— 出包脚本(计划 §0)。产出 CentOS6/内核2.6.32 可跑的【单个静态二进制】。
#
#   ./build.sh            交叉编译目标机二进制 (linux/386, 静态)
#   ./build.sh native     本机二进制(自测用)
#
# 约束：工具链必须是 Go 1.23.x(1.24+ 的 runtime 要内核 3.2)。CGO_ENABLED=0 → 纯静态、
# 无需 gcc/glibc。依赖已 vendor 进仓库，可离线构建(GOPROXY=off)。
set -eu

OUT=addex
LDFLAGS="-s -w"

# 校验工具链主版本为 1.23.x。
ver=$(go env GOVERSION 2>/dev/null || echo unknown)
case "$ver" in
  go1.23.*) : ;;
  *) echo "警告: 当前工具链 $ver 非 go1.23.x —— 目标机 CentOS6/内核2.6.32 要求 go1.23.x" >&2 ;;
esac

if [ "${1:-cross}" = "native" ]; then
  echo "本机构建 -> $OUT"
  CGO_ENABLED=0 GOFLAGS=-mod=vendor GOPROXY=off \
    go build -trimpath -ldflags="$LDFLAGS" -o "$OUT" .
else
  echo "交叉编译 linux/386 静态 -> $OUT"
  CGO_ENABLED=0 GOOS=linux GOARCH=386 GOFLAGS=-mod=vendor GOPROXY=off \
    go build -trimpath -ldflags="$LDFLAGS" -o "$OUT" .
fi

echo "----"
file "$OUT" || true
echo "验证: 上面应显示 'statically linked'；在 2.6.32 真机跑 plan/apply 与 Python 对拍。"
