# Feature YAML 原语参考

本文档汇总 `features/*.yaml` 当前支持的全部原语，覆盖每个原语的**语义、字段、生成的 XML、
判重（幂等）规则与示例**，以源码为准（`internal/feature`、`internal/anchor`、`internal/ops`、
`internal/engine`）。面向"要写一份新 feature"的读者；概览与整体设计见 [README](../README.md)。

> 术语：一个功能 = 一份 `features/<id>.yaml`，核心是有序的 `steps`。引擎对**每个腔室**加载
> 相关配置片段，按 `steps` 顺序执行；后一步可依赖前一步的产物。所有动作**幂等**：已达目标即
> no-op，重复运行不重复插入。

---

## 1. 文件骨架

```yaml
id: <功能标识>              # 用于日志与产物命名
description: <一句话描述>    # 人类可读；仅用于打印
version: 1                 # 原样标量(1 / "1.0" 都照打)
steps:                     # 有序步骤列表，见下
  - name: ...
    anchor: ...
    ...
```

| 顶层字段 | 含义 |
|---------|------|
| `id` | 功能标识（字符串）。 |
| `description` | 描述，仅用于打印。 |
| `version` | 版本标量，保留原始文本用于打印 `v{version}`。 |
| `steps` | 有序步骤列表，逐个执行。 |

---

## 2. Step 字段总览

每个 step 先用 `anchor`（+ 可选 `where`）定位到**一批实例节点**，再用 `require`/`bind` 做
守卫与变量绑定，最后对每个命中实例执行动作原语。

| 字段 | 类别 | 作用 |
|------|------|------|
| `name` | 元信息 | 步骤名，仅用于打印。 |
| `anchor` | 定位 | 一条 **class 路径**，逐层 descendant 定位；leaf 多实例会 fan-out。见 §3。 |
| `where` | 定位 | leaf 多实例时筛子集 + 插入定位（`before-method`）。见 §4。 |
| `require` | 守卫/绑定 | `exist` 守卫：路径解析不到则**跳过**该实例；命中则绑定变量。见 §5。 |
| `bind` | 绑定 | 纯绑定，不做存在性要求。见 §5。 |
| `add-node` | 动作 | 建对象节点，可内嵌实体引用。见 §7.1。 |
| `add-data` | 动作 | 建数据点位 `type="data"`。见 §7.2。 |
| `add-blank` | 动作 | 插入空行（方法块前的分隔）。见 §7.6。 |
| `add-comment` | 动作 | 插入注释行（方法块前的段注释）。见 §7.7。 |
| `add-method` | 动作 | 加方法调用。见 §7.3。 |
| `remove-method` | 动作 | 删方法调用。见 §7.4。 |
| `add-io` | 动作 | 建 IO 点位，可内嵌实体引用。见 §7.5。 |
| `add-element` | 动作 | 建普通元素(`<Param>`/`<Value>`/`<FileSize>`/`<spare>`)。见 §7.8。 |
| `add-setup` | 动作 | 向 Setup **成对追加** `<Param>` 与 `<Option>/<Value>`。见 §7.16。 |
| `add-xml` | 动作 | 插入一段**内联 XML 片段**(整棵子树，逐字保留实体书写)。见 §7.9。 |
| `set-text` | 动作 | 改写已存在元素的文本。见 §7.10。 |
| `set-attr` | 动作 | 改写/新增已存在元素的属性。见 §7.10。 |
| `rename-node` | 动作 | 重命名元素的**标签**(开/闭同步)，可同时增改属性。见 §7.17。 |
| `remove-node` | 动作 | 删除已存在元素(含子树)。见 §7.11。 |
| `wrap` | 动作 | 用 `open`/`close` 包裹一段节点区间(注释掉 / CDATA 化)。见 §7.12。 |
| `uncomment` | 动作 | 放开(或删除)包住某段文本的注释块。见 §7.13。 |
| `file:`(step 级) | 定位 | 让某个 step 直接作用于 `config/<file>`(已存在的文件)；路径含占位符则**按腔室展开**。见 §7.14。 |
| `new-file:`(step 级) | 动作 | 文件不存在时按 `content` **逐字新建**；已存在即 no-op；路径含占位符则按腔室展开。见 §7.15。 |

同一 step 内动作的**执行顺序固定**（与 YAML 中书写顺序无关）：
`add-node` → `add-data` → `add-element` → `add-setup` → `add-xml` → `rename-node` →
`set-text`/`set-attr`/`remove-node` → `wrap` → `uncomment` → `add-blank` → `add-comment` →
`add-method` → `remove-method` → `add-io`（见 §8）。
> 需要把注释/空行放到方法块**之后**时，另起一个**同 anchor 的 step**单独写 `add-comment`（追加落在末尾）。

---

## 3. `anchor` —— class 路径定位

`anchor` 是一条以 `/` 分隔的路径，逐层在子孙中定位节点。**靠 class 定位，与实例标签名无关**，
这样"各台设备实例名不同"也能命中。

### 3.1 域前缀

首段若为 `Control` / `IO` / `IOBridge`，作为**域**，路由到对应配置片段；否则默认 `Control`。

