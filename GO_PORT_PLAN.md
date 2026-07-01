# Go 重写方案 — auto-update-tool → 静态可执行文件（CentOS 6 可跑）

## 0. 目标与约束

- 把现有 Python(`addex.py` + `src/`)引擎重写为 **Go**，产出**单个静态链接可执行文件**，
  直接拷到工控机运行，零依赖、零安装。
- 目标机：**CentOS 6 / 内核 2.6.32 / glibc 2.12**。

### 版本与工具链结论（已核实）

| 事项 | 结论 |
|------|------|
| Go 版本 | **必须用 Go 1.23.x 或更早** —— Go 1.23 是最后一个支持内核 2.6.32 的版本，1.24+ 要求内核 3.2。选 **go1.23.x**（最新的仍兼容版本）。 |
| glibc 2.12 | **无关**。`CGO_ENABLED=0` 的纯 Go 二进制是完全静态的，不链接 libc，只依赖内核 syscall ABI。 |
| 关键陷阱 | 语言等级 `go 1.23` 不够——**构建用的工具链本身**也必须是 1.23.x。用 1.24 工具链即便设 `go 1.23`，产出的 runtime 仍需内核 3.2。安装 go1.23.x SDK 或设 `GOTOOLCHAIN=go1.23.x`。 |
| 构建命令 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o addex .` |
| 待确认 | 工控机是 **amd64 还是 386(32 位)**？CentOS6 有 i686 版。定了才能定 `GOARCH`（`amd64`/`386`）。 |
| 验证 | `file addex` 应显示 *statically linked*；在 2.6.32 容器或真机上跑 `plan/apply` 对拍。 |

> 为什么值得重写而不是打包 Python：CentOS6 自带 Python 2.6，lxml 需针对老 glibc 编译，
> PyInstaller 在 2.6.32 上打包脆弱。Go 纯静态二进制是这个部署场景最干净的路径。

---

## 1. 现有架构回顾（Python，要 1:1 映射的行为）

引擎的本质：**声明式补丁 + 外科式文本写盘**。关键行为不能丢：

1. `steps` 有序执行，后一步能看到前一步在内存树里加的节点（跨步引用 `{IonGauge}`→`./IonGauge`）。
2. 靠 **class 路径**定位；leaf 同类多实例 **fan-out**；`where` 按 glob/attr 筛。
3. `require`(守卫+绑定)/`bind`(纯绑定)；逻辑路径 `/IO/...`、`/Control/...` 跨片段解析。
4. 动作 `add-node`/`add-method`/`remove-method`/`include-entity`，全部**幂等**。
5. **实体保留**：`&Simulated_Ch1;`(内核外部实体)加载不炸、回写原样保留；实体真名从
   `Control_config.xml` 声明里 glob 解析。
6. **外科式写盘**：只在插入/删除点改原文，其余字节逐字不动（clean diff）。

---

## 2. Go 侧最难的三件事（先想清楚再动手）

### 2.1 XML 解析：要“字节偏移”和“实体容忍”，而非完整 DOM

外科式写盘的根基是**每个标签的精确字节位置**，以及**把 `&Simulated_Ch1;` 当不透明标记**。
两条可选路线：

- **路线 A（推荐先试）：`encoding/xml` + 手动偏移跟踪。**
  - 用 `Decoder.Token()` 流式读；每读一个 token 后记 `Decoder.InputOffset()`（= 该 token 末字节）。
    上一 token 的末偏移 = 当前 token 的起始偏移 → 由此得到每个 `<tag>`/`</tag>` 的 `[start,end)`。
  - 未声明实体会报错 → 预扫 master 的 `<!ENTITY>` 声明，填 `Decoder.Entity[name]=""`，令
    `&Simulated_Ch1;` 解析为空 CharData（不炸；偏移照常前进）。我们不需要它的展开值。
  - 幂等判 `&name;` 是否已存在：直接在该节点原文字节区间里子串搜 `&name;`。
- **路线 B（备选）：自写极简 XML 分词器**（只认 标签/属性/文本/注释/`&实体;`，带字节偏移）。
  配置结构简单、我们又有 Python 参照 + 金标准测试兜底，可控性最高；但自写有边界 bug 风险。

> 建议：先用 A；若 `encoding/xml` 在自闭合/注释/CharData 边界的偏移精度上磨人，再切 B。
> 两条路线对上层接口一致（都产出“带字节区间的轻量节点树”），可无痛替换。

### 2.2 跨步可见性：可变轻量 DOM + 只序列化“新子树”

沿用 Python 的做法（不是纯文本拼接）：

- 解析出**原节点树**（每个原节点带 `[start,end)` 字节区间）。
- steps 在这棵树上**改**：加合成节点（标记 `synthetic`，无字节区间）、标记删除。
  步2 的 anchor 在树上查得到步1 加的合成 `IonGauge` → 跨步可见性成立。
- 落盘时把改动翻译成**字节编辑**：
  - 新节点，若其父是**原节点** → 把该新子树渲染成文本，插到父节点 `</tag>` 起始偏移处；
    父若也是合成节点 → 跳过（其文本已含在祖先渲染里）。
  - 删除的原节点 → 删掉它的 `[start,end)` 区间。
- **只需给“新节点”写一个小序列化器**（标签+有序属性+方法+`&实体;`），原内容永不重序列化。
  缩进由父深度算，字符串拼出——比 lxml 的 tail 操作还简单。

### 2.3 YAML 的灵活 schema

feature 里有 `require: - exist: {var: path}`、`bind: - {var: path}` 这种“单键 map 列表”。
用 `gopkg.in/yaml.v3`（纯 Go）。建议：顶层用带 tag 的 struct，灵活处用 `yaml.Node` 或
`[]map[string]yaml.Node` 承接，再自行取单键。

---

## 3. 建议的 Go 包结构

```
go.mod                     # module .../addex ; go 1.23
main.go                    # CLI 入口(flag): plan/apply, --feature, --chamber
internal/
  xmldoc/                  # 2.1+2.2：分词/偏移、轻量可变 DOM、新子树序列化器
  config/                  # master 实体声明解析、片段清单、字节读/写
  logical/                 # /IO、/Control 逻辑路径跨片段解析
  anchor/                  # class 路径定位 + fan-out + where
  ops/                     # 幂等原语 → 产出 Edit 记录
  splice/                  # 字节级 Edit 应用到 []byte
  feature/                 # YAML schema + require/bind 求值
  engine/                  # steps 编排：逐腔室/步/实例
