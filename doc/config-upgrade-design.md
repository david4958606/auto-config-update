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
| `add-xml` | 插入一段**内联 XML 片段**（整棵子树），逐字落盘、按父深度重排缩进 | 已有结构完全一致的兄弟 |
| `set-text` | 改写已存在元素文本（`tag`+可选 `child`/`attr`/`old`） | 文本已是目标值 |
| `set-attr` | 改写/插入已存在元素的属性 | 属性已是目标值 |
| `remove-node` | 删除已存在元素（含子树），`has` 子条件区分同名异内容节点 | 无命中 |
| `wrap` | 用 `open`/`close` 包裹节点区间（`comment: true` 即注释掉，或 CDATA 化） | 紧邻处已有标记 / 无命中 |
| `uncomment` | 放开（`drop: false`）或删除（`drop: true`）包住 `find` 的注释块 | 找不到包裹注释 |
| `file:`（step 级） | 让 step 直接作用于 `config/<file>` | — |

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
