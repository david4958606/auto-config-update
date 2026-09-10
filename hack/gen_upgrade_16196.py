#!/usr/bin/env python3
"""从 example-16196 的 config_old → config **结构差异**生成升级 feature。

用法（仓库根目录）：
    .venv/bin/python hack/gen_upgrade_16196.py

产物（按目录分组，均使用文件级步骤 file: / new-file:）：
    features/upgrade-16196-setup.yaml     Setup/*.xml、SysLog_config.xml
    features/upgrade-16196-io.yaml        IOBridge/*
    features/upgrade-16196-control.yaml   Control/*

设计要点与 hack/gen_upgrade_control.py 一致：不用 lxml（会吃掉 &amp;&amp;），自带带字节偏移的
极简分词器（注释/CDATA 整段跳过、实体作为不透明子节点、节点原文=源字节切片），保证 add-xml
逐字保真。差异对齐改为带评分的单调对齐（Needleman-Wunsch 思路）：

  - 完全一致（tag+属性值+文本）→ 递归核对；
  - tag+属性**键集合**一致（值变了/文本变了）→ 递归核对，落到 set-attr / set-text；
  - tag 不同或属性键集合不同 → 视为"旧删 + 新加"（add-xml 逐字插入）。

新增节点用 `before`/`after` 定位到"同一父节点下最近的、两侧都存在的兄弟"：同一锚点连续插入
会落在同一个原始后继之前，从而按声明顺序拼接（见 ops.InsertAfterNode）。

Recipe/ 下新增的 recipe 与 recipe 文件按需求**不处理**。
"""
import json
import os
import sys

ROOT = "example-16196"
OLD = os.path.join(ROOT, "config_old")
NEW = os.path.join(ROOT, "config")

# 需求明确：RecipeNamespace 新增 recipe 与对应 recipe 文件不处理。
SKIP_PREFIX = ("Recipe/",)

# master 文件：把片段拼成逻辑根，自身不是腔室片段(不参与逐腔室归并/腔室集合判定)。
MASTERS = {"Control/Control_config.xml", "IO_config.xml", "Driver_config.xml", "DataLog_config.xml"}

ZERO = """<?xml version="1.0" encoding="UTF-8"?>"""


class Node:
    __slots__ = ("tag", "attrs", "children", "is_entity", "ent", "start", "end",
                 "text", "src", "parts")

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
        self.parts = []  # 顺序化的 ("t", 文本) / ("&", 实体名)

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

    def add_part(node, kind, val):
        if stack:
            stack[-1].parts.append((kind, val))

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
                add(node)
                if not selfclose:
                    stack.append(node)
                i = j
        elif c == "&":
            j = src.find(";", i)
            if j < 0:
                j = i + 1
            ent = src[i + 1:j]
            node = Node(is_entity=True, ent=ent, start=i, end=j + 1)
            add(node)
            add_part(node, "&", ent)
            i = j + 1
        else:
            j = i
            while j < n and src[j] not in "<&":
                j += 1
            if stack:
                stack[-1].text += src[i:j]
            add_part(None, "t", src[i:j])
            i = j
    for r in roots:
        mark(r, src)
    return roots


def mark(n, src):
    n.src = src
    for c in n.children:
        mark(c, src)


# ───────────────────────── 匹配规则 ─────────────────────────

def pure_leaf(n):
    return not n.children


def has_element_child(n):
    return any(not c.is_entity for c in n.children)


def mixed(n):
    """含实体、且不含元素子节点 → 文本/实体混合叶子。"""
    return any(c.is_entity for c in n.children) and not has_element_child(n)


def mixed_sig(n):
    out = []
    for kind, val in n.parts:
        if kind == "&":
            out.append(("&", val))
        elif val.strip():
            out.append(("t", val.strip()))
    return tuple(out)


def attr_keys(n):
    return tuple(sorted(k for k, _ in n.attrs))


