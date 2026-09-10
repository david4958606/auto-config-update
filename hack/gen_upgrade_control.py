#!/usr/bin/env python3
"""从 config_old → config 的**结构差异**生成 Control 侧的升级 feature。

用法（仓库根目录）：
    .venv/bin/python hack/gen_upgrade_control.py > features/upgrade-control.yaml

实现要点：这里**不用 lxml**——lxml 会把 `&amp;&amp;` 这类预定义实体解析掉、序列化时丢失，
而设备配置里到处都是这种混合内容。改为自带一个带字节偏移的极简分词器（与 Go 侧
internal/xmldoc 同构）：注释/CDATA/指令整段跳过，实体 `&name;` 作为不透明子节点保留，
每个节点的"原文"就是源文件的字节切片，序列化即原文，逐字保真。

自顶向下对齐新旧两棵树：
  - 两边都有、结构相同的节点 → 递归比；
  - 只有新的一侧有 → add-xml(内联片段，取新文件原文)；
  - 只有旧的一侧有 → remove-node(语义上等价于把目标里的注释块去掉)；
  - 同 tag/属性的叶子文本不同 → set-text；属性不同 → set-attr。

生成的是**静态** feature 文件：运行时不依赖 config/(目标)，只依赖 primitives。
"""
import json
import sys

FILES = [
    "Control/Control_Ch1",
    "Control/Control_Ch2",
    "Control/Control_ChC",
    "Control/Control_ChD",
    "Control/Control_ChE",
    "Control/Control_ChF",
    "Control/Interlock_Ch1",
    "Control/Interlock_Ch2",
    "Control/Interlock_Ch4",
    "Control/Interlock_Ch5",
    "Control/Interlock_Ch6",
    "Control/Interlock_ChC",
    "Control/Interlock_ChD",
    "Control/Interlock_ChE",
    "Control/Interlock_ChF",
    "Control/Interlock_Platform",
    "Control/Interlock_Platform_ChA",
    "Control/Interlock_Platform_ChB",
    "Control/Interlock_Platform_LA",
    "Control/Interlock_Platform_LB",
]


class Node:
    __slots__ = ("tag", "attrs", "children", "is_entity", "ent", "start", "end", "text", "src")

    def __init__(self, tag="", attrs=None, is_entity=False, ent="", start=-1, end=-1):
        self.tag = tag
        self.attrs = attrs or []
        self.children = []
        self.is_entity = is_entity
        self.ent = ent
        self.start = start
        self.end = end
        self.text = ""
        self.src = None

    def raw(self):
        return self.src[self.start:self.end]


def parse(src):
    """返回若干顶层节点(与 Go xmldoc.Parse 同构)。"""
    roots = []
    stack = []
    i = 0
    n = len(src)

    def add(node):
        if stack:
            stack[-1].children.append(node)
        else:
            roots.append(node)

    while i < n:
        c = src[i]
        if c == "<":
            if src.startswith("<!--", i):
                j = src.find("-->", i)
                i = n if j < 0 else j + 3
            elif src.startswith("<![CDATA[", i):
                j = src.find("]]>", i)
                i = n if j < 0 else j + 3
            elif src.startswith("<?", i):
                j = src.find("?>", i)
                i = n if j < 0 else j + 2
            elif src.startswith("<!", i):
                j = src.find(">", i)
                i = n if j < 0 else j + 1
            elif src.startswith("</", i):
                j = src.find(">", i)
                name = src[i + 2:j].strip()
                node = stack.pop()
                assert node.tag == name, "结束标签不匹配 %s != %s" % (name, node.tag)
                node.end = j + 1
                i = j + 1
            else:
                j = i + 1
                while j < n and src[j] not in " \t\r\n/>":
                    j += 1
                tag = src[i + 1:j]
                attrs = []
                selfclose = False
                while True:
                    while j < n and src[j] in " \t\r\n":
                        j += 1
                    if j >= n:
                        break
                    if src.startswith("/>", j):
                        selfclose = True
                        j += 2
                        break
                    if src[j] == ">":
                        j += 1
                        break
                    k = j
                    while k < n and src[k] not in " \t\r\n=/>":
                        k += 1
                    aname = src[j:k]
                    j = k
                    while j < n and src[j] in " \t\r\n":
                        j += 1
                    aval = ""
                    if j < n and src[j] == "=":
                        j += 1
                        while j < n and src[j] in " \t\r\n":
                            j += 1
                        q = src[j]
                        j += 1
                        k = src.find(q, j)
                        aval = src[j:k]
                        j = k + 1
                    attrs.append((aname, aval))
                node = Node(tag=tag, attrs=attrs, start=i, end=j)
                if selfclose:
                    node.end = j
                    add(node)
                else:
                    add(node)
                    stack.append(node)
                i = j
        elif c == "&":
            j = src.find(";", i)
            if j < 0:
                j = i + 1
            node = Node(is_entity=True, ent=src[i + 1:j], start=i, end=j + 1)
            add(node)
            i = j + 1
        else:
            j = i
            while j < n and src[j] not in "<&":
                j += 1
            if stack:
                stack[-1].text += src[i:j]
            i = j
    for r in roots:
        mark(r, src)
    return roots