```
anchor: /Control/{ITO}/{PhyGauge}    # 显式 Control 域
anchor: ITO/PhyChuck                 # 无域前缀 → 默认 Control 域
anchor: /IO/${ITO}/IG                # IO 域
anchor: /IOBridge/${ITO}/{DnVacuumGauge}   # IOBridge 域(文件顶层节点是 IOBridge)
```

### 3.2 段写法

| 写法 | 匹配方式 | 说明 |
|------|---------|------|
| `{X}` | 按 **class** 匹配 | 命中后把变量 `X` 绑成该实例的**标签名**。 |
| 裸名 `X` | 按 **class** 匹配；无果回退按 **tag 名** | 某些层（如 IO 的 `<IG>`）无 class，回退用标签名。绑定同 `{X}`。 |
| `${X}` | 按 **tag 名** 匹配 | `X` 需已由上层（腔室级绑定）解析成具体标签（如 `${ITO}`→`Ch1`）；未绑定则该步跳过。 |

### 3.3 fan-out（同类多实例）

leaf（最后一层）命中同一 class 的**多个实例**时，对**每个实例各执行一次**——绝不静默只取第一个。
每次执行有独立的一份 tags：`{class名: 该实例标签}`，供占位符逐实例替换。中间层同样可各自
fan-out。

> 例：`anchor: CVD/PhyGauge`，若腔内有 `Gauge01`/`Gauge02`，动作各执行一次，
> `{PhyGauge}` 分别取 `Gauge01` / `Gauge02`。

---

## 4. `where` —— leaf 筛选与插入定位

`where` 仅作用于 leaf。三种子字段可组合：

| 子字段 | 作用 |
|--------|------|
| `tag-glob: "IonGauge"` | 按**标签名 glob**（`path.Match` 语义，支持 `*`）筛出子集。缺省=全部实例。 |
| `attr: { k: v }` | 按属性**全等**筛选（多个键需全部匹配）。 |
| `before-method: { name: init }` | **插入定位**（不参与筛选）：本步 `add-method` 插到名为 `init` 的既有方法**之前**，而非追加末尾。找不到该方法则退化为追加末尾并打印告警 `!`。 |

```yaml
where:
  tag-glob: "IG"
  before-method:
    name: init          # add-method 插到 <init> 之前
```

---

## 5. `require` / `bind` —— 守卫与绑定

两者都产出**变量**，供后续占位符引用。列表中每一项是一个单键 map。

### 5.1 `require`

目前仅支持 `exist` 一种（其它类型报"未知 require 类型"并跳过该实例）：

```yaml
require:
  - exist: { PedCurPos: "/IO/{CVD}/Ped/CurPosDI" }
```

语义：先对路径做占位符替换，再按**逻辑路径**（`/IO/...` 或 `/Control/...`，只看根腔室标签、
不看文件名）解析。

- 解析**不到** → 该实例**跳过**（打印原因）。
- 命中 → 把变量（`PedCurPos`）绑成该逻辑路径，并记录"命中于哪个文件"用于语义 diff。

### 5.2 `bind`

纯绑定，**不做存在性要求**。适用于"引用的目标不必仍存在"的场景（如 `remove-method` 要删的项）。

```yaml
bind:
  - PinCurPos: "/IO/{CVD}/Pin/CurPosDI"    # 删除项引用，用 bind 而非 require
```

---

## 6. 占位符 / 变量作用域

有**两种写法**，作用取决于出现的位置：

| 位置 | `{X}` | `${X}` |
|------|-------|--------|
| **anchor 段** | 按 **class** 匹配，命中后把 `X` 绑定为该实例标签（无 class 时回退 tag）。见 §3.2。 | 按 **tag 名** 匹配；`X` 必须**已绑定**（腔室 class 绑定 / require / bind），否则该步跳过。 |
| **所有取值字段** | 替换为 `tags[X]` | 替换为 `tags[X]`（**与 `{X}` 同解**） |

也就是说：**只有 anchor 段有区别**（一个是"按类找并绑定"，一个是"按已绑定的名字找"），
在 attr/value/xml/text/old/name/child/where/open/close/find 等**取值**里两者完全等价，
可随场景混用（路径里用 `${X}` 更直观，`{X}` 更简洁）。实现见 `internal/feature.Format`。

> 取值字段的替换覆盖**全部原语**：`add-node`(tag/class/attrs/include-entity)、
> `add-data`/`add-io`(name/attrs/Bd/Ch/Min/Max/Accuracy/DescriptorList/Unit)、
> `add-element`(tag/attrs/text)、`add-xml`(xml)、`add-method`/`remove-method`(name/value/attrs)、
> `set-text`(tag/child/old/value)、`set-attr`(tag/child/attr/old/name/value)、
> `remove-node`(tag/child/attr/value/has)、`wrap`(open/close/select)、`uncomment`(find/open/close)、
> `add-comment`，以及 step 级 `where`(tag-glob/attr)、`require`/`bind` 路径、`before`/`after` 选择器。
> 回归覆盖见 `internal/engine/placeholder_test.go`（两种写法混用、断言产物不残留占位符）。
> `file:` / `new-file:` 的**路径**是唯一"作用域由路径自身决定"的位置：不含 `{` 时全局执行一次
> （无腔室上下文，按字面处理）；含 `{` 时按腔室展开，可用 `${Chamber}` 与各腔室 class 绑定
> （见 §7.14）。`content` 始终按字面写入，不做占位符替换。

