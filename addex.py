#!/usr/bin/env python3
"""瘦入口：引擎已拆分到 src/ 包。见 src/__init__.py 的分层说明。

    uv run addex.py plan  --feature features/add-pedcurpos-dataex.yaml
    uv run addex.py apply --feature features/add-pedcurpos-dataex.yaml
    uv run addex.py plan  --feature ... --chamber Ch4
"""
import sys

from src.cli import main

if __name__ == "__main__":
    sys.exit(main())