# 身份属性：这些属性的**取值**参与匹配(不同即视为"旧删+新加"，绝不改名)。
# 若把它们也当作可改写属性，会出现"把 Param 改名成另一个新增 Param 的名字"这类重复节点。
IDENTITY_ATTRS = ("name", "paramName", "index", "id")


def ident_attrs(n):
    return {k: v for k, v in n.attrs if k in IDENTITY_ATTRS}


def attrs_eq(a, b):
    return tuple(sorted(a.attrs)) == tuple(sorted(b.attrs))


def text_eq(a, b):
    return a.text.strip() == b.text.strip()


def matchable(o, n):
    if o.is_entity or n.is_entity:
        return o.is_entity and n.is_entity and o.ent == n.ent
    if o.tag != n.tag:
        return False
    if attr_keys(o) != attr_keys(n):
        return False
    if ident_attrs(o) != ident_attrs(n):
        return False
    if mixed(o) or mixed(n):
        return mixed_sig(o) == mixed_sig(n)
    return True


def exact_match(o, n):
    if not matchable(o, n):
        return False
    if o.is_entity:
        return True
    return attrs_eq(o, n) and (text_eq(o, n) if pure_leaf(o) else True)


def align(oc, nc):
    """单调对齐(最大化精确匹配优先的得分)，返回 [(old_idx,new_idx)]。"""
    m, k = len(oc), len(nc)
    dp = [[0] * (k + 1) for _ in range(m + 1)]
    for i in range(1, m + 1):
        for j in range(1, k + 1):
            best = dp[i - 1][j] if dp[i - 1][j] >= dp[i][j - 1] else dp[i][j - 1]
            if matchable(oc[i - 1], nc[j - 1]):
                s = 10 if exact_match(oc[i - 1], nc[j - 1]) else 1
                if dp[i - 1][j - 1] + s > best:
                    best = dp[i - 1][j - 1] + s
            dp[i][j] = best
    pairs = []
    i, j = m, k
    while i > 0 and j > 0:
        if matchable(oc[i - 1], nc[j - 1]):
            s = 10 if exact_match(oc[i - 1], nc[j - 1]) else 1
            if dp[i][j] == dp[i - 1][j - 1] + s:
                pairs.append((i - 1, j - 1))
                i -= 1
                j -= 1
                continue
        if dp[i - 1][j] >= dp[i][j - 1]:
            i -= 1
        else:
            j -= 1
    pairs.reverse()
    return pairs


# ───────────────────────── 差异收集 ─────────────────────────

class FileOps:
    def __init__(self):
        self.adds = {}     # parent_path -> [(new_node, ref_path, ref_old_node, ref_after)]
        self.removes = {}  # parent_path -> [old_node]
        self.settext = {}  # parent_path -> [(old_node, new_text)]
        self.setattr = {}  # parent_path -> [(old_node, name, old_val, new_val)]

    def add(self, path, node, ref, ref_after):
        self.adds.setdefault(path, []).append((node, ref, ref_after))

    def remove(self, path, node):
        self.removes.setdefault(path, []).append(node)

    def text(self, path, old, new):
        self.settext.setdefault(path, []).append((old, new))

    def attr(self, path, old, name, old_val, new_val):
        self.setattr.setdefault(path, []).append((old, name, old_val, new_val))


