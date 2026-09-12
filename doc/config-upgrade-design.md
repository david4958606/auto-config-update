# config_old → config 升级方案（原语扩展 + feature 设计）

本文记录把 `example-config/config_old` 升级到 `example-config/config` 所需的**程序改动、
新原语**与 **feature 文件**。验收标准：在 `config/` 为 `config_old` 副本的工作目录下按序
运行 `features/upgrade-*.yaml`，产出的配置与用户提供的 `config/` **语义同等**——不要求逐字节
相同（忽略缩进/空行/属性书写顺序/`<x/>` 与 `<x></x>` 之别），但元素层级与标签、属性集合与取值、
叶子文本、实体引用（`&SimulatedFlag_Ch1;`）、以及"哪些块被停用"必须一致。

验收由 `internal/engine/upgrade_test.go::TestUpgradeExampleConfig` 自动完成（语义比对 + 幂等），
一键复现：

```bash
hack/run-upgrade.sh /tmp/up apply
go test ./internal/engine -run TestUpgradeExampleConfig
```

---

## 1. 变更目录（config_old → config）

按"改什么"归类。`N` 表示同一模式套用到多个腔室。

### 1.1 纯文本 / 属性值修正（已存在节点）

| 文件 | 节点 | 旧 → 新 |
|------|------|---------|
| `IOBridge/IO_Ch1`、`IO_Ch2` | `PedTempAI`/`ESCOuterTempAI`/`ESCInnerTempAI` 的 `<Max>` | `600` → `3276.7` |
| `IOBridge/IO_Ch1/2/4/5/6` | `DnBusStatus` 的 `<Bd>` | `1` → `100/200/400/500/600` |
| `IOBridge/IO_Ch4/5/6` | `BakeoutCommErrDI` 的 `<Bd>` | `200` → `400/500/600` |
| `IOBridge/IO_Platform` | `LoadBtnRlsDI` 的 `<Bd>` | `1001` → `31` |
| `Setup/CleanRuleRtInfo.xml` | `Value@paramName=CleanRuleDelayTimeInfo` 文本 | 改为新腔室串 |
| `IOBridge/Driver_Facility` | `addCmdAI` 的 `comment` 属性 | `Spare`→`PcwWtrFlowAI`、`PcwFlowAI`→`ReturnFlow9AI` |
| `Control/Interlock_Platform` | `HeatExchanger/setIntlkAlarm` 文本 | `20.0` → `50.0` |
| `Control/Interlock_*` | 若干 `setIntlkAlarm` / `setTrigger` / `addChecker` 文本 | 见 §5 生成结果 |

### 1.2 已存在块的"停用 / 启用"

- `Control/Interlock_Ch1/2/4/5/6`、`_ChC/ChD`、`_ChE/ChF`、`_Platform_ChA/ChB`、`_LA/_LB`：
  把 `RobotExToChN` / `SlotVlvNotClosed` / `Vtr1Extended` 等既有报警对**注释掉**（本方案实现为
  等价的**删除**：注释不参与解析，两者语义相同），并补新的 `*NotReady` / `*NotSafe` / `RobotInterlock` 报警对。
- `Control/Interlock_Platform`：把 `<!--<HeatExchanger …>…</HeatExchanger>-->` **放开**。
- `IOBridge/IO_Platform`：3 处 `<V6DO …>…</V6DO>` 注释掉；4 处 turbo/dry-pump 段用
  `<![CDATA[ … ]]>` 包住（等效停用）。
- `IOBridge/IO_Facility`：`<Chiller>…</Chiller>` 用 `<![CDATA[ … ]]>` 包住。

### 1.3 新增对象 / 数据点位 / 方法 / 参数

- `Control/Control_Ch1`、`Control_Ch2`：两个 `PhyHeater` 各加
  `setMonitorTempVp`、`setTempB4Offset` 方法 + `HeaterNTempB4OffsetVp` data。
