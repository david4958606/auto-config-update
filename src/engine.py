"""engine —— 把一个 feature 的 steps 应用到各腔室，产出语义 diff、原地落盘。

编排：
  遍历 Control 片段(每个 = 一个腔室，实体保留式加载) →
    按 steps 顺序执行(共享同一棵内存树，故步2能看到步1刚建的 <IonGauge>) →
      每步 anchor 定位(leaf 多实例则 fan-out) →
        每个实例：并入腔室级绑定 → require/bind → 执行动作(幂等) →
  该腔室有改动才回写它自己的片段(保留 &实体; 与原格式；master/其它片段不动)。

腔室级绑定(chamber_binds)：add-node 建对象后登记 {标签}->"./标签"，供后续步骤跨步引用。
"""
from __future__ import annotations

from . import ops
from .anchor import resolve_anchors
from .feature import Skip, resolve_bindings
from .model import (CONTROL_MASTER, build_indexes, fragment_files,
                    load_control_fragment, save_control_fragment)


def _run_step(step, croot, indexes, chamber_binds, edits, log) -> None:
    """执行单个 step；把产生的编辑记录追加到 edits。"""
    name = step.get("name", "?")
    class_path = [c for c in step["anchor"].split("/") if c]
    anchors = resolve_anchors(croot, class_path, step.get("where"))
    if not anchors:
        log(f"  步[{name}] 跳过：anchor {step['anchor']} 无匹配")
        return

    for node, atags in anchors:
        inst = f"{node.tag}(class={node.get('class')})"
        tags = {**chamber_binds, **atags}     # 并入跨步对象引用；anchor 绑定优先
        try:
            tags, notes = resolve_bindings(step, tags, indexes)
        except Skip as e:
            log(f"  步[{name}] {inst} 跳过：{e}")
            continue
        for var, logical, fpath in notes:
            where = fpath.name if fpath else "?"
            log(f"  步[{name}] {inst}: {logical} 命中于 {where}（绑定 {{{var}}}）")

        results = []
        for nd in step.get("add-node", []) or []:
            attrs = {k: str(v).format(**tags) for k, v in (nd.get("attrs") or {}).items()}
            results.append(ops.add_node(node, nd["tag"], nd["class"], attrs))
            chamber_binds[nd["tag"]] = "./" + nd["tag"]   # 登记对象引用，供后续步骤 {标签}
        for m in step.get("add-method", []) or []:
            val = m["value"].format(**tags) if m.get("value") is not None else None
            results.append(ops.add_method(node, m["name"], val))
        for m in step.get("remove-method", []) or []:
            results.append(ops.remove_method(node, m["name"], m["value"].format(**tags)))

        for did, msg, edit in results:
            log(f"  步[{name}] {inst}: {'✎' if did else '·'} {msg}")
            if edit:
                edits.append(edit)


def apply_feature(feature: dict, selected: list[str] | None, write: bool, log=print):
    indexes = build_indexes()
    want = set(selected) if selected else None

    for _name, fpath in fragment_files(CONTROL_MASTER):
        croot, text, offset = load_control_fragment(fpath)
        chamber = croot.tag
        if want and chamber not in want:
            continue

        log(f"[{chamber}] ({fpath.name})")
        chamber_binds: dict = {}
        edits: list = []
        for step in feature.get("steps", []):
            _run_step(step, croot, indexes, chamber_binds, edits, log)

        if edits and write:
            save_control_fragment(text, offset, edits, fpath)
            log(f"[{chamber}] ✎ 已写回 {fpath.name}（{len(edits)} 处编辑，其余字节不动）")
