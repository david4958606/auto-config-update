# auto-update-tool (demo)

对设备配置做**幂等语义补丁**的离线升级工具。把"改文本 + 批量替换腔室号"这件本质上
"结构化、有语义"的事，变成对解析后 XML 树的**声明式补丁**，并**外科式回写**——只动
插入/删除点，其余字节逐字保留。设计背景见 [PLAN.md](PLAN.md)。

## 运行

```bash
uv run addex.py plan  --feature features/ig-auto-close.yaml            # dry-run，不写盘
uv run addex.py apply --feature features/ig-auto-close.yaml            # 写盘
uv run addex.py plan  --feature features/... --chamber Ch1 --chamber Ch4  # 指定腔室
```

- `plan` 与 `apply` 打印完全一致的**语义 diff**，区别只在 `apply` 会落盘。
- 每行前缀：`✎` = 有改动，`·` = 已达目标(幂等 no-op)——每个声明的动作都出一行，不静默省略。

## Feature 怎么写（`steps` 有序列表）

一个功能 = 一份 `features/<id>.yaml`，核心是 `steps`：按顺序执行，后一步可依赖前一步的产物。

```yaml
id: ig-auto-close
description: IG 规在工艺中自动关闭灯丝功能
version: 1

steps:
  # 步1：在 ITO 腔室下建对象节点，并内嵌 Control_config.xml 声明的仿真实体
  - name: 建 IonGauge 对象
    anchor: ITO
    add-node:
      - tag: IonGauge
        class: PhyGauge
        attrs: { type: instance, alias: "/Control/{ITO}Exports/IonGauge" }
        include-entity: "Simulated*{ITO}"     # 解析成 &Simulated_Ch1; 内嵌为最后一个子节点

  # 步2：给 IG 规接信号点；leaf 类多实例(如多个 PhyGauge)会各执行一次
  - name: 配置 IG 规
    anchor: ITO/PhyGauge
    where: { tag-glob: "IonGauge" }           # 同类多实例时按标签精确筛子集(可选)
    add-method:
      - name: setOnOffSp
        value: "/IO/{ITO}/IG/OnOffDI"
      - name: enableModeSwitch                # 无 value → 自闭合 <enableModeSwitch type="method"/>

  # 步3：跨步引用步1建的对象
  - name: 在 ITO 中添加相关方法
    anchor: ITO
    add-method:
      - name: setIonGauge
        value: "{IonGauge}"                   # {IonGauge} 自动成 ./IonGauge

  # 步4：前置条件满足才装
  - name: 添加 setPgValve
    anchor: ITO
    require:
      - exist: { PgValve: "/Control/{ITO}/Vacuum/PgValve" }   # 守卫+绑定；不满足则跳过该实例
    add-method:
      - name: setPgValve
        value: "./Vacuum/PgValve"
```

### 每步的字段

| 字段 | 含义 |
|------|------|
| `anchor` | 一条 **class 路径**(如 `ITO/PhyGauge`)，逐层 descendant 定位。**靠 class，不靠标签/实例名**。 |
| `where` | leaf 命中同类多实例时筛子集：`tag-glob:` 按标签 glob、`attr: {k: v}` 按属性。缺省=全部实例。 |
| `require` | 守卫 + 绑定。`exist: {var: 路径}` —— 逻辑路径(`/IO/...` 或 `/Control/...`)必须解析得到，否则**跳过**；命中则把 `var` 绑成该路径。 |
| `bind` | 纯绑定，不做存在性要求(如删除项引用的路径不必仍存在)。 |
| `add-node` | 建对象节点。可带 `attrs`(值支持占位符) 和 `include-entity`(内嵌声明的实体引用)。 |
| `add-method` | 加方法调用。有 `value` → `<name type="method">值</name>`；无 `value` → 自闭合。 |
| `remove-method` | 删匹配 (名字+值) 的方法调用。 |

### 占位符 / 变量

- `{类名}` —— anchor 路径上该 class 匹配到的**实例标签名**，逐实例自动绑定(`PhyGauge`→`Gauge01`)。
- `add-node` 建出的对象会登记 `{标签}` → `./标签`，供**后续步骤**引用。
- `require`/`bind` 绑定的变量。
- `include-entity` 的 glob 也先做占位符替换，再去 `Control_config.xml` 的实体声明里匹配真名。

## 它如何应对"各台不一样 / 真实配置"

| 情况 | 工具怎么处理 |
|------|------|
| 节点实例名各异(`IonGauge` / `Gauge01`) | 靠 `class` 定位，**实例名无关** |
| 同一 class 有**多个**实例(如多个 `PhyGauge`) | leaf **fan-out**：每个实例各执行一次，`{类名}` 逐实例绑标签，绝不静默取第一个 |
| 腔室号不同 | `{ITO}` 等自动取当前腔室标签，路径自洽 |
| 某腔室不适用(class 不匹配 / `require` 不满足) | **自动跳过**，并打印原因 |
| 配置用 XML 外部实体(`&Simulated_Ch1;`)拼装 | 加载时**声明但不展开**，回写**原样保留**；实体真名从 `Control_config.xml` 解析(`Simulated_Ch1`/`SimulatedFlag_Ch1`) |
| 评审要看清改了什么 | **外科式写盘**：只动插入/删除点，空标签风格/注释/缩进逐字不变 |
| 重复运行 | **幂等** no-op |

## 配置结构（master + 实体装配）

```
config/
  Control/Control_config.xml   # master：用 <!ENTITY ... SYSTEM ...> 把片段拼成逻辑 <Control>
  Control/Control_Ch1 ...      # 腔室片段(无扩展名，是"实体体"，内部可再引用 &Simulated_Ch1;)
  IO_config.xml                # master：拼成逻辑 <IO>
  IOBridge/IO_Ch1 ...          # IO 片段(IO_Motor 一个文件可含多个腔室段 <Ch1>/<Ch4>)
  Simulated_Ch1                # 共享实体体
```

逻辑路径 `/IO/Ch1/...`、`/Control/Ch1/...` 的解析只看**根腔室标签**，不看文件名——
片段分片、`IO_Motor` 这类多段/自定义命名文件都被透明纳入。Control 片段**逐份原地回写**，
master 与其它片段不动。

## 目录

- `addex.py` —— 瘦入口(`uv run addex.py ...`)
- `src/` —— 引擎(分层)：
  - `model` 解析/寻址：master+实体装配、逻辑 IO/Control 视图、实体保留式片段读写
  - `anchor` class 路径定位 + 同类多实例 fan-out + `where` 筛选
  - `ops` 幂等原语：`add_node` / `add_method` / `remove_method` / `add_entity_ref`
  - `feature` 功能 YAML 加载 + `require`/`bind` 变量绑定
  - `splice` 外科式文本拼接写盘
  - `engine` steps 编排：逐腔室、逐步、逐实例执行并落盘
  - `cli` `plan` / `apply`
- `features/*.yaml` —— 功能定义：`ig-auto-close`(建对象+配信号+跨步引用+条件)、
  `add-pedcurpos-dataex`(加/删日志项)
- `config/` —— 演示夹具(Ch1 是较完整的真实 ITO 腔室)

> 注：`models/`、`machines/`、`features/turbopump-setspeed.yaml` 是已移除的旧引擎
> `upgrade.py` 的配套(分层覆盖那套写法)，新引擎 `addex.py` 不再使用，保留仅作参考。