变量来源：

- **class 段绑定**：anchor 路径上每个 class 匹配到的实例标签，逐实例自动绑定（`PhyGauge`→`Gauge01`）。
- **`{Chamber}` 保留绑定**：每个腔室恒定绑定 `Chamber`→该腔室标签（如 `Ch1`），**与 class 无关**。
  片段根没有 class（如 `Interlock_*`）、或同一改动要跨多个 class 的腔室复用时，用它写"一份声明、
  逐腔室替换腔室名"的 feature：
  ```yaml
  - name: PVD 腔室稳定时间改引用 SETUP
    anchor: PVD/ProcessLogger                 # 按 class PVD 命中 Ch1/Ch2/Ch5/Ch6
    set-text:
      - { tag: setDataStableTime, attr: { type: method },
          old: "/IO/${PVD}/SourceDC/ActPowerAI,1",
          value: "/IO/${PVD}/SourceDC/ActPowerAI,/SETUP/${PVD}/ProcessDataStableTime/SrcPwrStableTime" }

  - name: 腔室 96℃ 告警降级（根无 class 的 Interlock_*）
    anchor: ${Chamber}/VInterlocks/ChamberAtTemp   # ${Chamber} 作 anchor 段=按标签名匹配腔室根
    set-text:
      - { tag: setIntlkAlarm, attr: { type: method },
          old: "Warning,ERROR,${Chamber} chamber temperature is over 96 centigrade.",
          value: "Warning,NOTICE,${Chamber} chamber temperature is over 96 centigrade." }
  ```
  注意：anchor 首段写 `PVD`（裸名=按 class 匹配）适用于该类全部腔室；写 `${Chamber}` 则命中**任意**
  腔室，需自行保证非目标腔室不会匹配到该 anchor 路径（否则会误改），必要时用 `require` 守卫跳过。
- **跨步对象引用**：`add-node` 建出的对象登记 `{标签}` → `./标签`，供**后续步骤**引用（如
  `{IonGauge}` → `./IonGauge`）。
- **`require`/`bind` 变量**：见 §5。
- `include-entity` 的 glob 也先做占位符替换，再去 `Control_config.xml` 的实体声明里匹配真名。

占位符替换作用于：`add-node` 的 `attrs` 值与 `include-entity`、`add-method`/`remove-method` 的
`value` 与额外 `attrs` 值、`add-io`/`add-data` 的 `attrs` 值与 `Accuracy`/`DescriptorList`/`Unit` 等。

---

## 7. 动作原语

每个原语返回 `(changed, message)`：`changed=false` 表示已达目标（no-op）。语义 diff 里**每个
声明的动作都出一行**：`+` = 有改动，`-` = 幂等 no-op。

### 7.1 `add-node` —— 建对象节点

在 anchor 下确保存在 `<tag class="class" attrs.../>`，**按 tag 判重**（已存在即 no-op）。

| 字段 | 说明 |
|------|------|
| `tag` | 新对象标签名。判重键。 |
| `class` | class 属性值（渲染为首属性 `class="..."`）。 |
| `attrs` | 额外属性，**保留书写顺序**，值支持占位符。 |
| `include-entity` | 可选。glob（先占位符替换）匹配 `Control_config.xml` 声明的实体真名，命中则把 `&实体名;` 内嵌为该对象**最后一个子节点**；未匹配到则打印告警 `!`。 |

```yaml
add-node:
  - tag: IonGauge
    class: PhyGauge
    attrs: { type: instance, alias: "/Control/${ITO}Exports/IonGauge" }
    include-entity: "Simulated*{ITO}"     # → 内嵌 &Simulated_Ch1;
```

生成：`<IonGauge class="PhyGauge" type="instance" alias="/Control/Ch1Exports/IonGauge"> &Simulated_Ch1; </IonGauge>`

### 7.2 `add-data` —— 建数据点位

在 anchor 下确保存在 `<name type="data" attrs...>`，**按 name 判重**。与 `add-io` 的唯一差别：
首属性固定 `type="data"`。其余子元素/实体规则与 `add-io` 一致。

| 字段 | 说明 |
|------|------|
| `name` | 点位标签名。判重键。 |
| `attrs` | 属性（`type="data"` 之后，保留书写顺序，值支持占位符）。 |
| `Bd` | 板号。`auto` = 自适应推断（见 §9）；否则取字面值。 |
| `Ch` | 通道号（字面值）。 |
| `Min` / `Max` / `Accuracy` | 可选子元素。 |
| `DescriptorList` | 可选。`name/value` 列表，渲染成 `<DescriptorList>OFF:0,ON:1</DescriptorList>`；空列表则不加。 |
| `Unit` | 可选。未配则不加；`NULL`/空串→成对空标签 `<Unit></Unit>`。 |
| `include-entity` | 可选。glob（先占位符替换）匹配顶层声明的实体真名，命中则把 `&实体名;` 内嵌为点位**最后一个子节点**（同 `add-node`/`add-io`）；未匹配到则打印告警 `!`。 |

