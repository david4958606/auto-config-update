"""splice —— 外科式文本拼接写盘。

不重排整棵树，只在"插入点/删除点"改动原文，其余字节逐字保留 → 真正 clean diff、
空标签风格不变。做法：
  - 每个新增元素：把它自己的文本(仅新内容)拼到"其父节点闭合标签之前"。
    父节点若本身是新建的(如 IonGauge 下的方法)，其文本已含在祖先插入里，跳过。
  - 每个删除元素：定位其在原文的行区间并删掉。

元素在原文里的起始行由 lxml sourceline 给出(减去解析时套的 DOCTYPE+<_frag> 前缀行数)；
闭合标签行由一个"深度计数扫描"从起始行往下找到。假设：结构良好、实体写作 &x;、
标签属性里不含裸 '<'（设备 XML 均满足）。
"""
from __future__ import annotations

import re

from lxml import etree

from .ops import _real_depth

_COMMENT = re.compile(r"<!--.*?-->", re.S)


def _localname(el) -> str:
    return etree.QName(el).localname


def _start_idx(el, offset: int) -> int:
    """元素在原文里的 0-based 起始行。"""
    return el.sourceline - offset - 1


def _find_close_idx(lines: list[str], start_idx: int, tag: str) -> int:
    """从 start_idx 起做深度计数，返回含匹配 </tag> 的行 0-based 索引。"""
    open_re = re.compile(rf"<{re.escape(tag)}(\s|>|/>|/\s)")
    close_re = re.compile(rf"</{re.escape(tag)}\s*>")
    selfclose_re = re.compile(rf"<{re.escape(tag)}\b[^>]*/>")
    depth = 0
    for i in range(start_idx, len(lines)):
        line = _COMMENT.sub("", lines[i])
        opens = len(open_re.findall(line))
        selfs = len(selfclose_re.findall(line))
        closes = len(close_re.findall(line))
        depth += opens - selfs - closes
        if depth <= 0 and i >= start_idx:
            return i
    return len(lines) - 1


def _render(el, depth: int) -> str:
    """把新增元素渲染成一行/多行文本块(首行缩进 depth 层，内部缩进已在建树时设好)。"""
    body = etree.tostring(el, encoding="unicode", with_tail=False)
    return "    " * depth + body + "\n"


def render(original_text: str, offset: int, edits: list) -> str:
    """edits: [('insert', parent, el), ('delete', parent, [els])]。返回拼接后的全文。"""
    lines = original_text.splitlines(keepends=True)
    created = {id(el) for kind, _p, el in edits if kind == "insert"}

    inserts: dict[int, list[str]] = {}   # close_line_idx -> [block, ...]
    deletes: list[tuple[int, int]] = []  # (start_idx, end_idx) 闭区间

    for kind, parent, payload in edits:
        if kind == "insert":
            if id(parent) in created:
                continue                 # 父是新建节点，文本含在祖先插入里
            close_idx = _find_close_idx(lines, _start_idx(parent, offset), _localname(parent))
            inserts.setdefault(close_idx, []).append(_render(payload, _real_depth(parent) + 1))
        else:
            for el in payload:
                s = _start_idx(el, offset)
                deletes.append((s, _find_close_idx(lines, s, _localname(el))))

    # 自底向上应用，保持行号稳定
    for close_idx in sorted(inserts, reverse=True):
        lines[close_idx:close_idx] = inserts[close_idx]
    for s, e in sorted(deletes, reverse=True):
        del lines[s:e + 1]

    return "".join(lines)
