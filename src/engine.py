"""engine —— 把一个 feature 的 steps 应用到各腔室，产出语义 diff、原地落盘。

编排顺序：
  遍历 Control 片段(每个 = 一个腔室) →
    按 steps 顺序执行(共享同一棵内存树，故步2能看到步1刚建的 <Ped>) →
      每步 anchor 定位(leaf 多实例则 fan-out) →
        每个实例：require/bind 绑变量(不满足则跳过该实例) → 执行动作(幂等) →
  该腔室有改动才写回它自己的片段文件(master 与其它片段不动)。
"""
from __future__ import annotations

from . import ops
from .anchor import resolve_anchors
from .feature import Skip, resolve_bindings
from .model import (CONTROL_MASTER, build_io_index, fragment_files, load_xml,
                    save_xml)


def _run_step(step: dict, chamber: str, croot, io_index, log) -> bool:
    """执行单个 step，返回是否有实际改动。"""
    class_path = [c for c in step["anchor"].split("/") if c]
    anchors = resolve_anchors(croot, class_path, step.get("where"))
    if not anchors:
        log(f"  步[{step.get('name','?')}] 跳过：anchor {step['anchor']} 无匹配")
        return False

    changed = False
    for node, tags in anchors:
        inst = f"{node.tag}(class={node.get('class')})"
        try:
            tags, notes = resolve_bindings(step, tags, io_index)
        except Skip as e:
            log(f"  步[{step.get('name','?')}] {inst} 跳过：{e}")
            continue
        for var, logical, fpath in notes:
            log(f"  步[{step.get('name','?')}] {inst}: {logical} 命中于 {fpath.name}（绑定 {{{var}}}）")

        results = []
        for nd in step.get("add-node", []) or []:
            results.append(ops.add_node(node, nd["tag"], nd["class"], nd.get("attrs")))
        for m in step.get("add-method", []) or []:
            results.append(ops.add_method(node, m["name"], m["value"].format(**tags)))
        for m in step.get("remove-method", []) or []:
            results.append(ops.remove_method(node, m["name"], m["value"].format(**tags)))

        # 完整语义 diff：每个声明的动作都出一行，✎=改动 ·=已达目标(no-op)
        for did, msg in results:
            log(f"  步[{step.get('name','?')}] {inst}: {'✎' if did else '·'} {msg}")
        if any(did for did, _ in results):
            changed = True
    return changed


def apply_feature(feature: dict, selected: list[str] | None, write: bool, log=print):
    io_index = build_io_index()
    want = set(selected) if selected else None

    for _name, fpath in fragment_files(CONTROL_MASTER):
        ctree = load_xml(fpath)
        croot = ctree.getroot()
        chamber = croot.tag
        if want and chamber not in want:
            continue

        log(f"[{chamber}] ({fpath.name})")
        changed = False
        for step in feature.get("steps", []):
            if _run_step(step, chamber, croot, io_index, log):
                changed = True

        if changed and write:
            save_xml(ctree, fpath)
            log(f"[{chamber}] ✎ 已写回 {fpath.name}")
