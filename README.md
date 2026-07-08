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
```

- `plan` / `apply` 打印一致的**语义 diff**,区别只在 `apply` 落盘。
- 行前缀:`+` 有改动、`-` 已达目标(幂等 no-op)、`!` 告警;每个声明动作都出一行。
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

**占位符**:`{类名}` = anchor 路径上该 class 命中的实例标签名(逐实例绑定);`add-node` 建出的对象登记 `{标签}`→`./标签` 供后续步骤引用;`require`/`bind` 变量同理。

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

- `main.go` —— CLI 入口(`plan` / `apply`)
- `internal/`
  - `xmldoc` —— 按字节偏移解析 XML、实体容忍(声明不展开)、外科回写的编辑记录
  - `config` —— master+实体装配、逻辑 IO/Control 视图、实体真名解析
  - `anchor` —— class 路径定位 + 同类多实例 fan-out + `where` 筛选
  - `ops` —— 幂等原语:`add-node`/`add-method`/`remove-method`/`add-io`/`add-data`/`add-entity-ref`
  - `feature` —— 功能 YAML 加载 + `require`/`bind` 绑定
  - `splice` —— 外科式字节拼接写盘
  - `engine` —— steps 编排:逐腔室、逐步、逐实例执行并落盘
- `features/*.yaml` —— 功能定义 · `config/` —— 演示夹具 · `Makefile` —— 出包脚本
- `legacy/` —— Python 原版,保留作参考

> 移植计划见 [GO_PORT_PLAN.md](GO_PORT_PLAN.md)。回归测试见 `internal/engine/engine_test.go` 与 `testdata/golden/`(golden 由 Go 引擎输出生成)。
