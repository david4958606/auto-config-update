# auto-update-tool (demo)

对设备配置做**幂等语义补丁**的离线升级工具:把 XML 解析成树,按 `feature.yaml` 做**声明式补丁**,再**外科式回写**——只动插入/删除点,其余字节逐字保留。设计背景见 [PLAN.md](PLAN.md)。

## 构建

依赖已 vendor,可离线纯静态编译(见 [Makefile](Makefile)):

```bash
make          # 本机 + linux/386 静态 + windows/amd64
make native   # 本机二进制 -> auto-config-update
make cross    # linux/386 静态 -> auto-config-update-32(CentOS6/内核2.6.32)
make win64    # windows/amd64 -> auto-config-update.exe
```

约束:工具链须为 Go **1.23.x**;`CGO_ENABLED=0` 纯静态、`GOPROXY=off` 离线可构建。

## 运行

`config/`、`features/` 按**当前工作目录**解析,需在含二者的目录下运行:

```bash
./auto-config-update plan  --feature features/ig-auto-close.yaml    # dry-run,不写盘
./auto-config-update apply --feature features/ig-auto-close.yaml    # 写盘
./auto-config-update apply --feature features/... --chamber Ch1 Ch4 # 限定腔室(缺省=全部)
./auto-config-update check                                          # 校验 Setup 的 Param/Value 一一对应
./auto-config-update switch true                                    # 把 config/*Simulated* 的模拟开关切到 true
./auto-config-update switch false                                   # ... 切到 false
```

- `switch <true|false>` 批量改写 `config/*Simulated*` 里
  `<setSimulated type="method">true|false</setSimulated>` 的取值，替代手工的
  `sed -i 's/\bfalse\b/true/g' config/*Simulated*`：只动该元素的文本(属性与其余字节逐字保留)、
  已是目标值的文件**不写盘**(幂等);某文件找不到 `setSimulated` 或文本里没有 `true`/`false`
  时打印 `!` 告警并以**非零退出码**结束。目标值缺失/非法返回退出码 2。

- `--feature` 可简写 `-f`;`--chamber` 可简写 `-c`,均支持连续多值(`-c Ch1 Ch2 Ch3`)。
- `plan` / `apply` 打印一致的**语义 diff**,区别只在 `apply` 落盘。
- 行前缀:`+` 有改动、`-` 已达目标(幂等 no-op)、`!` 告警;每个声明动作都出一行。
- **一致性闸门**:`apply` 结束会对 `config/Setup/*.xml` 做自检——`<Param name>` 与
  `<Option>/<Value paramName>` 必须**按下标一一同名**(设备把两者当并行数组读取,顺序即语义);
  数量不符、笔误、重复、错位/顺序不同都打印 `!!` 并以**非零退出码**报错(`--no-verify` 可跳过)。
  也可单独跑 `check` 作为上线前的硬闸门。
- 原语速查见 [doc/feature-primitives.md](doc/feature-primitives.md)。

## Feature 怎么写

一个功能 = 一份 `features/<id>.yaml`,核心是 `steps`:按顺序执行,后一步可用前一步的产物。

```yaml
id: ig-auto-close
version: 1
steps:
  - name: 建 IonGauge 对象
    anchor: ITO
    add-node:
      - tag: IonGauge
        class: PhyGauge
        attrs: { type: instance }
        include-entity: "Simulated*{ITO}"   # 解析成 &Simulated_Ch1; 内嵌为末子节点

  - name: 配置 IG 规
    anchor: ITO/PhyGauge
    where: { tag-glob: "IonGauge" }          # 同类多实例时按标签精确筛(可选)
    add-method:
      - { name: setOnOffSp, value: "/IO/{ITO}/IG/OnOffDI" }
      - { name: enableModeSwitch }           # 无 value → 自闭合

  - name: 建 IG IO 点位
    anchor: IG                               # 域前缀缺省 Control;IO/IOBridge 路由到对应片段
    require:
      - exist: { IGBoard: "/IO/{Ch}/IG" }    # 守卫+绑定;不满足则跳过该实例
    add-io:
      - name: OnOffDI
        Bd: auto                             # 按本节点既有点位/腔室号推断板号
        Ch: 0
        DescriptorList: [ { name: OFF, value: 0 }, { name: ON, value: 1 } ]
```

