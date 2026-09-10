#!/usr/bin/env python3
"""生成 features/upgrade-files.yaml（Setup/*.xml、SysLog_config.xml、Control_config.xml）。

用法（仓库根目录）：
    .venv/bin/python hack/gen_upgrade_files.py > features/upgrade-files.yaml

要点：Setup 文件里 `<Param>` 声明与 `<Option>` 内的 `<Value>` 取值必须是**一一对应**的
（数量与顺序都对齐）。这里用同一份 zone/param 列表同时生成 Param 与 Value 两组步骤，
从构造上保证两边顺序一致；Param 用 `before: Option` 逐个插到 Option 之前，Value 用
`before: Value[LidShowerhead (TOS) ms]` 逐个插到同一位置，因而落盘顺序 = 声明顺序。
"""

import sys

out = []
w = out.append

w("# 文件级升级：Setup/*.xml、SysLog_config.xml、Control_config.xml")
w("# 这些文件不属于 Control/IO 片段，用 file: 直接定位；见 doc/config-upgrade-design.md §3.8。")
w("# 由 hack/gen_upgrade_files.py 生成。")
w("id: upgrade-files")
w("description: 文件级配置升级（Setup 参数、SysLog FileSize、TimeSynchronizer 方法）")
w("version: 1")
w("")
w("steps:")

# ── SysLog：新增 FileSize ──
w("  # ── SysLog：新增 FileSize ──")
w("  - name: SysLog 新增 FileSize")
w("    file: SysLog_config.xml")
w("    anchor: SysLog")
w("    add-element:")
w('      - { tag: FileSize, text: "10", after: { tag: Threshold } }')
w("")

# ── Control 主文件：TimeSynchronizer ──
w("  # ── Control 主文件：TimeSynchronizer 增加一秒定时更新 ──")
w("  - name: TimeSynchronizer 增加 enableOneSecTimerUpdate")
w("    file: Control/Control_config.xml")
w("    anchor: /Control/TimeSynchronizer")
w("    add-method:")
w("      - { name: enableOneSecTimerUpdate }")
w("")

# ── CleanRuleRtInfo ──
w("  # ── CleanRuleRtInfo：延迟时间串改为新腔室布局 ──")
w("  - name: CleanRuleRtInfo 更新 CleanRuleDelayTimeInfo")
w("    file: Setup/CleanRuleRtInfo.xml")
w("    anchor: CleanRuleRtInfo/Option")
w("    set-text:")
w("      - { tag: Value, attr: { paramName: CleanRuleDelayTimeInfo }, value: \"Ch1:5000!Ch2:5000!Ch3:5000!Ch4:5000!Ch5:5000!Ch6:5000!ChE:5000!ChF:5000!\" }")
w("")


def setup_block(ch, params, values):
    """输出一个 Setup 文件的 Param 声明与 Value 取值两组步骤（顺序一一对应）。"""
    w(f"  # ── {ch}Setup：新增 {len(params)} 个 Param 与一一对应的 {len(values)} 个 Value ──")
    w(f"  - name: {ch}Setup 增补参数声明")
    w(f"    file: Setup/{ch}Setup.xml")
    w(f"    anchor: {ch}Setup")
    w("    add-element:")
    for name, attrs in params:
        a = ", ".join(f'{k}: {v}' for k, v in attrs.items())
        w("      - tag: Param")
        w(f"        attrs: {{ {a} }}")
        w("        self-close: true")
        w("        after: { tag: Param, attr: { name: HeaterWaterVlvOpenTemp } }")
    w(f"  - name: {ch}Setup 增补取值")
    w(f"    file: Setup/{ch}Setup.xml")
    w(f"    anchor: {ch}Setup/Option")
    w("    add-element:")
    for name, value in values:
        w("      - tag: Value")
        w(f"        attrs: {{ paramName: {name} }}")
        w(f'        text: "{value}"')
        w("        after: { tag: Value, attr: { paramName: HeaterWaterVlvOpenTemp } }")
    w("")


# Ch1/Ch2：两个加热器温差参数
for ch in ("Ch1", "Ch2"):
    params = []
    values = []
    for idx in (1, 2):
        tag = "" if idx == 1 else "2"
        name = f"Heater{idx}TcTempDiffMax"
        params.append((name, {
            "name": name,
            "dataObject": f'"/SETUP/Control/{ch}/Heater/PhyHeater{tag}/TcTempDiffMax"',
            "type": "D", "min": 0, "max": 300, "units": "K", "accuracy": "0.0000001", "default": 0,
        }))
        values.append((name, "10"))
    setup_block(ch, params, values)

