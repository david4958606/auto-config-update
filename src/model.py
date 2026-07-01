"""model —— 配置的解析 / 寻址层。

真实结构：Control_config.xml / IO_config.xml 用 XML 外部实体(<!ENTITY ... SYSTEM ...>)
把一批"无扩展名的片段文件"拼成一棵逻辑 <Control> / <IO> 树。片段本身是"实体体"，
可含多个顶层元素(如 IOBridge/IO_Motor 同时有 <Ch1> 和 <Ch4>)，并非独立 XML 文档；
且片段内部可能再引用共享实体(如 Control_Ch1 里 36 处 &Simulated_Ch1;)。

本层职责：
  - 从 master 的实体声明拿到"片段清单"与"实体声明子集"(真相源，不靠 glob)。
  - Control 侧可写：逐片段加载并【保留 &实体; 不展开】，编辑后原地回写、保留原格式。
  - 只读逻辑视图：把 /IO/... 或 /Control/... 逻辑路径解析成真实节点(带来源文件)。
"""
from __future__ import annotations

from pathlib import Path

from lxml import etree

ROOT = Path(__file__).resolve().parent.parent
CONFIG_DIR = ROOT / "config"
CONTROL_MASTER = CONFIG_DIR / "Control" / "Control_config.xml"
IO_MASTER = CONFIG_DIR / "IO_config.xml"

# IO 片段(只读、无内部实体)：可去空白
IO_PARSER = etree.XMLParser(remove_blank_text=True)
# master：装配整棵逻辑树(实体展开)，用于读取实体声明 / 只读逻辑视图
MASTER_PARSER = etree.XMLParser(load_dtd=True, resolve_entities=True, no_network=True)
# Control 片段(可写)：声明实体但【不展开】，从而回写时保留 &...;；且保留原格式(不去空白)
FRAG_PARSER = etree.XMLParser(load_dtd=True, resolve_entities=False, no_network=True)


# ── 片段清单 / 实体声明 ──────────────────────────────────────────────
def _dtd(master_path: Path):
    return etree.parse(str(master_path), MASTER_PARSER).docinfo.internalDTD


def fragment_files(master_path: Path):
    """返回 master 【正文】按顺序引用的腔室片段 [(entity_name, fragment_path)]。
    只算正文里 &X; 用到的实体；共享实体(如 Simulated_Ch1，仅被片段内部引用)不在此列。"""
    base = master_path.parent
    urls = {e.name: e.system_url for e in _dtd(master_path).iterentities() if e.system_url}
    root = etree.parse(str(master_path), FRAG_PARSER).getroot()   # 不展开 → 正文保留实体引用节点
    return [(ent.name, (base / urls[ent.name]).resolve())
            for ent in root.iter(etree.Entity) if ent.name in urls]


def _entity_doctype(master_path: Path) -> str:
    """把 master 的全部实体声明拼成一个 DOCTYPE 内部子集，供片段独立解析时声明实体
    (只声明、不解析)，这样 &Simulated_Ch1; 之类既不报未定义、又能原样保留回写。"""
    decls = [f'<!ENTITY {e.name} SYSTEM "{e.system_url}">'
             for e in _dtd(master_path).iterentities() if e.system_url]
    return "<!DOCTYPE _frag [\n" + "\n".join(decls) + "\n]>"


# ── Control 片段：实体保留式加载 / 原地回写 ─────────────────────────
def load_control_fragment(path: Path):
    """加载一个 Control 片段(单腔室根)，保留内部 &实体; 与原格式。
    返回其腔室根元素(可直接编辑)。"""
    text = Path(path).read_text(encoding="utf-8")
    doc = f"{_entity_doctype(CONTROL_MASTER)}\n<_frag>{text}</_frag>"
    wrapper = etree.fromstring(doc.encode("utf-8"), FRAG_PARSER)
    return wrapper[0]                      # 片段是单根：<_frag> 下唯一的腔室元素


def save_control_fragment(chamber_el, path: Path):
    """回写腔室根元素：元素级序列化天然保留 &实体; 且不带 DOCTYPE/包裹层。"""
    text = etree.tostring(chamber_el, encoding="unicode")
    path.write_text(text + "\n", encoding="utf-8")


# ── 只读逻辑视图：/IO/... 与 /Control/... 通用解析 ──────────────────
def _load_sections(master_path: Path, parser):
    """按 master 实体清单加载所有片段，展开成 [(fragment_path, section_element)]。
    多段片段(IO_Motor 有 Ch1/Ch4)会展开成多条。"""
    index = []
    doctype = _entity_doctype(master_path)
    for _name, fpath in fragment_files(master_path):
        text = Path(fpath).read_text(encoding="utf-8")
        wrapper = etree.fromstring(f"{doctype}\n<_frag>{text}</_frag>".encode("utf-8"), parser)
        for el in wrapper:
            index.append((fpath, el))
    return index


def build_indexes():
    """构造只读逻辑索引：{'IO': [...], 'Control': [...]}，键 = 逻辑根名。"""
    return {
        "IO": _load_sections(IO_MASTER, FRAG_PARSER),
        "Control": _load_sections(CONTROL_MASTER, FRAG_PARSER),
    }


def resolve_logical(indexes: dict, logical_path: str):
    """把 '/IO/Ch4/Ped/CurPosDI' 或 '/Control/Ch1/Vacuum/PgValve' 解析成 (fragment_path, node)。

    按首段(IO/Control)选索引，其余段在"腔室标签匹配"的片段段里逐个 find；
    命中即返回(可能落在 IO_Motor 等任意片段)，都找不到则 (None, None)。
    """
    parts = [p for p in logical_path.strip("/").split("/") if p]
    root_name, chamber, *rest = parts
    for fpath, root in indexes.get(root_name, []):
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
