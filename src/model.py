"""model —— 配置的解析 / 寻址层。

真实结构：Control_config.xml / IO_config.xml 用 XML 外部实体(<!ENTITY ... SYSTEM ...>)
把一批"无扩展名的片段文件"拼成一棵逻辑 <Control> / <IO> 树。片段本身是"实体体"，
可含多个顶层元素(如 IOBridge/IO_Motor 同时有 <Ch1> 和 <Ch4>)，并非独立 XML 文档。

本层职责：
  - 从 master 的实体声明拿到"片段清单"(真相源，不靠 glob 目录)。
  - IO 侧只读：装配出逻辑 IO 索引，把逻辑路径解析成真实节点(带来源文件)。
  - Control 侧可写：逐片段独立解析、原地保存(单腔室根)。
"""
from __future__ import annotations

from pathlib import Path

from lxml import etree

ROOT = Path(__file__).resolve().parent.parent
CONFIG_DIR = ROOT / "config"
CONTROL_MASTER = CONFIG_DIR / "Control" / "Control_config.xml"
IO_MASTER = CONFIG_DIR / "IO_config.xml"

PARSER = etree.XMLParser(remove_blank_text=True)
# 读 master 的实体声明需要 load_dtd + resolve_entities(否则 &IO_Ch1; 会报未定义)
MASTER_PARSER = etree.XMLParser(load_dtd=True, resolve_entities=True, no_network=True)


# ── 片段清单 / 解析 / 落盘 ───────────────────────────────────────────
def fragment_files(master_path: Path):
    """读 master 的实体声明，按声明顺序返回 [(entity_name, fragment_path)]。"""
    dtd = etree.parse(str(master_path), MASTER_PARSER).docinfo.internalDTD
    base = master_path.parent
    return [(e.name, (base / e.system_url).resolve())
            for e in dtd.iterentities() if e.system_url]


def load_xml(path: Path):
    """把一个"单根"片段(如 Control_Ch4)当独立文档解析，便于原地编辑/保存。"""
    return etree.parse(str(path), PARSER)


def save_xml(tree, path: Path):
    etree.indent(tree, space="    ")
    path.write_text(etree.tostring(tree, encoding="unicode") + "\n", encoding="utf-8")


def load_fragment_sections(path: Path):
    """片段是"实体体"(可能多个顶层元素、无 XML 声明)：包一层再解析，
    返回其中每个顶层元素(= 一个腔室段)。"""
    text = Path(path).read_text(encoding="utf-8")
    wrapper = etree.fromstring(f"<_frag>{text}</_frag>", PARSER)
    return list(wrapper)


# ── 逻辑 IO 视图(只读) ───────────────────────────────────────────────
def build_io_index():
    """按 master 实体清单加载所有 IO 片段，返回 [(fragment_path, chamber_element)]。
    IO_Motor 这类多段片段会展开成多条 —— 路径解析只看腔室标签，不看文件名。"""
    index = []
    for _name, fpath in fragment_files(IO_MASTER):
        for el in load_fragment_sections(fpath):
            index.append((fpath, el))
    return index


def resolve_io_logical(io_index, io_logical_path: str):
    """把逻辑路径 '/IO/Ch4/Ped/CurPosDI' 解析成 (fragment_path, node)。

    在所有腔室标签匹配的片段段里逐个尝试剩余路径(可能落在 IO_Motor 等任意片段)，
    命中即返回；都找不到则返回 (None, None)。
    """
    parts = [p for p in io_logical_path.strip("/").split("/") if p]
    assert parts and parts[0] == "IO", f"非法 IO 路径: {io_logical_path}"
    chamber, *rest = parts[1:]
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
