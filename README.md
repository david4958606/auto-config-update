# Auto Config Update

对设备配置做**幂等语义补丁**的离线升级工具:把 XML 解析成树,按 `feature.yaml` 做**声明式补丁**,再**外科式回写**——只动插入/删除点,其余字节逐字保留。设计背景见 [PLAN.md](PLAN.md)。

## 构建

依赖已 vendor,可离线纯静态编译(见 [Makefile](Makefile)):

```bash
make          # 本机 + linux/386 静态 + windows/amd64
make amd64    # 本机二进制 -> auto-config-update
make i386     # linux/386 静态 -> auto-config-update-32(CentOS6/内核2.6.32)
make win64    # windows/amd64 -> auto-config-update.exe
make tar      # 打包二进制、features、doc、README、run.sh、prepare-onsite.sh 及已生成的一键脚本
```

约束:工具链须为 Go **1.23.x**;`CGO_ENABLED=0` 纯静态、`GOPROXY=off` 离线可构建。

## 运行

`features/` 按**当前工作目录**解析；`config/` 缺省取**可执行文件同目录**下的 `config`，
也可用 `--config <dir>` 指定（相对路径同样以可执行文件所在目录为基准，绝对路径原样使用）：

```bash
./auto-config-update plan  --feature features/ig-auto-close.yaml    # dry-run,不写盘
./auto-config-update apply --feature features/ig-auto-close.yaml    # 写盘
./auto-config-update apply --feature features/... --chamber Ch1 Ch4 # 限定腔室(缺省=全部)
./auto-config-update apply --feature features/... --config config-14346   # 改用 exe 同目录的 config-14346
./auto-config-update check                                          # 校验 Setup 的 Param/Value 一一对应
./auto-config-update switch true                                    # 把 config/*Simulated* 的模拟开关切到 true
./auto-config-update switch false                                   # ... 切到 false
./auto-config-update switch true --config config-14346              # flag 写在位置参数前后均可
```

- `--config <dir>` 指定 config 文件夹：缺省为**可执行文件同目录**的 `config`；传相对路径时以
  可执行文件所在目录为基准（`--config config-14346` = 该目录下的 `config-14346/`），传绝对路径则原样使用。
  该 flag 对 `plan` / `apply` / `check` / `switch` 全部生效。

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
  属性名笔误同样拦下:XML 属性名区分大小写,`<Value paramname="X">`(或 `<Param Name="X">`)会让该
  取值在设备侧**整个失效**,自检单独报 `attr-name` / `attr-missing` 并指名道姓。
  也可单独跑 `check` 作为上线前的硬闸门。
- 原语速查见 [doc/feature-primitives.md](doc/feature-primitives.md)。

### 工程师预置 · 现场一键执行（推荐）

现场操作人员直接在功能/腔室列表里选择容易选错。推荐流程：工程师先在办公机用
`prepare-onsite.sh` 选好功能与腔室，生成一个**只含“应用升级配置 / 退出”两个选项**的
现场脚本；现场人员按一次 `1` 即可执行，功能、腔室、顺序都不可再改。

**第一步：工程师生成**（在含 `features/` 的交付目录运行）

```bash
bash prepare-onsite.sh                             # 生成 updateConfig-<日期>.sh
bash prepare-onsite.sh --config config-14346       # 预置非缺省 config 目录
bash prepare-onsite.sh -o updateConfig-site-a.sh     # 指定输出文件（相对当前目录）
```

按编号勾选功能（勾选后进入腔室页：编号切换、回车保存，全不选 = 全部腔室），输入
`g` 生成。生成前会再次确认执行顺序、腔室、生成人/工位/备注，并把每个功能 YAML 的
`sha256`、配置目录和方案指纹写进生成的脚本。输出名以 `updateConfig-` 开头时，`make tar`
会自动把它一起打包；自定义输出名需手动放进交付包。

**第二步：部署到现场**（以下内容放在同一目录，`features/` 保留子目录结构）