def diff_children(path, old, new, ops):
    oc, nc = old.children, new.children
    pairs = align(oc, nc)
    matched_old = {i for i, _ in pairs}
    matched_new = {j for _, j in pairs}

    # DP 只能做单调对齐：位置被挪动过的节点会被判成"旧删+新加"。这里补一轮"跨位置配对"：
    #   - 第 1 轮：完全一致(纯挪位) → 就地保留，不删不加；
    #   - 第 2 轮：带身份属性(Param 的 name / Value 的 paramName / index / id)且可匹配 →
    #     就地 set-text/set-attr。这一条专为 Setup 的 Param/Value 而设：两条序列必须同步挪动，
    #     若各自 remove+add 会错位；同理也避免"remove+add 同名节点"在二次 apply 互相抵消。
    # 普通方法(无身份属性)的挪动则交给 remove+add，按 before/after 落到目标位置，顺序更贴近目标。
    pending_old = [i for i in range(len(oc)) if i not in matched_old]
    pending_new = [j for j in range(len(nc)) if j not in matched_new]
    for predicate in (lambda o, n: matchable(o, n) and attrs_eq(o, n) and text_eq(o, n),
                      lambda o, n: matchable(o, n) and ident_attrs(n)):
        used_new = set()
        still_old = []
        for i in pending_old:
            hit = -1
            for j in pending_new:
                if j in used_new:
                    continue
                if predicate(oc[i], nc[j]):
                    hit = j
                    break
            if hit < 0:
                still_old.append(i)
                continue
            used_new.add(hit)
            matched_old.add(i)
            matched_new.add(hit)
            pairs.append((i, hit))
        pending_old = still_old
        pending_new = [j for j in pending_new if j not in used_new]

    def ref_of(j):
        """给新节点 j 找插入参照：优先最近的前置已配对**元素**兄弟(after)，否则最近的
        后继已配对元素兄弟(before)。实体/注释不能作定位点(选择器 tag 为空会误匹配)。"""
        usable = lambda jj: (not nc[jj].is_entity) and nc[jj].tag != ""
        for jj in range(j - 1, -1, -1):
            if jj in matched_new and usable(jj):
                return ("after", jj)
        for jj in range(j + 1, len(nc)):
            if jj in matched_new and usable(jj):
                return ("before", jj)
        return (None, None)

    for i, j in pairs:
        diff_node(path, oc[i], nc[j], ops)
    for i, on in enumerate(oc):
        if i not in matched_old:
            ops.remove(path, on)
    for j, nn in enumerate(nc):
        if j not in matched_new:
            kind, ref_j = ref_of(j)
            ref = nc[ref_j] if ref_j is not None else None
            ops.add(path, nn, ref, kind == "after")


def diff_root(old, new, ops):
    """文件根节点：anchor 只能定位到根自身，故根上的属性改写无法用现有原语表达。"""
    om, nm = dict(old.attrs), dict(new.attrs)
    for k, v in nm.items():
        if k in om and om[k] != v:
            raise SystemExit("根节点 <%s> 属性 %s 变化，现有原语无法表达" % (old.tag, k))
    diff_children((new.tag,), old, new, ops)


def diff_node(path, old, new, ops):
    """path 是 old/new 的**父节点**路径(anchor 用)，选择器定位到 old/new 自身。"""
    if old.is_entity or new.is_entity:
        return
    om, nm = dict(old.attrs), dict(new.attrs)
    for k, v in nm.items():
        if k in om and om[k] != v:
            ops.attr(path, old, k, om[k], v)
    if pure_leaf(old) and pure_leaf(new):
        if not text_eq(old, new):
            ops.text(path, old, new)
        return
    if mixed(old) or mixed(new):
        return  # matchable 已保证混合内容一致
    diff_children(path + (new.tag,), old, new, ops)


# ───────────────────────── YAML 输出 ─────────────────────────

def yq(s):
    return json.dumps(s, ensure_ascii=False)


def sel_of(node):
    d = {"tag": node.tag}
    if node.attrs:
        d["attr"] = {k: v for k, v in node.attrs}
    if pure_leaf(node) and node.text.strip():
        d["value"] = node.text.strip()
    return d


