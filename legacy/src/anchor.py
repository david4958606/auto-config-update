"""anchor —— 靠 class 路径定位节点，支持"同类多实例"fan-out。

关键设计(应对 PhyGauge 这类一腔多个的对象)：
  - anchor 是一条 class 路径(如 CVD/PhyChuck)，逐层 descendant 匹配。
  - leaf(最后一层)若命中同一 class 的多个实例，则【全部返回】，由上层对每个各执行一次，
    绝不静默只取第一个。
  - 每个匹配都带一份 tags：{class名: 该实例的标签名}，供 {占位符} 逐实例替换
    (PhyChuck→Ped/Ped01, PhyGauge→Gauge01/Gauge02)。
  - where(可选)：leaf 命中多个时按标签精确筛子集(glob)，取代脆弱的"标签子串包含"。
"""
from __future__ import annotations

import fnmatch


def _find_by_class(node, cls: str, include_self: bool):
    axis = "descendant-or-self::*" if include_self else ".//*"
    return node.xpath(f"{axis}[@class='{cls}']")


def _match_where(node, where: dict) -> bool:
    """where 谓词：目前支持
       tag-glob: 标签名 glob(如 'Ped*'、'Gauge0?')
       attr: {属性名: 期望值} 全部相等
    """
    if not where:
        return True
    if "tag-glob" in where and not fnmatch.fnmatch(node.tag, where["tag-glob"]):
        return False
    for k, v in (where.get("attr") or {}).items():
        if node.get(k) != str(v):
            return False
    return True


def resolve_anchors(root, class_path: list[str], where: dict | None = None):
    """返回所有匹配的 (anchor_node, tags)。

    leaf 多实例 → 多条；一条都没有 → 空列表(上层据此打印跳过原因)。
    tags 记录路径上每一层匹配节点的标签名，如 {'CVD': 'Ch4', 'PhyChuck': 'Ped'}。
    """
    # 第一层：从根(含自身)找该 class
    frontier = [(n, {class_path[0]: n.tag})
                for n in _find_by_class(root, class_path[0], include_self=True)]
    # 其余层：在上一层节点的子孙里继续找(逐层可能各自 fan-out)
    for cls in class_path[1:]:
        nxt = []
        for node, tags in frontier:
            for child in _find_by_class(node, cls, include_self=False):
                t = dict(tags)
                t[cls] = child.tag
                nxt.append((child, t))
        frontier = nxt
    # where 只筛 leaf
    return [(n, t) for n, t in frontier if _match_where(n, where)]