testdata/                  # 复用现有 config/ 夹具 + 期望输出(golden)
```

### Python → Go 映射

| Python | Go 包 | 备注 |
|--------|-------|------|
| `model.py`(装配/寻址/读写) | `config` + `logical` + `xmldoc` | 拆成三块 |
| `anchor.py` | `anchor` | `fnmatch` → `path.Match` 或小 glob |
| `ops.py` | `ops` + `xmldoc`(渲染/缩进) | 返回 `Edit` |
| `feature.py` | `feature` | yaml.v3 |
| `splice.py` | `splice` | 行索引 → 字节偏移 |
| `engine.py` | `engine` | 逻辑照搬 |
| `cli.py`/`addex.py` | `main.go` | `flag` |

### 核心数据结构（示意）

```go
type Node struct {
    Tag       string
    Attrs     []Attr        // 有序，保留书写顺序
    Children  []*Node
    Parent    *Node
    Synthetic bool          // 本次新增(无字节区间)
    Removed   bool
    Start,End int           // 原节点在源文件的字节区间；合成节点为 -1
    IsEntity  bool          // &Simulated_Ch1; 这类
    EntName   string
}

type Edit struct {           // 由 ops 产出，splice 消费
    Kind   EditKind          // Insert | Delete
    Offset int               // Insert 用：父 </tag> 起点
    Text   string            // Insert 用：渲染好的新子树文本(带缩进)
    Start,End int            // Delete 用：字节区间
}
```

---

## 4. 依赖清单（尽量少，全纯 Go）

- 标准库：`encoding/xml`、`flag`、`os`、`path/filepath`、`strings`、`regexp`、`sort`。
- 第三方：**仅 `gopkg.in/yaml.v3`**（纯 Go，静态无碍）。
- 不用 cgo、不用任何 C 库。→ `CGO_ENABLED=0` 天然满足。

---

## 5. 验证策略：拿 Python 当“金标准”对拍

现有 Python 引擎 + `config/` 夹具就是最好的 oracle：

1. 对每个 feature × 每种模式，跑 Python `apply` 得到结果文件，存为 `testdata/golden/`。
2. Go 版跑同样输入，**逐字节比对**产物文件与 `plan` 文本输出。
3. 重点用例：
   - `ig-auto-close`：建对象、`include-entity`(`&Simulated_Ch1;` 最后一位)、跨步 `./IonGauge`、
     `require /Control/...`、fan-out。
   - `add-pedcurpos-dataex`：插入 + 删除、`/IO/...` 跨 `IO_Motor` 解析。
   - **幂等**：apply 两次，第二次零改动。
   - **实体计数不变 / clean diff**：`git diff` 只出现新增/删除行。
4. Go 单元测试覆盖：偏移解析、glob 择名(`Simulated_Ch1` vs `SimulatedFlag_Ch1`)、
   `where` 筛选、多匹配择一规则。

---

## 6. 分阶段落地（每步可独立验证）

1. **骨架 + 解析**：go.mod(go1.23)、`xmldoc` 解析出带字节区间的树 + 实体容忍；
   单测：对 `Control_Ch1` 解析后节点数/关键节点区间正确。
2. **只读能力**：`config` 片段清单/实体声明、`logical` 路径解析、`anchor`(含 fan-out/where)。
   里程碑：`plan` 能打印与 Python 一致的定位/跳过信息（还不写盘）。
3. **写盘**：`ops`(幂等 + Edit) + `splice`(字节编辑) + 新子树序列化器。
   里程碑：`apply` 产物与 Python 逐字节一致（golden 对拍通过）。
4. **feature 全量 + CLI**：`require/bind`、`include-entity` 择名、`remove-method`、`flag` 入口。
   里程碑：两个 feature 全绿、幂等。
5. **发布**：Go1.23 静态交叉编译；`file` 确认静态；2.6.32 容器/真机冒烟；写 `Makefile`/`build.sh`。

Python 版**保留到第 4 步全绿**，一直当对拍 oracle；确认无误后再决定去留。

---

## 7. 待你拍板的开放问题

1. **CPU 架构**：工控机是 x86-64(`amd64`) 还是 32 位(`386`)？决定 `GOARCH`。
2. **构建机**：在哪台机器出包？（在现代 mac/linux 上装 go1.23.x 交叉编译最省事，产物照样跑 2.6.32。）
3. **CLI 是否保持一致**：沿用 `plan/apply --feature --chamber` 即可？还是要加 `--config-dir` 等？
4. **Python 去留**：Go 通过全部对拍后，Python 版是删除，还是留在 `legacy/` 作参考？
5. **交付形态**：只要 `addex` 一个二进制，还是要连 `features/*.yaml`、`config/` 一起打个发布包？

## 8. 一句话小结

技术上完全可行且干净：**Go 1.23 + `CGO_ENABLED=0` 静态二进制**，glibc 无关、只依赖内核 2.6.32（正好达标）。
最核心的工程量在 **`xmldoc`（字节偏移解析 + 实体容忍 + 新子树渲染）** 和 **`splice`（字节编辑）**；
其余模块几乎是 Python 逻辑的直译。用现有 Python 引擎做金标准对拍，可把“行为不一致”的风险压到最低。