```
auto-config-update-32
features/
config/                 # 或生成时 --config 指定的目录
updateConfig-<日期>.sh
```

**第三步：现场执行**

```bash
bash updateConfig-<日期>.sh
```

界面只有两个选项（与工程师预置的方案摘要一并显示，供操作人员核对）：

```
===== 自动升级脚本 =====
    1) 应用升级配置
    2) 退出
========================
```

选 `1` 立即按预置顺序执行，选 `2` 直接退出。开始前会：

- 获取本目录的升级锁（`.updateConfig.lock`），同一目录同时只允许一个升级进程，避免并行写盘；
- 校验 `auto-config-update-32`、`config/` 和每个功能 YAML 是否存在，功能 YAML 的
  `sha256` 是否与生成时一致；不一致立即中止并提示联系工程师（防止脚本与功能包版本错配）；
- 运行一次只读的 `check` 预检（Setup 自检 + 确认二进制能在本机运行）。config 按二进制
  **真实路径**解析并以绝对路径传给 `check`/`apply`，二进制被软链到别处时也不会检查/修改错目录；
- 默认**不**自动备份 `config/`（现场人员会手工备份）；工程师需要时可在现场脚本上加
  `--backup`，由脚本备份到 `logs/config-backup-<时间>-<pid>.tar.gz`（权限 600）。启用备份后
  若备份失败会**中止**且不修改配置。

执行中每个功能的命令、开始/结束时间、耗时、退出码和完整输出都会写入
`logs/upgrade-<时间>-<pid>.log`，日志头部包含执行环境、生成人/工位/备注、方案指纹；
结束后还会对比执行前后配置文件的 `sha256`，列出新增/修改/删除的文件。某项失败时
停止后续功能并以原退出码退出，同时提示回传日志。按 `Ctrl-C`、关闭终端或 SSH 断开时
也会尽量补写“执行到哪一步、已改哪些文件、备份位置”。日志（以及启用 `--backup` 时的备份）
默认权限为 `600`。日志与备份路径会打印在最后，排查问题时把日志（及备份）回传给工程师即可。

现场脚本还支持（工程师远程指导时用）：`--yes` 跳过菜单直接执行、`--force` 功能定义
不一致仍继续（危险）、`--backup` 执行前自动备份 config、`--log-dir <目录>` 指定日志目录。
现场操作人员正常不需要这些参数。

### 现场交互选择功能（调试/临时使用）

交付时把 `run.sh`、`auto-config-update-32` 和整个 `features/`（保留子目录结构）放在同一目录，并在该目录放置待升级的 `config/`（也可通过 `--config` 指定）。执行：

```bash
bash run.sh
bash run.sh --config config-14346
```

入口递归列出 `.yaml` / `.yml`，显示 YAML 顶层单行 `id`、`description`；列表前缀取自 **features 下的相对目录**，不取 YAML 的 `version`。输入功能编号勾选后立即进入腔室选择页，输入腔室编号切换，回车保存返回一级菜单。二级页面不勾选任何腔室时表示**全部腔室**；取消勾选功能会清除对应的腔室选择。输入 `r` 查看执行摘要，再输入 `yes` 确认实际修改；`q` 退出。依列表顺序逐个调用 `auto-config-update-32 apply`，若某项失败则停止，不执行后续功能。声明了 `file:` 的文件级步骤由程序处理，不受腔室选项限制。

脚本仅依赖 Bash、`find`、`sort`、`awk`，不要求 `dialog`、Python 或联网；终端中按数字勾选。交互终端中切换页面或勾选后会刷新屏幕；非交互输入或重定向输出时不清屏，以便保留日志。运行前建议备份配置；多份功能之间如有依赖，请注意列表按文件路径排序。现场若已用 `prepare-onsite.sh` 生成一键脚本，优先使用一键脚本。

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

