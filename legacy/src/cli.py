"""cli —— plan / apply 两段式命令行。"""
from __future__ import annotations

import argparse

from .engine import apply_feature
from .feature import load_feature


def main() -> int:
    ap = argparse.ArgumentParser(description="auto-update-tool —— 配置幂等语义补丁 (demo)")
    ap.add_argument("mode", choices=["plan", "apply"], help="plan=dry-run, apply=写盘")
    ap.add_argument("--feature", required=True)
    ap.add_argument("--chamber", action="append", help="可重复；缺省=全部腔室")
    args = ap.parse_args()

    feature = load_feature(args.feature)
    print(f"功能 {feature['id']} v{feature['version']} | 模式={args.mode}"
          f"{' | 腔室=' + str(args.chamber) if args.chamber else ''}\n")
    apply_feature(feature, args.chamber, write=(args.mode == "apply"))
    return 0