- `Control/Control_ChC`、`Control_ChD`：加热器加 `setMonitorTempVp`、`setTempB4Offset` + `TempB4OffsetVp`。
- `Control/Control_ChE`、`Control_ChF`：`EzZoneTc` 各 Zone（含 Ped/LidShowerhead）加 3 个方法 +
  `ZoneNCalibrationOffsetAO` data。
- `Control/Interlock_Ch1/2/4/5/6`：`Devicenet<Ch>SourceDC`、`SourceStatus` data、
  `SrcDcPower/SrcDcStatusOn/SrcDcStatusOffAbnormal/SrcDcStatusOffNormal`；Ch4/5/6 另有
  `EscWaterFlow`、`OpenV1/OpenV31/OpenV32/OpenV33`、`EMag1DcOn/EMag2DcOn/BakeoutPowerOn` 等
  SInterlock 与大量 `createActionAlarm`/`addIntDescriptorAction`/`addDoubleValueAction`。
- `Control/Interlock_ChC/ChD`：`WaterLeak`/`CoverOpen` 加动作报警；新增 `IdleWafer` 互锁。
- `Control/Interlock_ChE/ChF`：新增 `Zone35MonitorAlarm`、`Zone35ControlAlarm`、
  `Src1CarrierGasCounterWarning/Alarm`。
- `Control/Interlock_Platform_ChA/ChB`：新增 `PinOriginPoint` 互锁。
- `IOBridge/IO_Facility`：`PcwWtrFlowAI` 移位 + 20+ 路 `*AI`；`<spare>`。
- `IOBridge/Driver_Facility`：`enable10ChannelMode`、多路 `addCmdAI`；删 `enable8ChannelMode`。
- `Setup/*.xml`：新增/删除 `<Param>`、`<Value>`。
- `SysLog_config.xml`：新增 `<FileSize>10</FileSize>`。
- `Control/Control_config.xml`：`TimeSynchronizer` 加 `<enableOneSecTimerUpdate type="method"/>`。

> 属性书写顺序（`<PIBBoxOpen class=… type=…>` → `<PIBBoxOpen type=… class=…>`）、
> 缩进与空行差异、`<Value></Value>` ↔ `<Value/>` 视为**语义无关**，本方案不处理。

---

## 2. 现有原语的能力缺口

改造前只有新增（`add-node`/`add-method`/`add-io`/`add-data`/`add-blank`/`add-comment`）与
按名+值删除（`remove-method`）。对 §1.1/§1.2 完全无能为力：

- 改不动已存在节点的文本/属性（§1.1）。
- 没有"注释掉 / 放开注释 / CDATA 包裹"（§1.2）。
- 删不了任意元素（`remove-method` 只能删 `type="method"` 且必须带值）。
- 建不了非 IO/data/method 的普通元素（`<Param>`/`<Value>`/`<FileSize>`/`<spare>`）。
- 覆盖不到 `Setup/*.xml`、`SysLog_config.xml`、`Control_config.xml` 这些**不属于** Control/IO
  片段的文件。

此外还有一个**隐蔽的硬伤**：设备配置里到处是 `A &amp;&amp; B` 这类"文本 + 实体"混合内容。
原解析器只保留"首个实体之前"的文本（`Node.Text`），且渲染器无法表达混合内容——任何按树
重建的写法都会把 `&amp;&amp;` 弄丢。这一条决定了 `add-xml` 必须**逐字插入**而不是渲染。

---

## 3. 新原语（已实现）

沿用既有约定：每个动作返回 `(changed, message, edit)`，**幂等**；`plan`/`apply` 输出一致；
落盘仍是"外科式字节编辑"，只动插入/删除/替换点。