子元素顺序：`Bd`、`Ch`、`[Min]`、`[Max]`、`[Accuracy]`、`[DescriptorList]`、`[Unit]`、`[&实体;]`。
子元素文本规则：

- **未配**（该键缺省）→ 该子元素**不加**。
- 配为 `NULL` 或空串 → 渲染成**成对空标签** `<Bd></Bd>`（而非自闭合 `<Bd/>`）。
- 否则取字面文本。

> 注意：`attrs` 里的 `simulated` 只是普通属性，**不**驱动实体引用；要追加 `&SimulatedFlag_ChN;`
> 之类的引用须显式写 `include-entity`。

同一 anchor 内 `add-data` **排在 `add-method` 之前**，便于方法引用 `./name`。

```yaml
add-data:
  - name: TempB4OffsetVp
    attrs: { dataType: "D", accessMode: "R", simulated: "true", alias: "/IO/${Degas}Exports/Heater_TempB4Offset" }
    include-entity: "SimulatedFlag*{Degas}"   # → 点位内末尾内嵌 &SimulatedFlag_ChD;
    Bd: NULL
    Ch: NULL
    Min: NULL
    Max: NULL
    Accuracy: 0.00000001
```

### 7.3 `add-method` —— 加方法调用

在 anchor 下确保存在 `<name type="method" ...>`。

| 字段 | 说明 |
|------|------|
| `name` | 方法名。 |
| `value` | 可选。有值 → `<name type="method">值</name>`；无值 → 标志型方法，自闭合 `<name type="method"/>`。 |
| `attrs` | 额外属性（如 `comment: ...`），追加在 `type="method"` **之后**，**不参与判重**。 |

判重规则：

- **无 value**（标志型）→ 按**名字**判重。
- **有 value** → 按**名字 + 值**判重（同名不同值可共存）。

插入位置：若本步 `where.before-method` 命中，则插到该既有方法**之前**；否则追加末尾。

```yaml
add-method:
  - name: setOnOffVp
    value: "/IO/${ITO}/IG/OnOffDI"
  - name: enableModeSwitch                 # 无 value → <enableModeSwitch type="method"/>
  - name: addExplicitMsgDI
    attrs: { comment: "DisplayIgOnOff (OFF:0,ON:1)" }
    value: "4101,0E|31|02|5D,8E|0"
```

### 7.4 `remove-method` —— 删方法调用

删 anchor 下**名字 + 值**都匹配的方法调用；命中多个**一次删净**记为一处编辑，一个不中记为 no-op。

| 字段 | 说明 |
|------|------|
| `name` | 方法名。 |
| `value` | 匹配值（占位符替换后按文本全等比较）。 |

```yaml
remove-method:
  - name: addDataEx
    value: "{PinCurPos},PinCurPos,1"
```

### 7.5 `add-io` —— 建 IO 点位

在 anchor（如 `<IG>`）下确保存在 IO 点位 `<name attrs...>`，**按 name 判重**。

| 字段               | 说明                                                                                    |
|------------------|---------------------------------------------------------------------------------------|
| `name`           | 点位标签名。判重键。                                                                            |
| `attrs`          | 属性（保留书写顺序，值支持占位符）。`simulated` 只是普通属性，不驱动实体引用。                                         |
| `Bd`             | 板号。`auto` = 自适应推断（见 §9）；否则字面值。                                                        |
| `Ch`             | 通道号（字面值）。                                                                             |
| `Min` / `Max`    | 可选。未配则不加；配了则加（空串→成对空标签）。                                                              |
| `Accuracy`       | 可选。未配则不加；`NULL`/空串→成对空标签。                                                             |
| `DescriptorList` | `name/value` 列表，渲染成 `<DescriptorList>OFF:0,ON:1</DescriptorList>`；空列表则不加。             |
| `Unit`           | 可选。未配则不加；`NULL`/空串→成对空标签 `<Unit></Unit>`。                                             |
| `include-entity` | 可选。glob（先占位符替换）匹配顶层声明的实体真名，命中则把 `&实体名;` 内嵌为点位**最后一个子节点**（同 `add-node`）；未匹配到则打印告警 `!`。 |

子元素顺序：`Bd`、`Ch`、`[Min]`、`[Max]`、`[Accuracy]`、`[DescriptorList]`、`[Unit]`、`[&实体;]`。
空值子元素渲染成成对空标签（如 `<Unit></Unit>`），与既有片段写法一致。

```yaml
add-io:
  - name: OnOffDO
    attrs: { dataType: "I", accessMode: "RW", simulated: "", alias: "/IO/${ITO}Exports/IG_OnOffDO" }
    include-entity: "Simulated*{ITO}"        # → 点位内末尾内嵌 &Simulated_Ch1;
    Bd: auto
    Ch: 4901
    Min: 0
    Max: 1
    DescriptorList:
      - { name: OFF, value: 0 }
      - { name: ON,  value: 1 }
```

生成（含末尾 `&Simulated_ChN;`，因 `include-entity` 命中）：

