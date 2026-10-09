## Language Policy / 语言输出规则

## 语言

- 默认使用简体中文回复，包括计划、解释、总结、审查意见、提交信息和 PR 描述。
- 用户明确指定其他语言时，本次回复遵循用户要求。
- 保留代码标识符、API、命令、路径及日志原文。
- 代码注释和文档遵循项目既有风格；无明确规定时新增文档默认使用简体中文。

## 项目定位与部署约束

- 本项目是纯 Go 的设备 XML 配置升级工具，采用声明式 YAML 补丁，要求幂等执行和最小字节改动。
- 现场目标机为 CentOS 6、Linux 内核 2.6.32、32 位 x86（i686）；发布目标为 `GOOS=linux GOARCH=386`。
- 面向旧内核的发布必须使用 **Go 1.23.x 工具链本身**，仅在 [go.mod](go.mod) 中声明 `go 1.23` 不够。Go 1.24+ 的运行时要求 Linux 内核 3.2，不能用于该目标。
- 保持 `CGO_ENABLED=0`，不要引入 C 库或 cgo 依赖；纯 Go 静态二进制不依赖目标机的 glibc。当前第三方依赖只有 `gopkg.in/yaml.v3`，已纳入 `vendor/`，支持离线构建。
- 目标 CPU 若不支持 SSE2，需要用 `GO386=softfloat` 构建；不要在未核实硬件能力时改变默认值。
- 既支持现代开发机交叉编译，也支持工控机使用 Go 1.23.x 工具链离线本机构建；发布后检查静态链接，并在目标机进行冒烟验证。

## 构建与测试

以 [Makefile](Makefile) 为构建入口：

```bash
make amd64   # 本机自测二进制
make i386    # Linux/386 现场二进制
make win64    # Windows/amd64 二进制
make tar      # 构建并打包二进制、功能定义和文档

go test ./...
```

- 构建使用 `CGO_ENABLED=0 GOFLAGS=-mod=vendor GOPROXY=off`；修改依赖时保持离线构建可用。
- `make` 的工具链检查目前只打印警告，不会阻止错误版本继续构建；出包前必须自行确认 `go env GOVERSION` 为 Go 1.23.x。
- 部分 `internal/engine` 和 `internal/xmldoc` 测试依赖仓库根目录的 `config/` 夹具。缺少夹具时应明确报告失败原因，不要通过跳过或删除测试掩盖问题；新增用例优先在 `t.TempDir()` 中构造最小配置。
- Python 参照实现与旧金标准产物已移除，当前以 Go 测试为准。重点覆盖幂等、跨步骤可见性、实体保留、字节偏移与写盘正确性、筛选与多实例匹配，以及 Setup 参数序列校验。

## 架构与行为约束

- `internal/xmldoc`：带字节偏移的轻量 XML 树，容忍并保留实体引用，不展开设备外部实体。
- `internal/config`：装配主配置和实体片段，提供逻辑 IO/Control 视图；`internal/anchor`：路径定位、多实例展开和 `where` 筛选。
- `internal/feature`：YAML 加载、`require` 守卫及变量绑定；`internal/ops`：幂等动作；`internal/engine`：按腔室和步骤编排执行。
- `internal/splice`：应用字节编辑；`internal/setupcheck`：Setup 一致性校验；`internal/xmlcmp`：语义比对；`internal/simswitch`：模拟开关切换。
- `steps` 必须按声明顺序执行，后一步能定位和使用前一步的改动；不要把整套步骤改成仅基于初始树求值。
- class 路径支持同类多实例展开，逻辑 `/IO/...` 和 `/Control/...` 路径可跨片段解析；具体定位规则以 [原语参考](doc/feature-primitives.md) 和源码为准。
- 原语必须幂等：重复执行不得重复插入或产生额外改动；`plan` 不写盘，`apply` 落盘，二者使用一致的动作判定和日志口径。
- 保留 `&Simulated_Ch1;` 等实体引用原文，实体真名由配置声明解析。不得通过展开实体或重新序列化整个原始文档来实现局部修改。
- 回写只修改目标字节区间，保留其余内容的缩进、换行、属性顺序及实体书写；仅对新增内容进行必要渲染。
- Setup 的 `<Param name>` 与 `<Option>/<Value paramName>` 是按下标读取的并行序列，必须数量一致、逐项同名同序；只比较名字集合不足以保证正确。

## 运行路径与文档入口

- `features/` 和相对 `--feature` 路径以当前工作目录为基准。
- 配置目录默认取可执行文件同目录下的 `config/`；`--config <dir>` 的相对路径也以可执行文件目录为基准，绝对路径原样使用。不要恢复旧移植方案中按当前工作目录寻找配置的约定。
- 命令用法、交付文件和现场操作见 [README](README.md)；YAML 字段与原语语义见 [原语参考](doc/feature-primitives.md)。避免在项目指引中复制整套用法，修改行为时同步更新对应文档。