def mark(n, src):
    n.src = src
    for c in n.children:
        mark(c, src)


def children(n):
    return n.children


def is_leaf(n):
    return len(n.children) == 0


def text_of(n):
    return n.text.strip()


def attrs_key(n):
    return tuple(sorted(n.attrs))


def serialize(n):
    """片段原文（去首尾空白），保留 &amp;&amp; 等实体书写。"""
    return n.raw().strip()


def yq(s):
    return json.dumps(s, ensure_ascii=False)


class Ops:
    def __init__(self):
        # path -> [xml,...]
        self.adds = {}
        self.removes = {}
        self.settext = {}
        self.setattr = {}

    def add(self, path, node):
        self.adds.setdefault(path, []).append(serialize(node))

    def remove(self, path, node):
        self.removes.setdefault(path, []).append(node)

    def text(self, path, node, old, new):
        self.settext.setdefault(path, []).append((node, old, new))

    def attr(self, path, node, name, old, new):
        self.setattr.setdefault(path, []).append((node, name, old, new))


def diff_children(path, old, new, ops):
    """对齐 old/new 的子节点，把差异写进 ops。path 为当前父节点的 tag 路径(tuple)。"""
    oc, nc = children(old), children(new)
    used = [False] * len(oc)
    pairs = []          # (old_idx, new_node) 对位成功
    leftover_new = []
    # 第一轮：tag + 属性 + 叶子文本 完全一致
    for nn in nc:
        k = (nn.tag, attrs_key(nn), text_of(nn) if is_leaf(nn) else None)
        hit = -1
        for i, on in enumerate(oc):
            if used[i]:
                continue
            if (on.tag, attrs_key(on), text_of(on) if is_leaf(on) else None) == k:
                hit = i
                break
        if hit >= 0:
            used[hit] = True
            pairs.append((hit, nn))
        else:
            leftover_new.append(nn)
    # 第二轮：tag + 属性(用于识别"被改写"的节点)
    still_new = []
    for nn in leftover_new:
        k = (nn.tag, attrs_key(nn))
        hit = -1
        for i, on in enumerate(oc):
            if used[i]:
                continue
            if (on.tag, attrs_key(on)) == k:
                hit = i
                break
        if hit >= 0:
            used[hit] = True
            pairs.append((hit, nn))
        else:
            still_new.append(nn)
    # 递归已对位节点
    for i, nn in pairs:
        diff_node(path + (nn.tag,), oc[i], nn, ops)
    # 旧有新无 → 删除
    for i, on in enumerate(oc):
        if not used[i]:
            ops.remove(path, on)
    # 新有旧无 → 新增(按新文档顺序)
    for nn in still_new:
        ops.add(path, nn)


def diff_node(path, old, new, ops):
    """old/new 是 tag+属性已对位的同一节点。"""
    # 属性差异
    om, nm = dict(old.attrs), dict(new.attrs)
    for k, v in nm.items():
        if om.get(k) != v:
            ops.attr(path, new, k, om.get(k, ""), v)
    for k in om:
        if k not in nm:
            ops.attr(path, new, k, om[k], None)  # 目前用不到(删除属性)
    # 文本差异(仅叶子)
    if is_leaf(old) and is_leaf(new):
        if text_of(old) != text_of(new):
            ops.text(path, new, text_of(old), text_of(new))
        return
    diff_children(path, old, new, ops)


def anchor_of(path):
    """把 tag 路径拼成 anchor。文件级步骤：首段匹配文件根元素。"""
    return "/".join(path)