```xml
<OnOffDO dataType="I" accessMode="RW" simulated="" alias="/IO/Ch1Exports/IG_OnOffDO">
  <Bd>100</Bd><Ch>4901</Ch><Min>0</Min><Max>1</Max>
  <DescriptorList>OFF:0,ON:1</DescriptorList>
  &Simulated_Ch1;
</OnOffDO>
```

### 7.6 `add-blank` —— 插入空行

在 anchor 下插入 `N` 行空行（渲染为**无缩进**的空行），作为方法块前的分隔。

| 字段 | 说明 |
|------|------|
| `add-blank` | 整数 `N`：插入的空行行数。`0` 或缺省 = 不插入。 |

判重（幂等）：注释与空行**不进解析树**（解析器直接跳过），故按 anchor 的**原始字节区间**
（`<tag ...>` 到 `</tag>` 之间）扫描——**已存在空行则 no-op**，避免二次 `apply` 反复堆空行。

```yaml
add-blank: 1        # → 在方法块前插入一行空行
```

### 7.7 `add-comment` —— 插入注释

在 anchor 下插入若干注释行，**原样输出**所提供的完整注释文本（须自带 `<!--` 与 `-->`）。

| 字段 | 说明 |
|------|------|
| `add-comment` | 字符串列表；每项是一行完整注释原文（如 `"<!--PG_INFICON-->"`），值支持占位符替换。 |

判重（幂等）：同 `add-blank`，按 anchor 原始字节区间**整串包含**判重——注释原文已出现则 no-op。

> 注意：注释文本必须是**合法闭合**的 `<!--...-->`。若结尾缺一个 `-`（写成 `->`），二次 `apply`
> 重新解析该片段时会因找不到 `-->` 而报错。

```yaml
add-comment:
  - "<!--PG_INFICON-->"                 # 方法块前的段注释
```

### 7.8 `add-element` —— 新增普通元素

建一个不带头部约定(既不是 method、也不是 io/data)的元素，如 Setup 的 `<Param>`/`<Value>`、
SysLog 的 `<FileSize>`、IO 的 `<spare>`。

| 字段 | 说明 |
|------|------|
| `tag` | 元素标签。判重键的一部分。 |
| `attrs` | 属性(保留书写顺序，值支持占位符)。 |
| `text` | 文本(空串=无文本)。 |
| `self-close` | true → 渲染成 `<tag .../>`。 |
| `paired-empty` | 文本为空且非自闭合时渲染成 `<tag></tag>`。 |
| `before` / `after` | 可选选择器 `{tag, attr, value}`：插到该兄弟之前/之后；缺省追加末尾。 |

判重：已存在"同 tag + 同 attrs + 同 text"的子元素即 no-op。

```yaml
add-element:
  - tag: Value
    attrs: { paramName: Heater1TcTempDiffMax }
    text: "10"
    after: { tag: Value, attr: { paramName: HeaterWaterVlvOpenTemp } }
  - { tag: Param, attrs: { name: X, dataObject: /SETUP/.../X, type: D, min: 0, max: 300, units: K, accuracy: 0.0000001, default: 0 }, self-close: true, after: { tag: Param, attr: { name: HeaterWaterVlvOpenTemp } } }
```

### 7.9 `add-xml` —— 插入内联 XML 片段

把一个完整的 XML 子树原样插入 anchor 下。片段**不经过渲染器**（逐字落盘、按父深度重排缩进），
因此 `&amp;&amp;`、`&lt;` 这类实体书写原封不动——设备配置里大量表达式的写法必须靠它才能保真。

| 字段 | 说明 |
|------|------|
| `xml` | 片段原文(支持占位符)；应为单个根元素。 |
| `before` / `after` | 可选插入定位。 |

判重：anchor 下已存在**结构与文本完全一致**的兄弟节点即 no-op(所以同名新方法可以成批插)。

```yaml
add-xml:
  - xml: |
      <SrcDcPower class="VInterlock" type="instance" alias="">
        <setDurationTrigger type="method">(((/IO/LoadRack/Ch4/SourceDC/PowerAO == 0.0)&amp;&amp;(/IO/LoadRack/Ch4/SourceDC/OnoffDO == Off))&amp;&amp;(/Control/Ch4/VInterlocks/SourceStatus == OffAbnormal)),4000</setDurationTrigger>
        <setIntlkAlarm type="method">Alarm,ERROR,Ch4 chamber turn off SourceDC failed.</setIntlkAlarm>
        &SimulatedFlag_Ch4;
        <acceptUnknown type="method">TriggerUnknown,FATAL,Interlock trigger state is unknown. (Hardware IO may be unavailable)</acceptUnknown>
      </SrcDcPower>
```

### 7.10 `set-text` / `set-attr` —— 原地改写

```yaml
set-text:
  - { tag: Max, old: "600", value: "3276.7" }                 # 改 anchor 直接子元素 <Max> 的文本
  - { tag: LoadBtnRlsDI, child: Bd, old: "1001", value: "31" } # 改孙元素 <LoadBtnRlsDI><Bd> 的文本
set-attr:
  - { tag: addCmdAI, old: "Ch = 1080 Spare", name: comment, value: "Ch = 1080 PcwWtrFlowAI" }
```

