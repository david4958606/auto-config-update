#!/usr/bin/env bash
# 把 example-config/config_old 升级到 example-config/config 的完整流程演示。
#
#   hack/run-upgrade.sh <目标目录> [plan|apply]
#
# 例：
#   hack/run-upgrade.sh /tmp/up  apply     # 拷贝 config_old 到 /tmp/up/config 并升级
#
# 三个 feature 必须按顺序施加(后一个依赖前一个已落盘的结果)：
#   1. upgrade-files     Setup/*.xml、SysLog_config.xml、Control_config.xml
#   2. upgrade-io        IOBridge 侧(IO 片段、IO_Platform、IO_Facility、Driver_Facility)
#   3. upgrade-control   Control 侧(加热器温差、EzZone 校准、互锁/报警)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${1:?用法: run-upgrade.sh <目标目录> [plan|apply]}"
MODE="${2:-plan}"

if [ "$MODE" != "plan" ] && [ "$MODE" != "apply" ]; then
  echo "模式只能是 plan 或 apply" >&2
  exit 2
fi

mkdir -p "$DEST"
if [ ! -d "$DEST/config" ]; then
  cp -a "$ROOT/example-config/config_old" "$DEST/config"
fi

cd "$ROOT"
CGO_ENABLED=0 GOFLAGS=-mod=vendor go build -o "$DEST/auto-config-update" .
cd "$DEST"

for f in upgrade-files upgrade-io upgrade-control; do
  echo "── $f ($MODE) ──"
  ./auto-config-update "$MODE" --feature "$ROOT/features/$f.yaml"
done

echo
echo "产物: $DEST/config"
echo "语义比对: go test ./internal/engine -run TestUpgradeExampleConfig"