| 原语 | 作用 | 幂等判据 |
|------|------|----------|
| `add-element` | 新增普通元素（attrs/text/自闭合），可 `before`/`after` 定位 | 同 tag+attrs+text 已存在 |
| `add-setup` | Setup 里成对追加 `<Param>` 声明与 `<Option>/<Value>` 取值（各落序列末尾） | 两侧按 `name`/`paramName` 各自判重 |
| `add-xml` | 插入一段**内联 XML 片段**（整棵子树），逐字落盘、按父深度重排缩进 | 已有结构完全一致的兄弟 |
| `set-text` | 改写已存在元素文本（`tag`+可选 `child`/`attr`/`old`） | 文本已是目标值 |
| `set-attr` | 改写/插入已存在元素的属性 | 属性已是目标值 |
| `remove-node` | 删除已存在元素（含子树），`has` 子条件区分同名异内容节点 | 无命中 |
| `wrap` | 用 `open`/`close` 包裹节点区间（`comment: true` 即注释掉，或 CDATA 化） | 紧邻处已有标记 / 无命中 |
| `uncomment` | 放开（`drop: false`）或删除（`drop: true`）包住 `find` 的注释块 | 找不到包裹注释 |
| `file:`（step 级） | 让 step 直接作用于 `config/<file>`；路径含占位符时按腔室展开 | — |

配套的底层改动：

| 位置 | 改动 |
|------|------|
| `internal/xmldoc` | `Node.OpenEnd`（开标签 `>` 后偏移）、`InnerText`（元素内部**全部**字符数据，解决混合内容）、`HasAttr`（区分"属性为空"与"没有该属性"）、`MarkSynthetic`/`Subs`（片段解析后清偏移 + 结构比较）、`EditKind` 增 `SetText`/`Wrap`/`Replace`/`InsertRaw` |
| `internal/splice` | 支持四类新编辑；插入块沿用文件主导换行（CRLF/LF 不再混用） |
| `internal/ops` | 上表 7 个原语；`SelectNodes` 改为子树搜索（`set-text`/`remove-node`/`wrap` 用），另留 `selectChildren` 专供 `before`/`after` 定位 |
| `internal/feature` | 新原语 YAML 结构体 + `SelSpec` 选择器 + 各动作的 `before`/`after` |
| `internal/engine` | 新原语派发、`file:` 文件级步骤、`add-xml` 缩进处理 |
| `internal/xmlcmp` | **新增**：语义比对（忽略空白/注释/属性序，严格比对元素树+实体+停用区），验收用 |

关于"停用"的取舍：目标配置把人写的停用写成 `<!-- ... -->` / `<![CDATA[ ... ]]>`。注释与 CDATA
内容**不参与解析**，所以"删掉"与"注释掉"语义相同。本方案对绝大多数停用直接 `remove-node`
（简单、且二次 apply 天然 no-op），对 `IO_Platform` 的 V6DO/CDATA 段则用 `wrap` 保留原文，
以演示该原语。两种做法都在验收里被接受。

---

## 4. Feature 划分