# ───────────────────── 步骤归并：per-file → 逐腔室参数化 ─────────────────────
#
# 直接按文件生成步骤会得到 Ch1/Ch2/… 一长串复制品，既难维护也不"可复用"。这里做一次归并：
#   - 类归并：同一 root class 的各腔室（如 PVD = Ch1/Ch2/Ch5/Ch6）改动**同构**时，合并为一个
#     `anchor: <Class>/…` 的腔室级步骤，模板里用 `${<Class>}` 占位（引擎按 class 绑定→腔室名）；
#   - 腔室归并：根没有 class（如 Interlock_*）或跨 class 时，用保留占位符 `${Chamber}`
#     （引擎绑定=当前腔室标签）合并；仅当该 anchor 路径在**其它腔室**里解析不到时才合并，
#     这样非成员腔室会因 anchor 不匹配而自动跳过，安全。
#
# 归并是"同一份声明、逐腔室替换腔室名"，与 features/add-pedcurpos-dataex.yaml 的写法一致。

SENT = "\x00CH\x00"  # 腔室名占位哨兵（仅在生成器内部使用）


def paths_of(ops):
    keys = set(ops.adds) | set(ops.removes) | set(ops.settext) | set(ops.setattr)
    return sorted(keys, key=lambda p: (len(p), p))


def action_data(ops, path):
    """把一个父 anchor 下的动作收集成可 JSON 化的字典（与 emit 时的 YAML 一一对应）。"""
    d = {}
    if path in ops.removes:
        d["remove-node"] = [sel_of(n) for n in ops.removes[path]]
    if path in ops.settext:
        items = []
        for old, new in ops.settext[path]:
            e = {"tag": old.tag, "old": old.text.strip(), "value": new.text.strip()}
            if old.attrs:
                e["attr"] = {k: v for k, v in old.attrs}
            items.append(e)
        d["set-text"] = items
    if path in ops.setattr:
        changed = {}
        for old, name, _o, _n in ops.setattr[path]:
            changed.setdefault(id(old), set()).add(name)
        items = []
        for old, name, old_val, new_val in ops.setattr[path]:
            e = {"tag": old.tag, "old": old_val, "name": name, "value": new_val}
            keep = {k: v for k, v in old.attrs if k not in changed[id(old)]}
            if keep:
                e["attr"] = keep
            items.append(e)
        d["set-attr"] = items
    if path in ops.adds:
        items = []
        for node, ref, ref_after in ops.adds[path]:
            e = {"xml": node.raw().strip()}
            if ref is not None:
                e["after" if ref_after else "before"] = sel_of(ref)
            items.append(e)
        d["add-xml"] = items
    return d


def map_strings(obj, fn):
    if isinstance(obj, dict):
        return {k: map_strings(v, fn) for k, v in obj.items()}
    if isinstance(obj, list):
        return [map_strings(v, fn) for v in obj]
    if isinstance(obj, str):
        return fn(obj)
    return obj


def canon_path(path, chamber):
    return tuple(SENT if seg == chamber else seg for seg in path)


def build_anchor(domain, root_seg, cpath, ph):
    segs = [root_seg] + [ph if s == SENT else s for s in cpath[1:]]
    if domain:
        segs = [domain] + segs
    return "/".join(segs)


# fetch_by / match_seg / path_matches 复刻 internal/anchor 的解析：按 class 优先、无果回退 tag，
# 逐层在子孙里找。用于判断"某个 anchor 路径在别的腔室里是否也存在"（存在则不能跨腔室归并）。
def fetch_by(node, include_self, pred):
    out = []

    def walk(n, self_):
        if n.is_entity:
            return
        if self_ and pred(n):
            out.append(n)
        for c in n.children:
            walk(c, True)

    walk(node, include_self)
    return out


def match_seg(node, name, include_self):
    hits = fetch_by(node, include_self, lambda n: dict(n.attrs).get("class", "") == name)
    if hits:
        return hits
    return fetch_by(node, include_self, lambda n: n.tag == name)


def path_matches(root, segs):
    if not segs:
        return False
    frontier = match_seg(root, segs[0], True)
    for s in segs[1:]:
        nxt = []
        for n in frontier:
            nxt.extend(match_seg(n, s, False))
        frontier = nxt
        if not frontier:
            return False
    return bool(frontier)


