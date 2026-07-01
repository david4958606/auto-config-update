"""ops —— 幂等补丁原语。

每个原语返回 (changed: bool, message: str)：
  changed=True  → 确有改动(message 描述改了什么)
  changed=False → 已达目标，no-op(message 说明为何无需动作)
上层据此产出"完整而诚实"的语义 diff —— 每个声明的动作都有一行，绝不静默省略。

三种动作对应 feature step 的 add-node / add-method / remove-method：
  - add_node    : 建"对象/实例节点"     <Ped class="PhyChuck" .../>
  - add_method  : 加"方法调用"           <setCurPosVp type="method">...</setCurPosVp>
  - remove_method: 删匹配到的方法调用(按 名字+值)
"""
from __future__ import annotations

from lxml import etree


def add_node(parent, tag: str, cls: str, attrs: dict | None = None):
    """确保 parent 下存在 <tag class=cls ...>。按 tag 判重(同名已存在即幂等)。"""
    if parent.find(tag) is not None:
        return False, f"对象 <{tag}> 已存在"
    el = etree.SubElement(parent, tag)
    el.set("class", cls)
    for k, v in (attrs or {}).items():
        el.set(k, "" if v is None else str(v))
    return True, f"新增对象 <{tag} class={cls}>"


def add_method(anchor, name: str, value: str):
    """确保 anchor 下存在 <name type="method">value</name>。

    方法可"多次调用"(如 addDataEx 记录多个数据点)，故按 (名字+值) 判重：
    同名同值已存在 → no-op；否则新增一条。
    """
    for el in anchor.findall(name):
        if (el.text or "") == value:
            return False, f"方法 {name}({value}) 已存在"
    el = etree.SubElement(anchor, name)
    el.set("type", "method")
    el.text = value
    return True, f"新增方法 {name}({value})"


def remove_method(anchor, name: str, value: str):
    """删除 anchor 下匹配 (名字+值) 的方法调用；一个都没有 → no-op。"""
    hits = [el for el in anchor.findall(name) if (el.text or "") == value]
    for el in hits:
        anchor.remove(el)
    if hits:
        return True, f"删除方法 {name}({value})"
    return False, f"方法 {name}({value}) 不存在，无需删除"