| 字段 | 说明 |
|------|------|
| `tag` / `child` | 目标元素(或再下沉一层到 `child`)。 |
| `attr` | 目标元素须**存在且相等**的属性(用 `{alias: ""}` 可区分"空值"与"没有该属性")。 |
| `old` | 可选：只命中"当前文本/当前属性值 == old"的节点。 |
| `value` | 新文本 / 新属性值(支持占位符)。 |
| `name` | `set-attr` 专有：要改写的属性名(不存在则插入到开标签 `>` 之前)。 |

判重：文本/属性已是目标值即 no-op。文本比较用元素内部**全部**字符数据（含实体之后的文本）。

### 7.11 `remove-node` —— 删除已存在元素

```yaml
remove-node:
  - { tag: PcwWtrFlowAI, has: { tag: Ch, value: "1240" } }   # 只删内部 Ch=1240 的那一个
  - { tag: addCmdAI, value: "1240" }
```

`has` 是附加条件(该元素须含有匹配此选择器的直接子元素)，用于区分同名但内容不同的节点。
命中多个则全部删除；无命中即 no-op。

> 设备配置里"停用"某块常写成把它注释掉；注释内容不参与解析，故**删除与注释在语义上等价**，
> 本工具两者都支持（删除用 `remove-node`，保留原文用 `wrap comment`）。

### 7.12 `wrap` —— 包裹一段节点区间(注释掉 / CDATA 化)

```yaml
wrap:
  - comment: true                     # 等价 open="<!--" close="-->"
    select:
      - { tag: createCheckerAlarm, value: "RobotExToCh4,ERROR,Robot extended to Ch4 chamber." }
      - { tag: addChecker, value: "(/IO/Platform/Plc/Ch4/SvClosedDI == Closed),SlotVlvNotClosed" }
  - open: "<![CDATA["
    close: "]]>"
    select: [ { tag: Chiller } ]
```

区间 = 全部命中节点的 `[min(Start), max(End))`；`open`/`close` 直接插在区间两端。
紧邻处已有该标记或命中为空 → no-op。被 `<!-- -->`/`<![CDATA[ ]]>` 包住的内容解析器本就跳过，
所以二次 apply 自然无匹配。

### 7.13 `uncomment` —— 放开/删除注释块

```yaml
uncomment:
  - { find: "HeatExchanger class=\"VInterlock\"", drop: false }   # 去掉包住它的 <!-- -->
  - { find: "platformtype_12k", drop: true }                      # 连注释内容一起删
```

在原文里定位 `find`，向前找最近的 `open`(缺省 `<!--`)、向后找最近的 `close`(缺省 `-->`)，
校验该区间确实包住 `find` 后去掉标记(或整段删除)。找不到 → no-op。

### 7.14 文件级步骤 `file:`

Setup/SysLog/主文件这类**不属于 Control/IO 片段**的文件，用 `file:` 直接定位：

```yaml
steps:
  - name: SysLog 增加 FileSize
    file: SysLog_config.xml      # 相对 config/ 的路径
    anchor: SysLog               # 相对该文件根元素(首段可匹配根自身)
    add-element:
      - { tag: FileSize, text: "10", after: { tag: Threshold } }
```

**字面量路径**（不含占位符）的步骤不参与腔室循环，在腔室步骤之后按声明顺序各执行一次，
直接读该文件、改完写回。

**按腔室展开**：路径里含占位符时（如 `${Chamber}` 或某腔室的 class 名），该步骤改为在腔室
循环内**逐腔室执行一次**——用该腔室的绑定解析路径、`anchor`/`where` 与各动作的取值字段，
因此一份声明就能覆盖所有腔室：

```yaml
steps:
  - name: 每个腔室的 Setup 都加一个参数
    file: Setup/Setup_${Chamber}.xml     # → Setup/Setup_Ch1.xml、Setup/Setup_Ch2.xml …
    add-setup:
      - param: SrcDCMax_${Chamber}       # → SrcDCMax_Ch1 / SrcDCMax_Ch2 …
        dataObject: /SETUP/${Chamber}/Source/SourceDC/CurrentOutputMaxPercent
        type: D
        min: 0
        max: 100
        units: "%"
        accuracy: 0.1
        default: 70
        value: 70
```

按腔室展开的语义细节：

- 腔室清单来自 Control 片段的根标签；`--chamber` 限定同样生效（未选中的腔室不展开）。
- 路径里可用 `{Chamber}`（**保留绑定**，见 §6）或该腔室根的 class 名；`{}` 与 `${}` 同解。
- 判定依据是**路径里有没有 `{`**：含 `{` 即按腔室展开，不含则全局执行一次。
- 某腔室**没有**该文件时只打印 `! 跳过：文件不存在` 并继续（不同腔室未必都有该文件），
  不会中止其余腔室；字面量路径缺失文件则仍报错。
- 与腔室步骤一致：同一腔室名若在多个片段里出现（如 `Control_Ch1` 与 `Interlock_Ch1` 的根
  都是 `<Ch1>`），该步也会随之执行多次；所有原语幂等，重复执行是 no-op。

