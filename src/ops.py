"""ops —— 幂等补丁原语。

每个原语返回 (changed: bool, message: str, edit)：
  changed=True  → 确有改动；False → 已达目标(no-op)。
  edit          → 供外科式写盘用的编辑记录 ('insert',parent,el)/('delete',parent,[els])；
                  no-op 时为 None。
上层据此产出"完整而诚实"的语义 diff —— 每个声明的动作都出一行。

三种动作对应 feature step 的 add-node / add-method / remove-method：
  - add_node    : 建"对象/实例节点"   <IonGauge class="PhyGauge" .../>
  - add_method  : 加"方法调用"         有值 <setModeVp ...>路径</setModeVp>
                                       无值 <enableModeSwitch type="method"/>
  - remove_method: 删匹配到的方法调用(按 名字[+值])

插入新节点时按"真实缩进深度"补空白，保证大文件里 diff 只出现在新增处。
"""
from __future__ import annotations

from lxml import etree


def _real_depth(el) -> int:
    """元素在真实文件里的缩进深度(腔室根=0)。减去解析时套的 <_frag> 包裹层。"""
    n = 0
    p = el.getparent()
    while p is not None:
        n += 1
        p = p.getparent()
    return n - 1


def _append_indented(parent, el):
    """把 el 追加为 parent 末子，并维持既有缩进风格(4 空格/层)。"""
    d = _real_depth(parent)
    child_indent = "\n" + "    " * (d + 1)   # 子节点行首缩进
    close_indent = "\n" + "    " * d         # parent 闭合标签行首缩进
    kids = list(parent)
    if kids:
        kids[-1].tail = child_indent
    else:
        parent.text = child_indent
    el.tail = close_indent
    parent.append(el)


def add_node(parent, tag: str, cls: str, attrs: dict | None = None):
    """确保 parent 下存在 <tag class=cls ...>。按 tag 判重(同名已存在即幂等)。
    attrs 的值已由上层做过占位符替换。"""
    if parent.find(tag) is not None:
        return False, f"对象 <{tag}> 已存在", None
    el = etree.Element(tag)
    el.set("class", cls)
    for k, v in (attrs or {}).items():
        el.set(k, "" if v is None else str(v))
    _append_indented(parent, el)
    return True, f"新增对象 <{tag} class={cls}>", ("insert", parent, el)


def add_method(anchor, name: str, value: str | None = None):
    """确保 anchor 下存在 <name type="method">value</name>。

    有值：按 (名字+值) 判重(方法可多次调用，如 addDataEx)。
    无值：标志型方法(如 enableModeSwitch)，按名判重，序列化为自闭合 <name type="method"/>。
    """
    if value is None:
        if anchor.find(name) is not None:
            return False, f"方法 {name}() 已存在", None
    else:
        for el in anchor.findall(name):
            if (el.text or "") == value:
                return False, f"方法 {name}({value}) 已存在", None
    el = etree.Element(name)
    el.set("type", "method")
    if value is not None:
        el.text = value
    _append_indented(anchor, el)
    msg = f"新增方法 {name}()" if value is None else f"新增方法 {name}({value})"
    return True, msg, ("insert", anchor, el)


def remove_method(anchor, name: str, value: str):
    """删除 anchor 下匹配 (名字+值) 的方法调用；一个都没有 → no-op。"""
    hits = [el for el in anchor.findall(name) if (el.text or "") == value]
    if hits:
        edit = ("delete", anchor, list(hits))   # 记录后再从树上移除(sourceline 仍保留)
        for el in hits:
            anchor.remove(el)
        return True, f"删除方法 {name}({value})", edit
    return False, f"方法 {name}({value}) 不存在，无需删除", None