# ChC/ChD：单个加热器温差参数
for ch in ("ChC", "ChD"):
    name = "HeaterTcTempDiffMax"
    params = [(name, {
        "name": name,
        "dataObject": f'"/SETUP/Control/{ch}/Heater/TcTempDiffMax"',
        "type": "D", "min": 0, "max": 50, 'units': '""', "accuracy": "0.001", "default": 30,
    })]
    setup_block(ch, params, [(name, "10")])

# ChE/ChF：删 2 项、按 zone 交错新增 28 项（与目标顺序一致）
ZONES = ["Zone1", "Zone3", "Zone4", "Zone5", "Zone6", "Zone7", "Zone8", "Zone9", "Zone10", "Zone13", "Zone16"]
for ch in ("ChE", "ChF"):
    base = f"/SETUP/Control/{ch}"
    w(f"  # ── {ch}TempControl：删除旧的 Ped 校准参数/取值（成对） ──")
    for side, tag, key in (("Param", "Param", "name"), ("Value", "Value", "paramName")):
        anchor = f"{ch}TempControl" if tag == "Param" else f"{ch}TempControl/Option"
        w(f"  - name: {ch}TempControl 删除旧的 Ped 校准{side}")
        w(f"    file: Setup/{ch}TempControl.xml")
        w(f"    anchor: {anchor}")
        w("    remove-node:")
        w(f'      - {{ tag: {tag}, attr: {{ {key}: HeaterCalibrationOffset }} }}')
        w(f'      - {{ tag: {tag}, attr: {{ {key}: HeaterTcTempDiffMax }} }}')
    # Param 与 Value 用同一份列表生成 → 数量/顺序严格一一对应
    # (名字, dataObject, min, max, default 声明值, Option 里的取值)
    params = [
        ("Zone35TempHigh", f"{base}/Zone35TempHigh", 0, 300, "75", "75"),
        ("Zone35TempLow", f"{base}/Zone35TempLow", 0, 300, "55", "55"),
    ]
    for z in ZONES:
        mx = 300 if z == "Zone3" else 25
        params.append((f"{z}CalibrationOffset", f"{base}/TempControl/{z}/CalibrationOffset", -25, mx, "0", "0"))
        params.append((f"{z}TcTempDiffMax", f"{base}/TempControl/{z}/TcTempDiffMax", 0, 30, "10", "20" if z == "Zone13" else "15"))
    params.append(("PedCalibrationOffset", f"{base}/TempControl/Ped/CalibrationOffset", -25, 25, "0", "0"))
    params.append(("PedTcTempDiffMax", f"{base}/TempControl/Ped/TcTempDiffMax", 0, 30, "10", "15"))
    params.append(("LidShowerheadCalibrationOffset", f"{base}/TempControl/LidShowerhead/CalibrationOffset", -25, 25, "0", "0"))
    params.append(("LidShowerheadTcTempDiffMax", f"{base}/TempControl/LidShowerhead/TcTempDiffMax", 0, 30, "10", "15"))

    w(f"  - name: {ch}TempControl 增补 {len(params)} 个 Param 声明")
    w(f"    file: Setup/{ch}TempControl.xml")
    w(f"    anchor: {ch}TempControl")
    w("    add-element:")
    for n, dobj, mn, mx, dflt, _v in params:
        w("      - tag: Param")
        w(f'        attrs: {{ name: {n}, dataObject: "{dobj}", type: D, min: {mn}, max: {mx}, units: degC, accuracy: 0.01, default: {dflt} }}')
        w("        self-close: true")
        w("        before: { tag: Option }")
    w(f"  - name: {ch}TempControl 增补 {len(params)} 个一一对应的 Value 取值")
    w(f"    file: Setup/{ch}TempControl.xml")
    w(f"    anchor: {ch}TempControl/Option")
    w("    add-element:")
    for n, _dobj, _mn, _mx, _dflt, v in params:
        w("      - tag: Value")
        w(f"        attrs: {{ paramName: {n} }}")
        w(f'        text: "{v}"')
        # 目标把新增取值放在 LidShowerhead (TOS) ms 之后：同一锚点连续插入按声明序拼接。
        w('        after: { tag: Value, attr: { paramName: "LidShowerhead (TOS) ms" } }')
    w("")

sys.stdout.write("\n".join(out))
