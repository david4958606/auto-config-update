# auto-update-tool (demo)

对设备配置做**幂等语义补丁**的离线升级工具。详见 [PLAN.md](PLAN.md)。

## 运行

```bash
uv run upgrade.py plan  --feature features/turbopump-setspeed.yaml          # dry-run，不写盘
uv run upgrade.py apply --feature features/turbopump-setspeed.yaml          # 写盘
uv run upgrade.py plan  --feature features/... --chamber Ch1 --chamber Ch2  # 指定腔室
```

## 这个 demo 的功能

「为分子泵增加 `setSpeedSp()` 设置转速」：
- Control 的泵节点下加 `setSpeedSp` 方法，指向 IO
- IO 容器下加一个 `SpeedDO` 数据节点（I 型，0–20000 rpm）

## 它如何应对"各台不一样"

| 差异 | 工具怎么处理 |
|------|------|
| Ch2 泵节点不叫 `PhyTurboPump`（叫 `TurboPumpMain`） | 靠 `class="PhyTurboPump"` 定位，**实例名无关** |
| 腔室号、IO 容器路径不同 | 从泵已有的 `setOnoffSp` 引用**反推**，自然自洽 |
| Ch2 的 IO 数据要叫 `PumpSpeedDO` | `machines/Ch2.yaml` 里 `params.io_data_name` **逐台覆盖** |
| 物理接线地址 Bd/Ch 因台而异 | 工具不臆测：机台清单提供，缺失则**校验标红** |
| 某腔室根本没分子泵（Ch4 只有干泵） | 适用性谓词不命中，**自动跳过** |
| 重复运行 | **幂等** no-op |

## 要写多少 yaml？（分层，machine 文件是稀疏例外）

值的来源按"先具体先生效"分层解析：**单台override > 机型 > feature默认 > 自动推导**。

| 层 | 文件数 | 放什么 |
|----|--------|--------|
| `features/<id>.yaml` | 每功能 1 份 | 改动本身 + 全机台相同的语义参数(Min/Max/Unit) |
| `models/<机型>.yaml` | 每**机型** 1 份 | 该机型的物理接线约定(板号；通道可 `auto` 自动分配) |
| `machines/<Ch>.yaml` | **仅个别异常** | 某腔室与机型不同的地方(如 Ch2 要叫 PumpSpeedDO) |

→ 100 台同型号 × 6 腔室：**1 份 feature + 1 份机型文件 + 几条异常**，不是 600 份。
路径/腔室号/通道全自动推导，普通腔室零额外文件。

## 目录

- `upgrade.py` —— 引擎 + CLI（分区注释对应 PLAN.md 的模块）
- `features/*.yaml` —— 功能定义（一个功能一份）
- `models/<机型>.yaml` —— 机型层（同型号机台共享，covers 绝大多数）
- `machines/<Ch>.yaml` —— 单台层（稀疏，仅真异常）
- `config/Control_<Ch>.xml`、`config/IO_<Ch>.xml` —— 配置（Ch1 真实，Ch2–4 演示夹具）