_ROOT_INFO = {}


def root_info(path):
    """返回 (class, 根标签, 根节点列表)。

    多根片段(如 Driver_Ch1 同时含容器 <Ch1> 与驱动 <Ch1>)只要各根标签一致，也能按
    ${Chamber} 归并；标签不一致(如 Control_EFEM 的 LightTower/LP1/…/ATR)则不可归并。
    """
    if path in _ROOT_INFO:
        return _ROOT_INFO[path]
    with open(path, "rb") as f:
        src = f.read().decode("utf-8", "replace")
    roots = [r for r in parse(src) if not r.is_entity]
    if not roots:
        info = ("", "", [])
    elif len(roots) == 1:
        info = (dict(roots[0].attrs).get("class", ""), roots[0].tag, roots)
    else:
        info = ("", roots[0].tag, roots)
    _ROOT_INFO[path] = info
    return info


class Group:
    """一组 feature（setup/io/control）的待输出内容。"""

    def __init__(self, name, merge):
        self.name = name
        self.merge = merge          # setup 的 file: 步骤不属于任何 master，不参与归并
        self.universe = []          # 组内全部文件（判断 class 成员/安全范围）
        self.changed = {}           # rel -> ops
        self.newfiles = []          # (rel, content)
        self.domain = {}            # rel -> "" | "IO" | "IOBridge"

    def domain_of(self, rel):
        return self.domain.get(rel, "")


def emit_group(g, control_chambers):
    lines = []
    info = {rel: root_info(os.path.join(NEW, rel)) for rel in g.universe}
    data, orig = {}, {}
    for rel, ops in g.changed.items():
        _cls, tag, roots = info.get(rel, ("", "", []))
        if not roots or any(r.tag != tag for r in roots):
            continue  # 多根且标签不一致 → 不参与归并（仍走文件级步骤）
        data[rel] = {}
        orig[rel] = {}
        for path in paths_of(ops):
            d = map_strings(action_data(ops, path), lambda s, t=tag: s.replace(t, SENT))
            cp = canon_path(path, tag)
            data[rel][cp] = json.dumps(d, sort_keys=True, ensure_ascii=False)
            orig[rel][cp] = path
    used = {rel: set() for rel in data}

    def in_scope(domain):
        out = []
        for rel in g.universe:
            if g.domain_of(rel) != domain:
                continue
            _cls, tag, roots = info[rel]
            if not roots or any(r.tag != tag for r in roots):
                continue
            if domain and tag not in control_chambers:
                continue  # 该域下"根标签不是腔室"的片段不被腔室循环路由
            out.append(rel)
        return out

    # 1) 类归并（同一 root class 的腔室改动同构 → 合并）
    if g.merge:
        members_by_class = {}
        for rel in g.universe:
            cls, _tag, roots = info[rel]
            if len(roots) == 1 and cls:
                members_by_class.setdefault((g.domain_of(rel), cls), set()).add(rel)
        for domain, cls in sorted(members_by_class):
            members = members_by_class[(domain, cls)]
            changed = [r for r in sorted(members) if r in data]
            if not changed or set(changed) != members:
                continue  # 该 class 并非每个腔室都改了 → 保守起见不归并
            common = set(data[changed[0]])
            for r in changed[1:]:
                common &= set(data[r])
            for cpath in sorted(common):
                sigs = {r: data[r][cpath] for r in changed}
                if len(set(sigs.values())) != 1:
                    continue
                ph = "${%s}" % cls
                anchor = build_anchor(domain, cls, cpath, ph)
                body = map_strings(json.loads(next(iter(sigs.values()))), lambda s: s.replace(SENT, ph))
                lines.append("  - name: %s 腔室（%s）升级：%s" % (g.name, cls, anchor))
                lines.append("    anchor: %s" % anchor)
                lines.extend(render_actions(body))
                lines.append("")
                for r in changed:
                    used[r].add(cpath)
            if domain and cls:  # 调试提示
                pass

        # 2) 腔室归并（根无 class / 跨 class；仅当 anchor 在非成员腔室解析不到时）
        cand = {}
        for rel in sorted(data):
            for cpath in data[rel]:
                if cpath in used[rel]:
                    continue
                cand.setdefault((g.domain_of(rel), cpath, data[rel][cpath]), []).append(rel)
        for domain, cpath, sig in sorted(cand):
            members = cand[(domain, cpath, sig)]
            if len(members) < 2:
                continue
            unsafe = False
            for rel in in_scope(domain):
                if rel in members:
                    continue
                _cls, tag, roots = info[rel]
                segs = [tag if s == SENT else s for s in cpath]
                if any(path_matches(r, segs) for r in roots):
                    unsafe = True
                    break
            if unsafe:
                continue
            ph = "${Chamber}"
            anchor = build_anchor(domain, ph, cpath, ph)
            body = map_strings(json.loads(sig), lambda s: s.replace(SENT, ph))
            lines.append("  - name: %s 腔室升级：%s" % (g.name, anchor))
            lines.append("    anchor: %s" % anchor)
            lines.extend(render_actions(body))
            lines.append("")
            for r in members:
                used[r].add(cpath)

    # 3) 未能归并的，仍按文件级步骤逐条输出（多根片段不参与归并，也在这里补上）
    for rel in sorted(g.changed):
        ops = g.changed[rel]
        if rel in data:
            todo = [orig[rel][cp] for cp in data[rel] if cp not in used[rel]]
        else:
            todo = paths_of(ops)
        for path in sorted(todo, key=lambda p: (len(p), p)):
            lines.append("  - name: %s 升级" % rel)
            lines.append("    file: %s" % rel)
            lines.append("    anchor: %s" % "/".join(path))
            lines.extend(render_actions(action_data(ops, path)))
            lines.append("")

    for rel, content in sorted(g.newfiles):
        lines.append("  - name: 新建 %s" % rel)
        lines.append("    new-file: %s" % rel)
        lines.append("    content: " + yq(content))
        lines.append("")
    return lines