**核心字段**

| 字段 | 含义 |
|------|------|
| `anchor` | **class 路径**,逐层 descendant 定位(靠 class,不靠实例名)。首段可选域前缀 `Control`/`IO`/`IOBridge`(缺省 `Control`);段写法 `{X}`/裸名=按 class、`${X}`=按标签名。 |
| `where` | leaf 多实例时筛子集 + 插入定位:`tag-glob`/`attr` 筛选;`before-method: {name}` 把本步方法插到该既有方法之前。 |
| `require` / `bind` | `require.exist: {var: 路径}` 守卫+绑定,路径解析不到则跳过;`bind` 纯绑定不校验。 |
| `add-node` | 建对象节点;可带 `attrs`、`include-entity`(内嵌声明的实体引用)。 |
| `add-method` / `remove-method` | 加/删方法调用;有 `value` 渲染 `<name type="method">值</name>`,无 `value` 自闭合。删按 名+值 匹配。 |
| `add-io` / `add-data` | 建 IO/数据点位,按 `name` 判重。子元素按序 `Bd`(`auto`=推断板号)/`Ch`/`Min`/`Max`/`Accuracy`/`DescriptorList`/`Unit`;`add-data` 首属性固定 `type="data"`。 |
| `add-blank` / `add-comment` | 在方法块前插入空行 / 段注释。`add-blank: N` 插 N 行空行;`add-comment` 是注释原文列表(须自带 `<!-- -->`)。二者不进解析树,按 anchor 原始字节区间判重(已存在即 no-op)。 |
| `add-element` | 建普通元素(如 Setup 的 `<Param>`/`<Value>`、`<FileSize>`、`<spare>`),可 `before`/`after` 定位。 |
| `add-xml` | 插入一段**内联 XML 片段**(整棵新对象子树),逐字保留 `&amp;&amp;` 等实体书写。 |
| `set-text` / `set-attr` | 改写已存在元素的文本 / 属性(按 `tag`+`attr`+可选 `old` 定位)。 |
| `remove-node` | 删除已存在元素(含子树);`has` 子条件可区分同名不同内容的节点。 |
| `wrap` / `uncomment` | 用任意 `open`/`close` 包裹一段节点区间(注释掉 / CDATA 化) / 放开(或删除)注释块。 |
| `file:`(step 级) | 让某个 step 直接作用于 `config/<file>`(Setup、SysLog、master 等非片段文件)。 |
| `new-file:`(step 级) | 目标版本多出的**整份新文件**:文件不存在时按 `content` 逐字新建,已存在即 no-op(绝不覆盖)。 |

**占位符**:两种写法。**anchor 段**里 `{X}`=按 class 匹配并把 `X` 绑定为该实例标签,`${X}`=按"已绑定的标签名"匹配(如腔室 class 绑定 `{ITO}`=Ch1,IOBridge 那层没有 class 只能写 `${ITO}`)。**取值字段**(attr/value/xml/text/old/name/child/where/open/close/find 等)里 `{X}` 与 `${X}` **同解**,都替换为标签/变量值,且**所有原语都支持**。`add-node` 建出的对象登记 `{标签}`→`./标签` 供后续步骤引用;`require`/`bind` 变量同理。另见 [doc/feature-primitives.md](doc/feature-primitives.md) §6。

## 它如何应对真实配置

- **实例名各异**(`IonGauge`/`Gauge01`):靠 class 定位,实例名无关。
- **同 class 多实例**:leaf fan-out,每个实例各执行一次,绝不静默取第一个。
- **不适用的腔室**(class 不匹配 / `require` 不满足):自动跳过并打印原因。
- **XML 外部实体**(`&Simulated_Ch1;`):加载时声明但不展开,回写原样保留;真名从 `Control_config.xml` 解析。
- **幂等**:重复运行为 no-op;**外科式写盘**只动改动点,注释/缩进/空标签风格逐字不变。

