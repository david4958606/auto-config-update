#!/usr/bin/env python3
"""auto-update-tool —— 对设备配置做"幂等语义补丁"的离线升级工具 (demo)。

设计要点（对应 PLAN.md）:
  - 给解析后的 XML 树打补丁，不改文本
  - 靠 class 定位节点(实例名可变)，靠现有引用反推 IO 路径(腔室号自洽)
  - 幂等：重跑 = no-op
  - 参数有默认值，机台清单可逐台覆盖
  - 物理接线地址(Bd/Ch)不臆测，缺失则校验标红

单文件便于评审；分区注释标出它对应 PLAN.md 里的哪个模块。
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

import yaml
from lxml import etree

ROOT = Path(__file__).parent
CONFIG_DIR = ROOT / "config"
MODELS_DIR = ROOT / "models"        # 按机型(腔室 class)共享，covers 大量同型号机台
MACHINES_DIR = ROOT / "machines"    # 稀疏：仅个别腔室的真异常

PARSER = etree.XMLParser(remove_blank_text=True)


# ── model.py：解析 / 寻址 / IO 路径解析 ──────────────────────────────
def load_xml(path: Path):
    return etree.parse(str(path), PARSER)


def save_xml(tree, path: Path):
    etree.indent(tree, space="    ")
    text = etree.tostring(tree, encoding="unicode")
    path.write_text(text + "\n", encoding="utf-8")


def io_path_segments(io_logical_path: str):
    """'/IO/Ch1/TurboPump/OnoffDO' -> ['Ch1','TurboPump','OnoffDO']  (去掉逻辑根 /IO)"""
    parts = [p for p in io_logical_path.strip("/").split("/") if p]
    assert parts and parts[0] == "IO", f"非法 IO 路径: {io_logical_path}"
    return parts[1:]


def resolve_io_node(io_root, io_logical_path: str):
    """在 IO 树里把逻辑路径解析成真实节点；解析不到返回 None（用于校验）。"""
    segs = io_path_segments(io_logical_path)
    if not segs or segs[0] != io_root.tag:
        return None
    node = io_root
    for seg in segs[1:]:
        node = node.find(seg)
        if node is None:
            return None
    return node


# ── feature.py：功能加载 + 适用性谓词 + 参数合并 ─────────────────────
def load_feature(path: Path) -> dict:
    return yaml.safe_load(Path(path).read_text(encoding="utf-8"))


def load_layer(path: Path, feature_id: str) -> dict:
    """读某一层(机型/单台)里本功能的覆盖项；文件不存在就返回空。"""
    if not path.exists():
        return {}
    data = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    return (data.get("features") or {}).get(feature_id) or {}


def resolve_config(feature: dict, chamber_class: str, chamber: str, fid: str):
    """分层解析：feature 默认 < 机型 < 单台override，先具体先生效。
    绝大多数腔室：机型文件已 cover，无需任何 machine 文件。"""
    params = dict(feature.get("params", {}))
    hardware = {}
    for layer in (
        load_layer(MODELS_DIR / f"{chamber_class}.yaml", fid),   # 机型层
        load_layer(MACHINES_DIR / f"{chamber}.yaml", fid),       # 单台层(稀疏)
    ):
        params.update(layer.get("params", {}))
        hardware.update(layer.get("hardware", {}))
    return params, hardware


def allocate_hardware(io_root, hardware: dict) -> dict:
    """把 Ch=auto 解析成该板上的下一个空闲通道——软地址零配置。
    物理固定的地址在机型文件里写死具体值即可，不会走到这里。"""
    out = dict(hardware)
    if str(out.get("Ch", "")).lower() == "auto":
        bd = str(out.get("Bd", ""))
        used = []
        for ch_el in io_root.iter("Ch"):
            bd_el = ch_el.getparent().find("Bd")
            if bd_el is not None and (bd_el.text or "") == bd and (ch_el.text or "").isdigit():
                used.append(int(ch_el.text))
        out["Ch"] = (max(used) + 1) if used else 0
    return out


def applies(control_root, feature: dict) -> bool:
    return bool(control_root.xpath(feature["applies_when"]))


def find_pump(control_root, feature: dict):
    """靠 class 定位分子泵 —— 节点标签名(实例名)可以是任意值。"""
    classes = feature["anchor"]["pump_class"]
    if isinstance(classes, str):
        classes = [classes]
    for cls in classes:
        hits = control_root.xpath(f".//*[@class='{cls}']")
        if hits:
            return hits[0]
    return None


# ── ops.py：幂等补丁原语 ────────────────────────────────────────────
def ensure_method(pump, name: str, value: str):
    """泵节点下确保存在 <name type="method">value</name>。返回动作描述或 None(无变化)。"""
    existing = pump.find(name)
    if existing is not None:
        if (existing.text or "") == value:
            return None  # 幂等：已是目标状态
        old = existing.text
        existing.text = value
        return f"更新方法 {name}: {old} -> {value}"
    el = etree.SubElement(pump, name)
    el.set("type", "method")
    el.text = value
    return f"新增方法 {name} -> {value}"


def ensure_io_data(container, spec: dict, name: str, hardware: dict):
    """IO 容器下确保存在一个 data 节点。返回动作描述或 None(无变化)。"""
    if container.find(name) is not None:
        return None  # 幂等
    data = etree.SubElement(container, name)
    for k, v in spec["attrs"].items():
        data.set(k, str(v))
    # 字段顺序：物理地址(Bd,Ch) 在前，语义字段(Min,Max,Unit) 在后，对齐既有 OnoffDO 风格
    for hf in spec.get("hardware_fields", []):
        etree.SubElement(data, hf).text = str(hardware.get(hf, "")) or None
    for k, v in spec["fields"].items():
        etree.SubElement(data, k).text = str(v)
    missing = [hf for hf in spec.get("hardware_fields", []) if hf not in hardware]
    note = f"  [⚠ 待填物理地址: {','.join(missing)}]" if missing else ""
    return f"新增 IO 数据 {name} (dataType={spec['attrs']['dataType']}, " \
           f"Min={spec['fields'].get('Min')}, Max={spec['fields'].get('Max')}, " \
           f"Unit={spec['fields'].get('Unit')}){note}"


# ── validate.py：交叉引用完整性 ─────────────────────────────────────
def validate_reference(io_root, method_value: str):
    node = resolve_io_node(io_root, method_value)
    if node is None:
        return f"✗ 引用失效: Control 指向 {method_value}，但 IO 中找不到该节点"
    missing = [f for f in ("Bd", "Ch") if (node.find(f) is None or not (node.find(f).text or "").strip())]
    if missing:
        return f"⚠ {method_value} 物理地址未填: {','.join(missing)}"
    return None


# ── feature.py：把一个功能应用到一个腔室 ────────────────────────────
def apply_to_chamber(feature: dict, chamber: str, write: bool):
    fid = feature["id"]
    control_path = CONFIG_DIR / f"Control_{chamber}.xml"
    io_path = CONFIG_DIR / f"IO_{chamber}.xml"
    if not control_path.exists():
        print(f"[{chamber}] 跳过：找不到 {control_path.name}")
        return

    ctree = load_xml(control_path)
    croot = ctree.getroot()

    # 适用性谓词：自适应判断该不该装
    if not applies(croot, feature):
        print(f"[{chamber}] 跳过：不满足适用性谓词（无分子泵/无可推导引用）")
        return

    # 分层解析参数 + 物理地址：feature默认 < 机型 < 单台。大多数腔室零额外文件。
    itree = load_xml(io_path)
    iroot = itree.getroot()
    chamber_class = croot.get("class")           # 如 PVD → models/PVD.yaml
    params, hardware = resolve_config(feature, chamber_class, chamber, fid)
    hardware = allocate_hardware(iroot, hardware)

    # 锚点 1：靠 class 定位泵（实例名可变）
    pump = find_pump(croot, feature)
    # 锚点 2：从泵已有方法反推 IO 容器路径（腔室号自洽，无需硬编码）
    src_method = pump.find(feature["anchor"]["io_container_from"])
    io_container_path = src_method.text.rsplit("/", 1)[0]   # /IO/Ch?/TurboPump

    ctx = {**params, "io_container": io_container_path}
    method_name = params["method_name"]
    method_value = feature["control_add_method"]["value"].format(**ctx)
    io_data_name = params["io_data_name"]

    actions = []

    # 改动一：Control 加方法
    a = ensure_method(pump, method_name, method_value)
    if a:
        actions.append((control_path, f"{pump.tag}(class={pump.get('class')}): {a}"))

    # 改动二：IO 加 data 节点
    container = resolve_io_node(iroot, io_container_path)
    if container is None:
        print(f"[{chamber}] ✗ 失败：IO 中找不到容器 {io_container_path}")
        return
    b = ensure_io_data(container, feature["io_add_data"], io_data_name, hardware)
    if b:
        actions.append((io_path, f"{io_container_path}: {b}"))

    # 语义 diff 输出
    if not actions:
        print(f"[{chamber}] 已是最新（幂等 no-op）")
    else:
        for path, desc in actions:
            print(f"[{chamber}] {path.name}: {desc}")

    # 落盘（仅 apply，且确有变化时）
    if write and actions:
        if any(p == control_path for p, _ in actions):
            save_xml(ctree, control_path)
        if any(p == io_path for p, _ in actions):
            save_xml(itree, io_path)

    # 校验：交叉引用完整性
    v = validate_reference(iroot, method_value)
    if v:
        print(f"[{chamber}]   校验: {v}")


def main():
    ap = argparse.ArgumentParser(description="auto-update-tool demo")
    ap.add_argument("mode", choices=["plan", "apply"], help="plan=dry-run, apply=写盘")
    ap.add_argument("--feature", required=True)
    ap.add_argument("--chamber", action="append", help="可重复；缺省=config 下所有腔室")
    args = ap.parse_args()

    feature = load_feature(args.feature)
    chambers = args.chamber or sorted(
        p.stem.replace("Control_", "") for p in CONFIG_DIR.glob("Control_*.xml")
    )

    print(f"功能 {feature['id']} v{feature['version']} | 模式={args.mode} | 腔室={chambers}\n")
    for ch in chambers:
        apply_to_chamber(feature, ch, write=(args.mode == "apply"))


if __name__ == "__main__":
    sys.exit(main())