def render_actions(data):
    lines = []
    if "remove-node" in data:
        lines.append("    remove-node:")
        for e in data["remove-node"]:
            lines.append("      - " + json.dumps(e, ensure_ascii=False))
    if "set-text" in data:
        lines.append("    set-text:")
        for e in data["set-text"]:
            lines.append("      - " + json.dumps(e, ensure_ascii=False))
    if "set-attr" in data:
        lines.append("    set-attr:")
        for e in data["set-attr"]:
            lines.append("      - " + json.dumps(e, ensure_ascii=False))
    if "add-xml" in data:
        lines.append("    add-xml:")
        for e in data["add-xml"]:
            lines.append("      - " + json.dumps(e, ensure_ascii=False))
    return lines


def write_feature(path, fid, desc, steps):
    out = []
    out.append("# 由 hack/gen_upgrade_16196.py 依据 example-16196 config_old → config 差异生成。")
    out.append("# 静态 feature：运行时只依赖 primitives，不读取目标 config/。")
    out.append("id: %s" % fid)
    out.append("description: %s" % desc)
    out.append("version: 1")
    out.append("")
    out.append("steps:")
    out.extend(steps)
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(out).rstrip() + "\n")
    print("生成 %s (%d 行)" % (path, len(out)), file=sys.stderr)


def rel_files(base):
    out = set()
    for dirpath, _dirs, files in os.walk(base):
        for fn in files:
            p = os.path.join(dirpath, fn)
            out.add(os.path.relpath(p, base).replace(os.sep, "/"))
    return out


def domain_for(rel):
    """腔室级步骤要走哪个域：Control 片段缺省 Control；IOBridge 下 IO_* 属 IO、Driver_* 属 IOBridge。"""
    base = os.path.basename(rel)
    if rel.startswith("IOBridge/"):
        if base.startswith("Driver_"):
            return "IOBridge"
        return "IO"
    return ""