## 配置结构

```
config/
  Control/Control_config.xml   # master:<!ENTITY ... SYSTEM ...> 把片段拼成逻辑 <Control>
  Control/Control_Ch1 ...      # 腔室片段(无扩展名,内部可引用 &Simulated_Ch1;)
  IO_config.xml                # master:拼成逻辑 <IO>
  IOBridge/IO_Ch1 ...          # IO 片段(IO_Motor 一文件可含 <Ch1>/<Ch4> 多段)
  Simulated_Ch1                # 共享实体体
```

逻辑路径 `/IO/Ch1/...`、`/Control/Ch1/...` 只看**根腔室标签**,不看文件名。片段逐份原地回写,master 与其它片段不动。

## 模块

- `main.go` —— CLI 入口(`plan` / `apply` / `check` / `switch`)
- `internal/`
  - `xmldoc` —— 按字节偏移解析 XML、实体容忍(声明不展开)、外科回写的编辑记录
  - `config` —— master+实体装配、逻辑 IO/Control 视图、实体真名解析
  - `anchor` —— class 路径定位 + 同类多实例 fan-out + `where` 筛选
  - `ops` —— 幂等原语:`add-node`/`add-method`/`remove-method`/`add-io`/`add-data`/`add-blank`/`add-comment`/`add-entity-ref`/`add-element`/`add-xml`/`set-text`/`set-attr`/`remove-node`/`wrap`/`uncomment`
  - `xmlcmp` —— 语义比对(忽略空白/注释/属性序,比对元素树+实体+停用区),用于升级验收
  - `feature` —— 功能 YAML 加载 + `require`/`bind` 绑定
  - `setupcheck` —— `Setup/*.xml` 的 Param/Value 按下标一一对应校验
  - `simswitch` —— 批量切换 `config/*Simulated*` 的 `<setSimulated>` 开关
  - `splice` —— 外科式字节拼接写盘
  - `engine` —— steps 编排:逐腔室、逐步、逐实例执行并落盘
- `features/*.yaml` —— 功能定义 · `config/` —— 演示夹具 · `Makefile` —— 出包脚本
- `legacy/` —— Python 原版,保留作参考

> 移植计划见 [GO_PORT_PLAN.md](GO_PORT_PLAN.md)。回归测试见 `internal/engine/engine_test.go` 与 `testdata/golden/`(golden 由 Go 引擎输出生成,`go test ./internal/engine -update-golden` 可重生成)。

## 用真实配置验证:config_old → config 升级

`example-config/` 下是一份**真实配置**的新旧两版(`config_old` = 升级前,`config` = 人工改好的升级后)。
仓库里的 `features/upgrade-*.yaml` 就是这次升级的完整声明:

| feature | 覆盖 |
|---------|------|
| `features/upgrade-files.yaml` | `Setup/*.xml`、`SysLog_config.xml`、`Control/Control_config.xml`(由 `hack/gen_upgrade_files.py` 生成) |
| `features/upgrade-io.yaml` | IO 片段量程/板号、`IO_Platform` 段停用、`IO_Facility` 新点位、`Driver_Facility` 通道模式 |
| `features/upgrade-control.yaml` | 加热器温差、EzZone 校准、互锁/报警、机器人安全互锁(由 `hack/gen_upgrade_control.py` 依结构差异生成) |

```bash
hack/run-upgrade.sh /tmp/up apply          # 拷 config_old → /tmp/up/config 并按序升级
go test ./internal/engine -run TestUpgradeExampleConfig   # 语义比对 + 幂等校验
```

`TestUpgradeExampleConfig` 会把 `config_old` 当输入、依次 apply 三个 feature,再用 `internal/xmlcmp`
与 `config` 做**语义比对**:忽略缩进/空行/属性书写顺序/`<x/>` 与 `<x></x>` 之别,但严格比对元素层级、
属性、叶子文本、实体引用,以及"哪些块被注释掉 / CDATA 化"。Setup 另有一项专项校验:`<Param name>`
与 `<Option>/<Value paramName>` 两条序列必须**数量相同、逐项同名同序**,并与目标一致。随后再 apply
一遍验证**幂等**。
设计与原语取舍见 [doc/config-upgrade-design.md](doc/config-upgrade-design.md)。