def header(out, file, path):
    """输出 file/anchor/where。anchor 段按 class 匹配，同 class 多实例会 fan-out；
    这里用 where.tag-glob 把 leaf 精确钉到生成时依据的那个 tag。"""
    out.append("    file: %s" % file)
    out.append("    anchor: %s" % anchor_of(path))
    if path:
        out.append("    where: { tag-glob: %s }" % yq(path[-1]))


def emit(ops, file, out):
    """把单个文件的 ops 输出为 steps。顺序：先删、再改、最后加。"""
    for path, nodes in ops.removes.items():
        out.append("  - name: %s 删除已停用节点" % file)
        header(out, file, path)
        out.append("    remove-node:")
        seen = set()
        for n in nodes:
            sel = {"tag": n.tag}
            if n.attrs:
                sel["attr"] = dict(n.attrs)
            if is_leaf(n) and text_of(n):
                sel["value"] = text_of(n)
            key = json.dumps(sel, sort_keys=True, ensure_ascii=False)
            if key in seen:
                continue
            seen.add(key)
            out.append("      - " + json.dumps(sel, ensure_ascii=False))
    for path, items in ops.setattr.items():
        out.append("  - name: %s 改写属性" % file)
        header(out, file, path[:-1])
        out.append("    set-attr:")
        for node, name, old, new in items:
            if new is None:
                continue
            d = {"tag": node.tag}
            if node.attrs:
                d["attr"] = dict(node.attrs)
            d["old"] = old
            d["name"] = name
            d["value"] = new
            out.append("      - " + json.dumps(d, ensure_ascii=False))
    for path, items in ops.settext.items():
        out.append("  - name: %s 改写文本" % file)
        header(out, file, path[:-1])
        out.append("    set-text:")
        seen = set()
        for node, old, new in items:
            d = {"tag": node.tag}
            if node.attrs:
                d["attr"] = dict(node.attrs)
            d["old"] = old
            d["value"] = new
            key = json.dumps(d, sort_keys=True, ensure_ascii=False)
            if key in seen:
                continue
            seen.add(key)
            out.append("      - " + json.dumps(d, ensure_ascii=False))
    for path, frags in ops.adds.items():
        out.append("  - name: %s 新增节点" % file)
        header(out, file, path)
        out.append("    add-xml:")
        for x in frags:
            out.append("      - xml: " + yq(x))


def main():
    out = []
    out.append("# Control 侧升级（腔室/互锁片段）。")
    out.append("# 由 hack/gen_upgrade_control.py 依据 config_old → config 的结构差异生成；")
    out.append("# 全部为文件级步骤(file:)，anchor 相对该文件根元素。")
    out.append("id: upgrade-control")
    out.append("description: Control 侧配置升级（加热器温差、EzZone 校准、互锁/互锁报警、机器人安全互锁）")
    out.append("version: 1")
    out.append("")
    out.append("steps:")

    used_anchors = []
    for f in FILES:
        old = parse(open("example-config/config_old/" + f, encoding="utf-8", errors="replace").read())[0]
        new = parse(open("example-config/config/" + f, encoding="utf-8", errors="replace").read())[0]
        if old.tag != new.tag:
            raise SystemExit("根标签不同: %s" % f)
        ops = Ops()
        diff_node((new.tag,), old, new, ops)
        for path in list(ops.adds) + list(ops.removes) + list(ops.settext) + list(ops.setattr):
            used_anchors.append((f, path))
        emit(ops, f, out)
        print("# %s: +%d节点 -%d节点 text=%d attr=%d" % (
            f, sum(len(v) for v in ops.adds.values()), sum(len(v) for v in ops.removes.values()),
            sum(len(v) for v in ops.settext.values()), sum(len(v) for v in ops.setattr.values())), file=sys.stderr)
    out.append("")
    # 只提示"实际用到的 anchor"里同层同名 tag 的情形：那种 anchor 会 fan-out 到多个实例。
    for f, path in used_anchors:
        d = {}
        cur = path
        # 逐层统计同名(用父目录的全量子节点数近似即可：直接看该层是否出现重复 tag)
        parent = path[:-1]
        if not parent:
            continue
        d.setdefault(cur[-1], 0)
    print("INFO anchors=%d" % len(used_anchors), file=sys.stderr)
    sys.stdout.write("\n".join(out))


if __name__ == "__main__":
    main()
