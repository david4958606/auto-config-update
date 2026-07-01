#!/usr/bin/env python3
"""addex —— "声明式锚点 + 方法注入" 这种 feature 描述法的最小实现 (demo)。

对应 features/add-pedcurpos-dataex.yaml 的写法：

    anchor:
      Control-class: PVD/ProcessLogger   # 锚点 = "class 路径"：class=PVD 的节点里、class=ProcessLogger 的节点
    param:
      method: addDataEx                  # 要加的方法名
      value: "/IO/{PVD}/Ped/CurPos,PedCurPos,1"   # {PVD} 自动替换成那一层匹配到的"标签名"(如 Ch1)

设计要点（与 upgrade.py 一脉相承）：
  - 靠 class 定位，不靠标签名/实例名 —— 锚点是一条 class 链，逐层 descendant 匹配。
  - {ClassName} 占位符 = 链上该 class 匹配到的节点的"标签名"，由腔室自洽解析。
  - 幂等：addDataEx 是"可多次调用"的方法，按 (方法名+值) 判重；已存在则 no-op。
  - 锚点链任一层断裂 → 自动跳过（class 命中但缺子节点，或 class 根本不命中）。

用法：
  uv run addex.py plan  --feature features/add-pedcurpos-dataex.yaml
  uv run addex.py apply --feature features/add-pedcurpos-dataex.yaml
  uv run addex.py plan  --feature ... --chamber Ch1 --chamber Ch2
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

import yaml
from lxml import etree

ROOT = Path(__file__).parent
CONFIG_DIR = ROOT / "config"
CONTROL_MASTER = CONFIG_DIR / "Control" / "Control_config.xml"   # 用实体装配出 <Control>
IO_MASTER = CONFIG_DIR / "IO_config.xml"                         # 用实体装配出 <IO>

PARSER = etree.XMLParser(remove_blank_text=True)
# 读 master 的 <!ENTITY ... SYSTEM ...> 声明（片段清单的"真相源"）
MASTER_PARSER = etree.XMLParser(load_dtd=True, resolve_entities=True, no_network=True)


# ── 解析 / 落盘 ──────────────────────────────────────────────────────
def load_xml(path: Path):
    return etree.parse(str(path), PARSER)


def save_xml(tree, path: Path):
    etree.indent(tree, space="    ")
    text = etree.tostring(tree, encoding="unicode")
    path.write_text(text + "\n", encoding="utf-8")


# ── 装配模型：master + 实体 → 一棵逻辑树，分片在多个无扩展名的片段文件里 ──
# 真实结构：Control_config.xml / IO_config.xml 用 XML 外部实体把片段拼成一棵
# 逻辑 <Control> / <IO> 树。片段文件无扩展名，且一个片段可含多个腔室段
# (如 IOBridge/IO_Motor 同时有 <Ch1> 和 <Ch4>)——它本身不是独立 XML，而是实体体。
def fragment_files(master_path: Path):
    """读 master 的实体声明，按声明顺序返回 [(entity_name, fragment_path)]。"""
    dtd = etree.parse(str(master_path), MASTER_PARSER).docinfo.internalDTD
    base = master_path.parent
    return [(e.name, (base / e.system_url).resolve())
            for e in dtd.iterentities() if e.system_url]


def load_fragment_sections(path: Path):
    """片段是"实体体"(可能含多个顶层元素、无 XML 声明)：包一层再解析，
    返回其中的顶层元素列表(每个 = 一个腔室段)。"""
    text = Path(path).read_text(encoding="utf-8")
    wrapper = etree.fromstring(f"<_frag>{text}</_frag>", PARSER)
    return list(wrapper)


def build_io_index():
    """按 master 实体清单加载所有 IO 片段，返回 [(fragment_path, chamber_element)]。
    IO_Motor 这类多段片段会展开成多条 —— 路径解析只看腔室标签，不看文件名。"""
    index = []
    for _name, fpath in fragment_files(IO_MASTER):
        for el in load_fragment_sections(fpath):
            index.append((fpath, el))
    return index


def resolve_io_logical(io_index, io_logical_path: str):
    """把逻辑路径 '/IO/Ch1/Ped/CurPosDI' 解析成 (fragment_path, node)。

    在所有腔室标签 == Ch1 的片段段里逐个尝试剩余路径(可能落在 IO_Motor 等任意
    片段)，命中即返回；都找不到则返回 (None, None)。
    """
    parts = [p for p in io_logical_path.strip("/").split("/") if p]
    assert parts and parts[0] == "IO", f"非法 IO 路径: {io_logical_path}"
    chamber, *rest = parts[1:]                       # 'Ch1', ['Ped','CurPosDI']
    for fpath, root in io_index:
        if root.tag != chamber:
            continue
        node = root
        for seg in rest:
            node = node.find(seg)
            if node is None:
                break
        if node is not None:
            return fpath, node
    return None, None


# ── 锚点解析：沿一条 class 链逐层 descendant 匹配 ────────────────────
def resolve_anchor(root, class_path: list[str]):
    """沿 class 链定位锚点节点。

    返回 (anchor_node, tags)：
      anchor_node —— 链最后一层匹配到的节点（要往里加方法的地方）
      tags        —— {class名: 匹配节点的标签名}，供 {占位符} 替换，如 {"PVD": "Ch1"}
    任一层匹配不到 → 返回 (None, 原因)。
    """
    # 第一层：从根开始(含自身)找该 class
    node = _find_by_class(root, class_path[0], include_self=True)
    if node is None:
        return None, f"未找到 class={class_path[0]} 的节点"
    tags = {class_path[0]: node.tag}

    # 其余层：在上一层节点的子孙里继续找
    for cls in class_path[1:]:
        child = _find_by_class(node, cls, include_self=False)
        if child is None:
            return None, f"class={node.get('class')}({node.tag}) 下未找到 class={cls} 的节点"
        tags[cls] = child.tag
        node = child
    return node, tags


def _find_by_class(node, cls: str, include_self: bool):
    axis = "descendant-or-self::*" if include_self else ".//*"
    hits = node.xpath(f"{axis}[@class='{cls}']")
    return hits[0] if hits else None


# ── 幂等补丁原语：注入一个"可多次调用"的方法 ───────────────────────
def ensure_method_call(anchor, name: str, value: str):
    """锚点下确保存在 <name type="method">value</name>。

    addDataEx 语义上可被多次调用(记录多个数据点)，故按 (名字+值) 判重：
    同名同值已存在 → no-op；同名不同值 → 视作新增一条。
    """
    for el in anchor.findall(name):
        if (el.text or "") == value:
            return None  # 幂等
    el = etree.SubElement(anchor, name)
    el.set("type", "method")
    el.text = value
    return f"新增方法调用 {name}({value})"


# ── 把一个功能应用到一个腔室 ────────────────────────────────────────
def apply_to_chamber(feature: dict, chamber: str, ctree, control_path: Path,
                     io_index, write: bool):
    croot = ctree.getroot()

    class_path = [c for c in feature["anchor"]["Control-class"].split("/") if c]
    anchor, info = resolve_anchor(croot, class_path)
    if anchor is None:
        print(f"[{chamber}] 跳过：{info}")
        return

    # {PVD} 之类的占位符 → 用锚点链上该 class 匹配到的标签名替换
    tags = dict(info)

    # condition.ensure-exist：逐条解析逻辑 IO 路径(可能落在 IO_Motor.xml 等任意片段)。
    #   命中 → 把变量名(如 PedCurPos)绑定为解析后的逻辑路径，供 value 复用；
    #   任一条找不到 → 前置条件不满足，跳过整个腔室。
    for binding in (feature.get("condition", {}) or {}).get("ensure-exist", []):
        (var, tmpl), = binding.items()
        logical = tmpl.format(**tags)
        fpath, node = resolve_io_logical(io_index, logical)
        if node is None:
            print(f"[{chamber}] 跳过：condition 不满足 —— 所有 IO 片段中均无 {logical}")
            return
        print(f"[{chamber}]   condition ok: {logical} 命中于 {fpath.name}（绑定 {{{var}}}）")
        tags[var] = logical

    name = feature["param"]["method"]
    value = feature["param"]["value"].format(**tags)

    action = ensure_method_call(anchor, name, value)
    if action is None:
        print(f"[{chamber}] 已是最新（幂等 no-op）")
        return

    print(f"[{chamber}] {control_path.name}: {anchor.tag}(class={anchor.get('class')}): {action}")
    if write:
        save_xml(ctree, control_path)


def main():
    ap = argparse.ArgumentParser(description="addex demo —— 声明式锚点+方法注入")
    ap.add_argument("mode", choices=["plan", "apply"], help="plan=dry-run, apply=写盘")
    ap.add_argument("--feature", required=True)
    ap.add_argument("--chamber", action="append", help="可重复；缺省=config 下所有腔室")
    args = ap.parse_args()

    feature = yaml.safe_load(Path(args.feature).read_text(encoding="utf-8"))

    # IO 侧只读：装配出逻辑 IO 索引(含 IO_Motor 的多腔室段)，供 condition 解析
    io_index = build_io_index()

    # Control 侧可写：每个片段独立解析(单腔室根)，原地编辑该片段文件
    control_frags = fragment_files(CONTROL_MASTER)
    chambers = [load_xml(fp).getroot().tag for _n, fp in control_frags]
    want = set(args.chamber) if args.chamber else None

    print(f"功能 {feature['id']} v{feature['version']} | 模式={args.mode} | "
          f"腔室={[c for c in chambers if not want or c in want]}\n")
    for (_name, fpath), ch in zip(control_frags, chambers):
        if want and ch not in want:
            continue
        ctree = load_xml(fpath)
        apply_to_chamber(feature, ch, ctree, fpath, io_index,
                         write=(args.mode == "apply"))


if __name__ == "__main__":
    sys.exit(main())