> 说明:该迁移把目标里"注释掉/ CDATA 包住"的块实现为**删除**——注释内容不参与解析,两者语义等价。
> 需要保留原文时可改用 `wrap`(本仓库对 `IO_Platform` 的 V6DO、CDATA 段就是这么做的)。

## 用第二份真实配置验证:example-16196(config_old → config)

`example-16196/` 下是另一台设备的新旧两版配置,用来复验原语覆盖面。与上一份不同,这次目标版本
**多出 10 个全新的 `Setup/*.xml`**(`GasFlowCompens_Ch*`、`ProcessDataStableTime_*`),需要
**新建文件**能力——由此新增了 step 级原语 `new-file`(见
[doc/feature-primitives.md](doc/feature-primitives.md) §7.15)。

| feature | 覆盖 | 生成方式 |
|---------|------|----------|
| `features/upgrade-16196-setup.yaml` | `Setup/*.xml`(含 10 个新文件)、`SysLog_config.xml` | `hack/gen_upgrade_16196.py` |
| `features/upgrade-16196-io.yaml` | `IOBridge/*`(量程/别名/描述子、停用节点) | 同上 |
| `features/upgrade-16196-control.yaml` | `Control/*`(补偿器、PMacro、稳定时间、互锁) | 同上 |

```bash
hack/run-upgrade-16196.sh /tmp/up16196 apply          # 拷 config_old → /tmp/up16196/config 并按序升级
go test ./internal/engine -run TestUpgradeExample16196 # 语义比对 + Setup 对应 + 幂等
```

`hack/gen_upgrade_16196.py` 用与 `gen_upgrade_control.py` 同构的带偏移分词器(不用 lxml,避免
丢掉 `&amp;&amp;`),把新旧树做**单调对齐**后生成静态 feature:`set-text`/`set-attr`/`remove-node`
落到具体节点,新增节点用 `add-xml` 按 `before`/`after` 定位,整体缺失的文件用 `new-file` 新建。
`Recipe/` 下新增 recipe 与对应 recipe 文件按需求不处理。

生成器还会把**逐腔室重复**的改动归并成"一份声明、逐腔室替换腔室名"的腔室级步骤(写法同
`features/add-pedcurpos-dataex.yaml`):同 class 的腔室用 `anchor: <Class>/…` + `${<Class>}`
(如 `PVD/ProcessLogger` 覆盖 Ch1/Ch2/Ch5/Ch6);根没有 class 的片段(Interlock)用保留占位符
`${Chamber}`。因此不再出现 `anchor: Ch1/…` / `anchor: Ch2/…` 的复制粘贴。

验收结果:三份 feature 依次 apply 后,与目标 `config/` 的语义差异为 **0 处**(只剩若干
"同级子节点顺序不同"的 `order`,按既有口径视为语义无关);`Setup/*.xml` 的
`<Param name>` 与 `<Value paramName>` **按下标**一一同名;二次 apply **逐字节幂等**;
10 个新文件与目标**逐字节一致**。

> 目标 `Setup/GasFlowCompens_Ch*.xml` 自身有一处笔误:`<Param name="AlONGasFlowPieceCompens">`
> 与 `<Value paramName="AlOGasFlowPieceCompens">` 不同名。工具**按目标保真**复现该笔误,
> 同时由一致性校验**检出并报错**:`auto-config-update check` 会打印
> `!! Setup/GasFlowCompens_ChN.xml: 第 1 项 Param=AlONGasFlowPieceCompens 与 Value=AlOGasFlowPieceCompens 不同名`
> (以及对应的 orphan-param / orphan-value),并以**非零退出码**结束;`apply` 默认也会在结束时
> 跑同样的自检。设备把 `<Param>`/`<Value>` 当**并行数组按下标读取**,所以顺序不同同样是 error。
> 测试 `checkSetup16196Issues` 断言产物与目标触发**完全相同**的问题集合。