### 7.15 `new-file` —— 新建文件

升级里常有"目标版本多出一整份新文件"（例如新腔室/新功能对应的 `Setup/*.xml`）。这类文件
在旧配置里**完全不存在**，`file:` 步骤（要求文件可读）无从下手，因此提供 `new-file`：

| 字段 | 说明 |
|------|------|
| `new-file` | 相对 `config/` 的新文件路径（step 级字段，与 `file:` 同族）。 |
| `content` | 新文件的**原文**（逐字写入：保留 CRLF/LF、是否带行尾换行、实体书写）。 |

语义与幂等：

- 文件**不存在** → 按 `content` 逐字新建（自动建父目录），打印 `+ 已新建`；
- 文件**已存在** → `- 已存在，保持原样(no-op)`，**绝不覆盖**（与外科式补丁一致）；
- `plan` 模式只打印 `+ 待新建`，不落盘。

`new-file` 同样属"文件级步骤"，且路径规则与 `file:` 相同：不含占位符时全局执行一次；
含占位符时按腔室展开（如 `new-file: Setup/New_${Chamber}.xml` 为每个腔室新建一份）。
`content` 始终**按字面写入**，不做占位符替换。

```yaml
steps:
  - name: 新建 Setup/ProcessDataStableTime_Ch1.xml
    new-file: Setup/ProcessDataStableTime_Ch1.xml
    content: |
      <?xml version="1.0" encoding="UTF-8"?>
      <Ch1ProcessDataStableTime comments="">
        <Param name="IG" dataObject="/SETUP/Ch1/ProcessDataStableTime/IGStableTime" type="D" min="0" max="10" units="s" accuracy="0.01" default="1" />
        <Option index="1">
          <Value paramName="IG">1</Value>
        </Option>
      </Ch1ProcessDataStableTime>
```

> 提示：`content` 不经过渲染器、也不做占位符替换；需要按腔室生成多份时，各写一个步骤
> （生成器会把目标文件原文分别内联）。若把 `content` 写成 YAML 的 `|` 块标量，缩进会被
> 保留，建议用 `|-` 或注意首行缩进。

### 7.16 `add-setup` —— Setup 的 Param/Value 成对追加

Setup 文件的参数分两处写：根元素下的 `<Param .../>` 声明，与 `<Option>` 里对应的
`<Value paramName="...">取值</Value>`。二者必须**按下标一一同名**（见 `setupcheck`），
手工分两处 `add-element` 容易漏一侧或错位。`add-setup` 把"一个参数"作为**一次声明**成对追加：

- `<Param name=... attrs.../>` 追加到 `<Param>` 序列**末尾**（即首个 `<Option>` 之前）；
- `<Value paramName=...>value</Value>` 追加到该 `<Option>` 的**末尾**（即 `</Option>` 之前）。

| 字段 | 说明 |
|------|------|
| `param` | 参数名：`<Param name>` 与 `<Value paramName>`（必填）。判重键。 |
| `dataObject` / `type` / `min` / `max` / `maxLength` / `descriptorList` / `units` / `accuracy` / `default` | `<Param>` 属性的便捷写法。按设备 Setup 的规范顺序渲染（`dataObject → type → min → max → maxLength → descriptorList → units → accuracy → default`），**未配置的项跳过**；写 `null` 或空串则渲染成空值属性。 |
| `attrs` | 其它属性（保留书写顺序）；与上面的便捷字段**同名时以 `attrs` 为准**，且仍落在规范顺序的位置。 |
| `value` | `<Value>` 的文本（支持占位符）；缺省/`null` → 渲染成成对空标签 `<Value paramName="X"></Value>`。 |

判重（幂等）：两侧各自按 `name` / `paramName` 判重——已存在的一侧不动，**只补缺失的一侧**；
两侧都在即 no-op。找不到 `<Option>` 时只追加 `<Param>` 并在日志里以 `!` 告警。

```yaml
- name: Setup/Setup_Ch1.xml 升级
  file: Setup/Setup_Ch1.xml
  add-setup:
    - param: SourceDCCurrentMax
      dataObject: /SETUP/Control/Ch1/Source/SourceDC/CurrentOutputMaxPercent
      type: D
      min: 0
      max: 100
      units: "%"
      accuracy: 0.1
      default: 70
      value: 70
```

生成（`<Param>` 落在 Param 序列末尾、`<Value>` 落在 Option 末尾）：

```xml
<Param name="SourceDCCurrentMax" dataObject="/SETUP/Control/Ch1/Source/SourceDC/CurrentOutputMaxPercent" type="D" min="0" max="100" units="%" accuracy="0.1" default="70"/>
...
<Option index="1">
  ...
  <Value paramName="SourceDCCurrentMax">70</Value>
</Option>
```

> anchor 可省略：`add-setup` 作用在文件根元素上，因此与 `file:` 配合即可；若写了 `anchor`
> 则相对文件根解析（空 `anchor` = 根元素本身）。

### 7.17 `rename-node` —— 重命名标签 / 增改属性

把一个**已存在**元素的标签改名（开标签与闭标签同步），属性、子节点与文本原样保留：