| 文件 | 覆盖 | 生成方式 |
|------|------|----------|
| `features/upgrade-files.yaml` | Setup/*.xml、SysLog_config.xml、Control_config.xml | `hack/gen_upgrade_files.py`（Param/Value 同一份列表生成，保证一一对应） |
| `features/upgrade-io.yaml` | IO_Ch1/2/4/5/6 量程板号、IO_Platform、IO_Facility、Driver_Facility | 手写 |
| `features/upgrade-control.yaml` | Control_Ch1/2/C/D/E/F 与全部 Interlock_* | `hack/gen_upgrade_control.py` 由结构差异生成 |

三个 feature **按上述顺序**施加（后者依赖前者已落盘）。全部使用 `file:` 文件级步骤：
Control/IO 片段虽可走腔室路由，但同一 tag 可能同时出现在多个片段（如 `Platform` 既是
`Control_Platform` 又是 `Interlock_Platform`、`IO_Platform` 又是 `IO_Facility` 的根），
文件级定位更精确、也不需要 `--chamber` 过滤。

`hack/gen_upgrade_control.py` 不依赖 lxml（lxml 会丢掉 `&amp;&amp;`），自带与 `internal/xmldoc`
同构的带偏移分词器：注释/CDATA 整段跳过、实体作为不透明子节点、节点原文即源字节切片。
自顶向下对齐新旧树后产出静态 YAML：

- 只在新侧出现 → `add-xml`（取新文件原文，实体书写逐字保留）；
- 只在旧侧出现 → `remove-node`；
- 同 tag/属性的叶子文本不同 → `set-text`；属性不同 → `set-attr`；
- 每个 anchor 额外带 `where.tag-glob`：anchor 段按 class 匹配，同 class 多实例（如
  `PhyHeater`/`PhyHeater2`/`PhyHeater3`）会 fan-out，必须用 tag-glob 钉死。

生成物是**静态 feature**，运行时完全不依赖目标 `config/`。

---

## 5. 验证

1. `internal/ops/ops_test.go`：7 个新原语各自的正向/幂等/边界（`old` 不匹配、`has` 条件、
   `attr` 空值 vs 缺失、`drop` 删除注释、实体逐字保留）。
2. `internal/engine/upgrade_test.go::TestUpgradeExampleConfig`：
   - 拷贝 `example-config/config_old` → 临时 `config/`；
   - 依次 apply 三个 feature；
   - 用 `internal/xmlcmp` 与 `example-config/config` 逐文件语义比对（非 XML 文件按字节）；
   - **Setup 结构约束**：逐文件提取 `<Param name>` 与 `<Option>/<Value paramName>` 两条序列，
     断言**数量相同、逐项同名同序**，且与目标配置的两条序列一致；
   - 再 apply 一遍，断言产物**逐字节不变**（幂等）。
3. `hack/run-upgrade.sh` 供人工复现。

当前结果：**语义差异 0 处，二次 apply 逐字节幂等**。

比对器另报 20 处 `order`（同级子节点集合相同、仅顺序不同），全部属于"新增节点追加在父节点末尾
vs 目标插在中间"这一类：

- `Control_Ch1/Ch2`、`Control_ChC/ChD`：加热器的新方法/data 追加在末尾；
- `Interlock_Ch1/2/4/5/6`、`Interlock_Platform`：`VInterlocks`/`SInterlocks` 下的新互锁对象追加在末尾；
- `IO_Facility/HeatExchanger`、`Driver_Facility/HeatExchanger`：新 IO 点位 / `addCmdAI` 追加在末尾。

`Setup/*.xml` 不在其中——Param 与 Value 两条序列已与目标**逐项同序**（见上文的专项校验）。

这些容器都是"按名/按标签查找"的集合（对象注册表、命名参数、通道映射、互锁列表），顺序不影响
运行语义；本方案从生成器里统一采用"追加到末尾"，不再逐个构造 `before`/`after` 定位。

---

## 6. 复验：example-16196（新增 `new-file` 原语）

`example-16196/` 是另一台设备的新旧两版配置，用来复验原语覆盖面。与 `example-config` 相比，
差异类型相同（`add-xml` / `set-text` / `set-attr` / `remove-node` / `add-element` 都够用），
但多出一个**新能力缺口**：目标版本新增了 10 个**完整的新文件**
（`Setup/GasFlowCompens_Ch{1,2,5,6}.xml`、`Setup/ProcessDataStableTime_Ch{1,2,5,6,C,D}.xml`），
而 `file:` 步骤要求文件已存在（`os.ReadFile` 会失败）。

### 6.1 新原语 `new-file`

step 级字段 `new-file: <相对 config/ 的路径>` + `content: <原文>`：

- 文件不存在 → 逐字写入 `content`（自动建父目录），保留 CRLF/LF 与是否带行尾换行；
- 文件已存在 → no-op，**绝不覆盖**（幂等判据=存在性）；
- `plan` 只打印"待新建"，不落盘。

10 个新文件因此与目标**逐字节一致**。

### 6.2 生成器 `hack/gen_upgrade_16196.py`

沿用 `gen_upgrade_control.py` 的带偏移分词器（不用 lxml，保住 `&amp;&amp;`），差异对齐升级为
**带评分的单调对齐**（精确匹配优先），并补一轮"跨位置配对"：位置被挪动、但两侧都还在的节点
**就地 `set-text`/`set-attr`**，既不删也不加。这样处理有两个好处：

1. 避免"`remove-node` + `add-xml` 同一节点"在二次 apply 时把刚补上的同名节点又删掉；
2. `Setup/*.xml` 的 `<Param>` 与 `<Value>` 序列**同步挪动**，始终一一对应
   （若只按各自的对齐结果删/加，两条序列会错位）。

匹配规则上有一点关键约束：`name` / `paramName` / `index` / `id` 是**身份属性**，取值不同即视为
"旧删+新加"，绝不改名——否则会把某个 `Param` 改名成另一个新增 `Param` 的名字，产生重复节点。

#### 6.2.1 逐腔室参数化（拒绝"按腔室复制粘贴"）

朴素的"按文件生成步骤"会把同一改动写成 `Ch1`/`Ch2`/… 一长串复制品，既不美观也不可复用。
生成器因此多做一次**归并**，让 feature 与 `features/add-pedcurpos-dataex.yaml` 同构：**一份声明、
逐腔室自动替换腔室名**（引擎的腔室循环 + 占位符）。

- **类归并**：同一 root class 的各腔室（`PVD`=Ch1/2/5/6、`LoadLock`=ChA/ChB/LA/LB、
  `Degas`=ChC/ChD、`TransferChamber`=Buffer/Transfer）改动同构时，合并成一个
  `anchor: <Class>/…` 的腔室级步骤，模板里用 `${<Class>}`。仅当该 class 的**每个**腔室都改了
  且逐项一致时才合并（否则会误伤未改动的腔室）。
- **腔室归并**：根没有 class（如 `Interlock_*`）时用保留占位符 `${Chamber}`（引擎绑定=当前腔室
  标签，见 6.2.2）。仅当该 anchor 路径在**其它腔室**里解析不到时才合并——非成员腔室会因
  anchor 不匹配自动跳过，避免把 `add-xml` 漏进不该改的腔室。
- 多根片段只要各根**标签一致**（如 `Driver_Ch1` 同时含容器 `<Ch1>` 与驱动 `<Ch1>`）也参与
  归并（`IOBridge/${Chamber}/SourceDC`）；标签不一致（`Control_EFEM`）或多根且路径不唯一时，
  仍退回文件级步骤。

效果：`upgrade-16196-control.yaml` 从 670 行降到 244 行、io 从 271 行降到 221 行，且
`Ch1/ProcessLogger`、`Ch2/ProcessLogger` 这类重复消失，代之以
`anchor: PVD/ProcessLogger` + `${PVD}`。剩余少量按文件的步骤是**必要**的：`Interlock_*` 的
`PinOriginPoint` 在全部腔室都存在（跨腔室 add 会漏改），`IO_Facility`/`IO_LoadRack` 是
单文件内多节点改写（不属于腔室 fan-out）。

#### 6.2.2 引擎新增：保留绑定 `{Chamber}`

`ApplyFeature` 现在为每个腔室恒定绑定 `chamberBinds["Chamber"] = <腔室标签>`（原实现只绑定
root 的 class）。这样根没有 class 的片段也能参数化——`anchor: ${Chamber}/VInterlocks/ChamberAtTemp`
按标签命中腔室根，取值里 `${Chamber}` 逐腔室替换。既有 feature/golden 不含 `{Chamber}` 文本，
不受影响。

### 6.3 性能与写盘修复（顺带）

**解析 O(n²)**：`internal/xmldoc` 的 `parser.has` / `skipUntil` / `readEntity` / `readEndTag`
原先写的是 `string(p.src[p.pos:])`，每遇到一个 `<`/`&` 就把**剩余全文**拷成字符串，整体退化成
O(n²)：`IO_Facility`（约 400 KB）单次解析要数秒。改为 `bytes.HasPrefix` / `bytes.Index` 后：

| | 修改前 | 修改后 |
|---|---|---|
| `engine.New`(加载全部片段) | ~18 s | ~0.02 s |
| `TestUpgradeExampleConfig` | ~95 s | ~1.2 s |

解析结果（字节偏移/树结构）不变，golden 逐字节对拍保持不变。

**同偏移插入/删除的写坏（时隐时现）**：把新节点插到"即将被删除的兄弟"之前时，插入点与删除
区间**起点相同**。`splice.Apply` 原先用 `sort.Slice`（不稳定）排序同起点补丁，若插入先于删除
执行，随后按原偏移执行的删除会把刚插入的文本一并吃掉，产物出现半截标签
（`croHeater type="method">…`）。改为 `sort.SliceStable` 且**同起点先删后插**，并补回归测试
`internal/splice/splice_test.go`。

### 6.4 验收

`internal/engine/upgrade16196_test.go::TestUpgradeExample16196`：拷贝 `config_old` → 依次
apply 三个 feature → `xmlcmp` 与目标逐文件语义比对 → Setup 的 Param/Value 按下标一一对应校验 →
二次 apply 逐字节幂等。

结果：**语义差异 0 处**；仅余 `Setup/Setup_Ch{1,2,5,6}.xml` 根与 `Option` 的 `order`——被挪位的
既有 `Param`/`Value` 就地保留（这是保住一一对应与幂等的前提）。Control/IO 侧已无 order 差异。
Setup 的 Param/Value 数量一致、逐项同名；二次 apply 逐字节幂等；10 个新文件逐字节一致。

### 6.4.1 一致性校验 `check`（笔误必须报错）

目标 `Setup/GasFlowCompens_Ch*.xml` 自身带一处供应商笔误：

```xml
<Param name="AlONGasFlowPieceCompens" .../>          <!-- 声明 -->
<Value paramName="AlOGasFlowPieceCompens">0</Value>  <!-- 取值 -->
```

`AlON…` 与 `AlO…` 不同名，参数在设备侧取不到值。这类问题必须在升级流程里被拦下，因此新增
`internal/setupcheck` 与 CLI 子命令 `check`：

- 提取每个 `Setup/*.xml` 的 `<Param name>` 序列与 `<Value paramName>` 序列；
- **全部按 error 处理**：数量不一致、`orphan-param`（有声明无取值）、`orphan-value`（有取值
  无声明）、`duplicate-param`/`duplicate-value`，以及 `index`（第 i 项不同名）；
- 关键口径：设备软件把 `Param` 与 `Value` 当**两个并行数组**按下标读取，**顺序即语义**，
  所以"名字集合一致、仅顺序不同"同样是错误（`index`），不能只按集合匹配；
- `auto-config-update check` 打印问题并**以非零退出码结束**；
- `apply` 结束默认跑同样的自检（`--no-verify` 可跳过），`plan` 只提示不失败。

对上面这处笔误，`check` 输出（退出码 1）：

```
!! Setup/GasFlowCompens_Ch1.xml: 第 1 项 Param=AlONGasFlowPieceCompens 与 Value=AlOGasFlowPieceCompens 不同名（设备按下标读取）
!! Setup/GasFlowCompens_Ch1.xml: Param name=AlONGasFlowPieceCompens 没有对应的 Value
!! Setup/GasFlowCompens_Ch1.xml: Value paramName=AlOGasFlowPieceCompens 没有对应的 Param
…（Ch2/Ch5/Ch6 同）
共 12 处问题，均必须修复（设备按数组下标读取 Param/Value）。
```

产物按目标**保真复现**该笔误，同时**必定被检出**——两者不矛盾：
`internal/engine/upgrade16196_test.go::checkSetup16196Issues` 断言产物与目标触发**完全相同**的
问题集合；`internal/setupcheck` 与 `main_test.go::TestVerifySetupGate` 各自覆盖检出逻辑与退出码。

### 6.5 仍未覆盖：精确重排

若要求"挪位节点也落在目标位置"（逐项同序），现有原语做不到：`remove-node` + `add-xml` 对
**同名同内容**的节点在二次 apply 时会互相抵消（新补的节点被删）。这需要新增 `move-node`
（按原字节区间搬移）原语。

另外，`check` 只校验"声明数组与取值数组按下标同名"这一条硬约束；它保证**两条序列彼此对齐**，
但不保证顺序与供应商目标一致。若目标顺序本身有语义（如数组还决定了 UI/执行顺序），
仍需 `move-node` 之类的重排能力。