def main():
    old_files = rel_files(OLD)
    new_files = rel_files(NEW)
    keep = lambda r: not r.startswith(SKIP_PREFIX)

    groups = {
        "setup": Group("Setup", merge=False),
        "io": Group("IOBridge", merge=True),
        "control": Group("Control", merge=True),
    }
    stats = []

    def group_of(rel):
        if rel.startswith("Setup/") or rel == "SysLog_config.xml":
            return "setup"
        if rel.startswith("IOBridge/"):
            return "io"
        if rel.startswith("Control/"):
            return "control"
        return "setup"

    for rel in sorted(new_files):
        if not keep(rel):
            continue
        g = groups[group_of(rel)]
        if rel not in MASTERS:  # master 文件不是腔室片段，不参与"腔室集合"判定
            g.universe.append(rel)
        g.domain[rel] = domain_for(rel)
        new_path = os.path.join(NEW, rel)
        if rel not in old_files:
            # 以二进制读入，保留原文件的 CRLF/LF 与行尾换行 —— new-file 是逐字落盘。
            with open(new_path, "rb") as f:
                content = f.read().decode("utf-8")
            g.newfiles.append((rel, content))
            stats.append("%-45s NEW" % rel)
            continue
        old_path = os.path.join(OLD, rel)
        with open(old_path, encoding="utf-8", errors="replace") as f:
            old_src = f.read()
        with open(new_path, encoding="utf-8", errors="replace") as f:
            new_src = f.read()
        if old_src == new_src:
            continue
        oroots, nroots = parse(old_src), parse(new_src)
        if len(oroots) != len(nroots) or any(
                o.tag != n.tag or o.is_entity != n.is_entity
                for o, n in zip(oroots, nroots)):
            raise SystemExit("顶层节点集合不一致(需人工处理): %s" % rel)
        ops = FileOps()
        for o, n in zip(oroots, nroots):
            diff_root(o, n, ops)
        if not (ops.adds or ops.removes or ops.settext or ops.setattr):
            continue
        g.changed[rel] = ops
        stats.append("%-45s +%d -%d txt=%d attr=%d" % (
            rel, sum(len(v) for v in ops.adds.values()),
            sum(len(v) for v in ops.removes.values()),
            sum(len(v) for v in ops.settext.values()),
            sum(len(v) for v in ops.setattr.values())))

    # 只在 old 侧存在的文件：当前数据没有(仅提示)。
    for rel in sorted(old_files - new_files):
        if keep(rel):
            print("警告: %s 只存在于 config_old（未生成删除文件步骤）" % rel, file=sys.stderr)

    # 腔室集合取自 Control 侧（IO/IOBridge 片段按腔室标签挂载到这些腔室上）。
    control_chambers = set()
    for rel in groups["control"].universe:
        cls, tag, roots = root_info(os.path.join(NEW, rel))
        if roots:
            control_chambers.add(tag)

    write_feature("features/upgrade-16196-setup.yaml", "upgrade-16196-setup",
                  "Setup/SysLog 文件级升级（Param/Value 增删与改写、ProcessDataStableTime/GasFlowCompens 新文件）",
                  emit_group(groups["setup"], control_chambers))
    write_feature("features/upgrade-16196-io.yaml", "upgrade-16196-io",
                  "IOBridge 升级（量程/别名/描述子改写、停用节点删除；逐腔室参数化）",
                  emit_group(groups["io"], control_chambers))
    write_feature("features/upgrade-16196-control.yaml", "upgrade-16196-control",
                  "Control 升级（补偿器、PMacro、稳定时间、互锁报警；逐腔室参数化）",
                  emit_group(groups["control"], control_chambers))

    print("\n差异汇总：", file=sys.stderr)
    for s in stats:
        print("  " + s, file=sys.stderr)


if __name__ == "__main__":
    main()