| 字段                           | 含义                                                                                                                                                                                                                                                            |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `anchor`                       | **class 路径**,逐层 descendant 定位(靠 class,不靠实例名)。首段可选域前缀 `Control`/`IO`/`IOBridge`(缺省 `Control`);段写法 `{X}`/裸名=按 class、`${X}`=按标签名。裸名段 class 无果时回退按 tag 名,且**只认自身(仅首段)或直接子元素**——同名深层节点须把路径写全。若 anchor 只在 master 正文命中(`Control_config.xml` 里直接写在 `<Control>` 下的节点,如 `/Control/CleanRuleRtManager`),自动按文件级步骤对 `Control/Control_config.xml` 全局执行一次。 |
| `where`                        | leaf 多实例时筛子集 + 插入定位:`tag-glob`/`attr` 筛选;`before-method: {name}` 把本步方法插到该既有方法之前。                                                                                                                                                    |
| `require` / `bind`             | `require.exist: {var: 路径}` 守卫+绑定,路径解析不到则跳过;`bind` 纯绑定不校验。                                                                                                                                                                                 |
| `add-node`                     | 建对象节点;可带 `attrs`、`include-entity`(内嵌声明的实体引用);`position: first` 插到 anchor 开标签之后(先于既有子节点与 `&实体;` 引用)。                                                                                                                         |
| `add-method` / `remove-method` | 加/删方法调用;有 `value` 渲染 `<name type="method">值</name>`,无 `value` 自闭合。删按 名+值 匹配。                                                                                                                                                              |
| `add-io` / `add-data`          | 建 IO/数据点位,按 `name` 判重。子元素按序 `Bd`(`auto`=推断板号)/`Ch`/`Min`/`Max`/`Accuracy`/`DescriptorList`/`Unit`;`add-data` 首属性固定 `type="data"`。                                                                                                       |
| `add-blank` / `add-comment`    | 在方法块前插入空行 / 段注释。`add-blank: N` 插 N 行空行;`add-comment` 是注释原文列表(须自带 `<!-- -->`)。二者不进解析树,按 anchor 原始字节区间判重(已存在即 no-op)。                                                                                            |
| `add-element`                  | 建普通元素(如 Setup 的 `<Param>`/`<Value>`、`<FileSize>`、`<spare>`),可 `before`/`after` 定位,可带 `include-entity`(内嵌声明的实体引用)。                                                                                                                       |
| `add-setup`                    | 向 Setup **成对追加**一个 `<Param .../>` 声明与对应的 `<Option>/<Value ...>` 取值:默认都加在各自序列末尾,可用 `before`/`after` 按既有参数名把两侧同步插到指定参数之前/之后,按 `param` 名判重(幂等)。                                                            |
| `remove-setup`                 | 从 Setup **成对删除**按 `param` 名匹配的 `<Param .../>` 与 `<Option>/<Value ...>`(整行删除,幂等)。                                                                                                                                                              |
| `add-recipe`                   | 向 `Recipe/RecipeNamespace.xml` 的指定 `RecipeClass`(支持 glob) 的 `<RecipeExecutor>` **幂等追加**一个 `<Param .../>`;省略 `file:` 时自动作用于该文件,同一 `RecipeExecutor` 下按 `param` 名判重。                                                                 |
| `add-xml`                      | 插入一段**内联 XML 片段**(整棵新对象子树),逐字保留 `&amp;&amp;` 等实体书写。                                                                                                                                                                                    |
| `set-text` / `set-attr`        | 改写已存在元素的文本 / 属性(按 `tag`+`attr`+可选 `old` 定位)。                                                                                                                                                                                                  |
| `rename-node`                  | 重命名已存在元素的**标签**(开/闭标签同步),可同时增改属性(`to` + `attrs`);选择器全空则作用于 anchor 自身。                                                                                                                                                       |
| `remove-node`                  | 删除已存在元素(含子树);`has` 子条件可区分同名不同内容的节点。                                                                                                                                                                                                   |
| `wrap` / `uncomment`           | 用任意 `open`/`close` 包裹一段节点区间(注释掉 / CDATA 化) / 放开(或删除)注释块。                                                                                                                                                                                |
| `file:`(step 级)               | 让某个 step 直接作用于 `config/<file>`(Setup、SysLog、master 等非片段文件)。                                                                                                                                                                                    |
| `new-file:`(step 级)           | 目标版本多出的**整份新文件**:文件不存在时按 `content` 逐字新建,已存在即 no-op(绝不覆盖)。                                                                                                                                                                       |