```xml
<Edge class="FuncEdge" type="instance">
    <setValve type="method">111</setValve>
</Edge>
```
```yaml
- name: setValve 方法改名
  anchor: Edge
  rename-node:
    - { tag: setValve, to: setGasInValve }   # 命中子元素 → 只改标签名
```
```xml
<Edge class="FuncEdge" type="instance">
    <setGasInValve type="method">111</setGasInValve>
</Edge>
```

| 字段 | 说明 |
|------|------|
| `tag` / `child` / `attr` / `value` / `has` | 选择器，口径同 §7.10/§7.11（`child` 再下沉一层，`has` 要求含指定直接子元素）。在 anchor 的**整棵子树**里选，可命中多个，全部处理。 |
| `to` | 新标签名（支持占位符）；开/闭标签同步改，自闭合只改开标签。省略=不改标签。 |
| `attrs` | 可选：要设置/新增的属性（保留书写顺序，值支持占位符；已有同名属性则原位替换其值）。 |

**选择器全空**（`tag`/`child`/`attr`/`value`/`has` 都没写）时，作用对象是 **anchor 自身**——
用于直接改 anchor 的标签名，或给它补属性（`set-attr` 只能改子孙，补 anchor 自身属性时用它）：

```yaml
- name: 给 anchor 自身补 class/type
  anchor: Edge
  rename-node:
    - attrs: { class: FuncEdge, type: instance }   # 不改标签，只加属性
```

判重（幂等）：标签已是 `to` 且 `attrs` 都已是目标值 → no-op；`to`/`attrs` 都省略 → no-op。
本原语在 `set-text`/`set-attr`/`remove-node` **之前**执行，所以同一步里可以"先改名、再按新名字
改写文本/属性"。

> 注意：把 anchor 自身的标签改掉后，下一次运行时该 `anchor` 路径已不再匹配（标签已变），
> 引擎会打印"anchor 无匹配"跳过——结果仍是幂等的（不再有改动）。需要重复定位时，建议
> 把 anchor 放在**父节点**上，用 `tag: 旧名` 选择要改名的子元素。

---

## 8. 执行顺序与幂等

- **腔室级**：`--chamber` 缺省=全部腔室；限定时逐腔室执行。某腔室 anchor 无匹配 / `require`
  不满足 → 自动跳过并打印原因。
- **文件级**：路径含占位符的 `file:`/`new-file:` 步骤在该腔室循环内**逐腔室执行**（顺序为
  腔室步骤之后）；路径为字面量的在全部腔室之后按声明顺序各执行一次。
- **step 级**：按 `steps` 顺序，后一步可见前一步在内存树上挂的新节点（跨步引用）。
- **同一 step 内动作顺序**（固定，与书写顺序无关）：
  `add-node` → `add-data` → `add-element` → `add-setup` → `add-xml` → `rename-node` →
  `set-text`/`set-attr`/`remove-node` → `wrap` → `uncomment` → `add-blank` → `add-comment` →
  `add-method` → `remove-method` → `add-io`。
- **幂等**：每个原语先判重，已达目标即 no-op（`-`），不产生字节编辑；重复运行安全。

`plan` 与 `apply` 打印**完全一致**的语义 diff，区别只在 `apply` 会落盘。每行前缀：
`+` = 有改动，`-` = 幂等 no-op，`!` = 告警（如 `include-entity` 未匹配、`before-method` 未找到）。

---

## 9. `Bd: auto` 板号推断

`add-io` / `add-data` 的 `Bd` 写 `auto` 时按下列顺序推断：

1. 取本 anchor 节点下**已有 IO 点位**的 `<Bd>` 文本（首个非空）——沿用同处其它点位的板号；
2. 否则按**腔室号**推断：`ChN` → `N*100`（`Ch1`→`100`、`Ch2`→`200`……）。

---

## 10. 最小完整示例

```yaml
id: demo
description: 建对象 → 配方法 → 条件建 IO 点位
version: 1
steps:
  - name: 建 IonGauge 对象
    anchor: /Control/{ITO}
    add-node:
      - tag: IonGauge
        class: PhyGauge
        attrs: { type: instance, alias: "/Control/${ITO}Exports/IonGauge" }
        include-entity: "Simulated*{ITO}"

  - name: 配置 IG 规
    anchor: /Control/{ITO}/{PhyGauge}
    where: { tag-glob: "IonGauge" }
    add-method:
      - name: setOnOffVp
        value: "/IO/${ITO}/IG/OnOffDI"
      - name: enableModeSwitch

  - name: 建 IG IO 点位
    anchor: /IO/${ITO}/IG
    add-io:
      - name: OnOffDI
        attrs: { dataType: "I", accessMode: "R", simulated: "", alias: "/IO/${ITO}Exports/IG_OnOffDI" }
        include-entity: "Simulated*{ITO}"
        Bd: auto
        Ch: 4101
        DescriptorList:
          - { name: OFF, value: 0 }
          - { name: ON,  value: 1 }
```

参考真实样例：`features/ig-auto-close.yaml`、`features/add-pedcurpos-dataex.yaml`、
`features/temp-diff.yaml`。
