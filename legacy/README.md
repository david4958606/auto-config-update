# legacy —— Python 参照实现（对拍 oracle）

这是移植前的 Python 引擎，现役实现已是仓库根的 Go 版（`addex` 二进制）。保留它作为
**金标准对拍 oracle**：`testdata/golden/` 就是它生成的，Go 侧 `go test` 逐字节比对。

从仓库根运行（复用共享的 `config/`、`features/`）：

```bash
.venv/bin/python legacy/addex.py plan  --feature features/ig-auto-close.yaml
.venv/bin/python legacy/addex.py apply --feature features/ig-auto-close.yaml --chamber Ch1
```

行为说明见各模块 docstring 与仓库根 `GO_PORT_PLAN.md`。
