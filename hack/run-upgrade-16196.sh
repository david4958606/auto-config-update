#!/usr/bin/env bash
# 把 example-16196/config_old 升级到 example-16196/config 的完整流程演示。
#
#   hack/run-upgrade-16196.sh <目标目录> [plan|apply]
#
# 例：
#   hack/run-upgrade-16196.sh /tmp/up16196  apply   # 拷贝 config_old 到 /tmp/up16196/config 并升级
#
# 三个 feature 由 hack/gen_upgrade_16196.py 依据结构差异生成，全部是文件级步骤：
#   1. upgrade-16196-setup     Setup/*.xml（含 new-file 新建 10 个文件）、SysLog_config.xml
#   2. upgrade-16196-io        IOBridge/*
#   3. upgrade-16196-control   Control/*
#
# 注意：本夹具的目标配置自带一处供应商笔误（GasFlowCompens 里
# AlONGasFlowPieceCompens vs AlOGasFlowPieceCompens）。逐 feature 的 apply 自检会因此
# 报错，故这里用 --no-verify 关掉，最后统一跑 `check` 把它暴露出来（非零退出码）。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${1:?用法: run-upgrade-16196.sh <目标目录> [plan|apply]}"
MODE="${2:-plan}"

if [ "$MODE" != "plan" ] && [ "$MODE" != "apply" ]; then
  echo "模式只能是 plan 或 apply" >&2
  exit 2
fi

mkdir -p "$DEST"
if [ ! -d "$DEST/config" ]; then
  cp -a "$ROOT/example-16196/config_old" "$DEST/config"
fi

cd "$ROOT"
CGO_ENABLED=0 GOFLAGS=-mod=vendor go build -o "$DEST/auto-config-update" .
cd "$DEST"

for f in upgrade-16196-setup upgrade-16196-io upgrade-16196-control; do
  echo "── $f ($MODE) ──"
  if [ "$MODE" = "apply" ]; then
    ./auto-config-update apply --feature "$ROOT/features/$f.yaml" --no-verify
  else
    ./auto-config-update plan --feature "$ROOT/features/$f.yaml"
  fi
done

echo
echo "产物: $DEST/config"
echo "语义比对: go test ./internal/engine -run TestUpgradeExample16196"
echo

set +e
./auto-config-update check
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  echo
  echo "说明: 上面的 !! 是目标配置自带的笔误（AlONGasFlowPieceCompens vs AlOGasFlowPieceCompens）。"
  echo "      工具按目标保真复现，并由 check 判为 error、以非零退出码报错；正式环境应先修复再上线。"
fi
exit "$rc"