> `file:` / `new-file:` 的路径含占位符时**按腔室展开**:一份声明逐腔室执行,如
> `file: Setup/Setup_${Chamber}.xml` 用各腔室绑定替换(`${Chamber}` 或腔室 class 名);
> 某腔室没有该文件只告警跳过。`file:` 的路径还支持 glob(如 `Setup/*Setup.xml`),匹配到的
> 每个文件各执行一次(按字典序;无匹配只告警)。路径不含 `{`/glob 元字符时仍全局执行一次。详见
> [doc/feature-primitives.md](doc/feature-primitives.md) §7.14。
>
> `add-recipe` 步骤即便不写 `file:`,也属于**文件级步骤**,缺省作用于
> `config/Recipe/RecipeNamespace.xml`:取值全为字面量(含 glob)时在全部腔室之后全局执行一次,
> `--chamber` 不影响;取值含占位符(如 `recipe-class: "${Chamber}"`)或显式 `file:` 含占位符时
> 按 §7.14 逐腔室展开,`--chamber` 生效。详见
> [doc/feature-primitives.md](doc/feature-primitives.md) §7.19。
>
> 只在 master 正文命中、在所有腔室片段里都匹配不到的 anchor(如
> `anchor: /Control/CleanRuleRtManager`)会自动当作文件级步骤,对 `Control/Control_config.xml`
> 全局执行一次,同样不受 `--chamber` 影响;这与显式写
> `file: Control/Control_config.xml` 等价。`require` 的逻辑路径也支持 master 正文节点。
> 详见 [doc/feature-primitives.md](doc/feature-primitives.md) §3.4。

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

逻辑路径 `/IO/Ch1/...`、`/Control/Ch1/...` 只看**根腔室标签**,不看文件名。片段逐份原地回写;master 只在 step 显式写 `file:` 或 anchor 命中 master 正文节点时才回写。

## 模块

- `main.go` —— CLI 入口(`plan` / `apply` / `check` / `switch`)
- `internal/`
  - `xmldoc` —— 按字节偏移解析 XML、实体容忍(声明不展开)、外科回写的编辑记录
  - `config` —— master+实体装配、逻辑 IO/Control 视图、实体真名解析
  - `anchor` —— class 路径定位 + 同类多实例 fan-out + `where` 筛选
  - `ops` —— 幂等原语:`add-node`/`add-method`/`remove-method`/`add-io`/`add-data`/`add-blank`/`add-comment`/`add-entity-ref`/`add-element`/`add-setup`/`remove-setup`/`add-recipe`/`add-xml`/`set-text`/`set-attr`/`rename-node`/`remove-node`/`wrap`/`uncomment`
  - `xmlcmp` —— 语义比对(忽略空白/注释/属性序,比对元素树+实体+停用区),用于升级验收
  - `feature` —— 功能 YAML 加载 + `require`/`bind` 绑定
  - `setupcheck` —— `Setup/*.xml` 的 Param/Value 按下标一一对应校验
  - `simswitch` —— 批量切换 `config/*Simulated*` 的 `<setSimulated>` 开关
  - `splice` —— 外科式字节拼接写盘
  - `engine` —— steps 编排:逐腔室、逐步、逐实例执行并落盘
- `features/*.yaml` —— 功能定义 · `config/` —— 演示夹具 · `Makefile` —— 出包脚本
