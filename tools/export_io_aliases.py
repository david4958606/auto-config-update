#!/usr/bin/env python3
"""把各腔室同名别名的 IO 信号定义并排导出成一张表，便于人工比对。

扫描 config/IOBridge/IO_* 中所有带 alias 属性的信号节点，把别名里的腔室名
归一成 ChX（如 /IO/Ch2Exports/CG_PressureAI -> /IO/ChXExports/CG_PressureAI），
按“通用别名 + 腔室”每个信号一行，列出 dataType、accessMode、Bd、Ch、Min、Max、
Accuracy、Unit、DescriptorList 等字段。只导出，不做差异判断。

用法：
    python3 tools/export_io_aliases.py                          # 输出 io_aliases.xlsx
    python3 tools/export_io_aliases.py -o io_aliases.csv        # 按扩展名输出 CSV
    python3 tools/export_io_aliases.py --alias CG_PressureAI    # 只导出匹配的别名
    python3 tools/export_io_aliases.py --chamber-regex 'Ch[0-9A-F]|LA|LB'
    python3 tools/export_io_aliases.py --config /path/to/config

只依赖 Python 3 标准库。
"""
import argparse
import csv
import fnmatch
import glob
import os
import re
import sys
import xml.etree.ElementTree as ET
import zipfile
from xml.sax.saxutils import escape

DEFAULT_CHAMBER_REGEX = r"Ch[0-9A-Z]"

# 固定列：(表头, 字段, xlsx 列宽)。信号字段列在其后动态追加。
BASE_COLUMNS = [
    ("通用别名", "generic", 46),
    ("腔室", "chamber", 8),
    ("别名", "alias", 46),
    ("物理路径", "path", 50),
    ("来源文件", "file", 14),
    ("已启用", "enabled", 8),
    ("dataType", "@dataType", 9),
    ("accessMode", "@accessMode", 11),
]
# 常见字段的列顺序与列宽；其余子元素/属性按字母序排在后面。
KNOWN_FIELDS = [
    ("Bd", 8), ("Ch", 8), ("Min", 10), ("Max", 10), ("Accuracy", 10), ("Unit", 8),
    ("DescriptorList", 50), ("MaxLength", 10), ("setSimulated", 12),
]
SKIP_ATTRS = ("alias",)

ENTITY_DECL = re.compile(r'<!ENTITY\s+(\S+)\s+SYSTEM\s+"([^"]+)"\s*>')
ENTITY_REF = re.compile(r"&(?!(?:amp|lt|gt|quot|apos|#\w+);)([\w.-]+);")
COMMENT = re.compile(r"<!--.*?-->", re.S)
XML_DECL = re.compile(r"<\?xml.*?\?>", re.S)


def warn(message):
    print("警告: " + message, file=sys.stderr)


def resolve_config_dir(path):
    """接受 config/ 本身，或外层还包了一层 config/ 的目录。"""
    if os.path.isfile(os.path.join(path, "IO_config.xml")):
        return path
    nested = os.path.join(path, "config")
    if os.path.isfile(os.path.join(nested, "IO_config.xml")):
        return nested
    raise SystemExit("错误: %s 下没有 IO_config.xml" % path)


def load_master(config_dir):
    """返回 (小片段实体 {名: 内容}, 已启用的 IO 片段文件名集合)。"""
    with open(os.path.join(config_dir, "IO_config.xml"), encoding="utf-8") as f:
        raw = f.read()
    text = COMMENT.sub("", raw)
    entities, fragments = {}, {}
    for name, rel in ENTITY_DECL.findall(text):
        path = os.path.normpath(os.path.join(config_dir, rel))
        if not os.path.isfile(path):
            continue
        if os.path.dirname(path) == os.path.join(config_dir, "IOBridge"):
            fragments[name] = os.path.basename(path)
        else:
            with open(path, encoding="utf-8") as f:
                entities[name] = f.read()
    body = text.split("]>", 1)[-1]
    enabled = {fragments[n] for n in ENTITY_REF.findall(body) if n in fragments}
    return entities, enabled


def parse_fragment(path, entities):
    """解析可能含多个根节点、引用外部实体的片段，返回包裹后的根。"""
    with open(path, encoding="utf-8") as f:
        text = XML_DECL.sub("", COMMENT.sub("", f.read()))

    missing = set()

    def resolve(match):
        name = match.group(1)
        if name not in entities and name not in missing:
            missing.add(name)
            warn("%s: 实体 &%s; 未声明或文件缺失，已忽略" % (os.path.basename(path), name))
        return entities.get(name, "")

    return ET.fromstring("<fragment>" + ENTITY_REF.sub(resolve, text) + "</fragment>")


def clean(text):
    return " ".join((text or "").split())


def signal_fields(elem):
    """信号的属性（带 @ 前缀）和子元素文本；同名子元素用换行合并。"""
    fields = {}
    for key, value in elem.attrib.items():
        if key not in SKIP_ATTRS:
            fields["@" + key] = value
    for child in elem:
        if len(child):
            value = "<%d 个子节点>" % len(child)
        else:
            value = clean(child.text)
        if child.tag in fields:
            fields[child.tag] += "\n" + value
        else:
            fields[child.tag] = value
    return fields


def walk(elem, parents, filename, matcher, rows):
    for child in elem:
        alias = (child.get("alias") or "").strip()
        if alias:
            match = matcher.match(alias)
            if match:
                row = signal_fields(child)
                row.update({
                    "generic": alias[:match.start(1)] + "ChX" + alias[match.end(1):],
                    "chamber": match.group(1),
                    "alias": alias,
                    "path": "/IO/" + "/".join(parents + [child.tag]),
                    "file": filename,
                })
                rows.append(row)
        walk(child, parents + [child.tag], filename, matcher, rows)


def collect(config_dir, chamber_regex=DEFAULT_CHAMBER_REGEX, patterns=None, include_single=False):
    io_dir = os.path.join(config_dir, "IOBridge")
    paths = sorted(p for p in glob.glob(os.path.join(io_dir, "IO_*")) if os.path.isfile(p))
    if not paths:
        raise SystemExit("错误: %s 下没有 IO_* 文件" % io_dir)
    entities, enabled = load_master(config_dir)
    matcher = re.compile(r"^/IO/(%s)Exports/" % chamber_regex)

    rows = []
    for path in paths:
        try:
            root = parse_fragment(path, entities)
        except ET.ParseError as err:
            raise SystemExit("错误: 解析 %s 失败: %s" % (path, err))
        walk(root, [], os.path.basename(path), matcher, rows)
    for row in rows:
        row["enabled"] = "是" if row["file"] in enabled else "否"

    if patterns:
        rows = [r for r in rows if any(match_pattern(r, p) for p in patterns)]

    chambers = {}
    for row in rows:
        chambers.setdefault(row["generic"], []).append(row["chamber"])
    for generic, names in chambers.items():
        for name in set(names):
            if names.count(name) > 1:
                warn("%s: 腔室 %s 有 %d 处定义" % (generic, name, names.count(name)))
    if not include_single:
        rows = [r for r in rows if len(set(chambers[r["generic"]])) > 1]

    rows.sort(key=lambda r: (r["generic"], r["chamber"], r["file"], r["path"]))
    return rows


def match_pattern(row, pattern):
    """含通配符时按 glob 匹配通用别名或末段名，否则做子串匹配。"""
    if any(c in pattern for c in "*?["):
        name = row["generic"].rsplit("/", 1)[-1]
        return fnmatch.fnmatchcase(row["generic"], pattern) or fnmatch.fnmatchcase(name, pattern)
    return pattern in row["generic"] or pattern in row["alias"]


def build_columns(rows):
    base = {c[1] for c in BASE_COLUMNS}
    seen = set()
    for row in rows:
        seen.update(k for k in row if k not in base)
    known = [name for name, _ in KNOWN_FIELDS]
    columns = list(BASE_COLUMNS)
    columns += [(name, name, width) for name, width in KNOWN_FIELDS if name in seen]
    columns += [(key, key, 14) for key in sorted(seen) if key not in known]
    return columns


def write_csv(rows, columns, output):
    # utf-8-sig 让 Excel 直接识别中文。
    with open(output, "w", encoding="utf-8-sig", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([c[0] for c in columns])
        for row in rows:
            writer.writerow([row.get(c[1], "") for c in columns])


def column_name(index):
    name = ""
    index += 1
    while index:
        index, rest = divmod(index - 1, 26)
        name = chr(ord("A") + rest) + name
    return name


def cell_xml(col, row_no, value, style):
    if value == "":
        return ""
    return '<c r="%s%d" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>' % (
        column_name(col), row_no, style, escape(value))


def write_xlsx(rows, columns, output):
    """写最小可用的 xlsx：冻结表头和前两列、自动筛选、单元格换行。"""
    last = "%s%d" % (column_name(len(columns) - 1), len(rows) + 1)
    cols = "".join('<col min="%d" max="%d" width="%d" customWidth="1"/>' % (i + 1, i + 1, c[2])
                   for i, c in enumerate(columns))
    body = ['<row r="1">' + "".join(cell_xml(i, 1, c[0], 1) for i, c in enumerate(columns)) + "</row>"]
    for no, row in enumerate(rows, 2):
        cells = "".join(cell_xml(i, no, row.get(c[1], ""), 2) for i, c in enumerate(columns))
        body.append('<row r="%d">%s</row>' % (no, cells))

    head = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n'
    main = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
    rel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
    pkg = "http://schemas.openxmlformats.org/package/2006"
    parts = {
        "[Content_Types].xml": head + (
            '<Types xmlns="%s/content-types">'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
            '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
            '<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>'
            "</Types>" % pkg),
        "_rels/.rels": head + (
            '<Relationships xmlns="%s/relationships">'
            '<Relationship Id="rId1" Type="%s/officeDocument" Target="xl/workbook.xml"/>'
            "</Relationships>" % (pkg, rel)),
        "xl/workbook.xml": head + (
            '<workbook xmlns="%s" xmlns:r="%s"><sheets>'
            '<sheet name="IOAliases" sheetId="1" r:id="rId1"/>'
            "</sheets></workbook>" % (main, rel)),
        "xl/_rels/workbook.xml.rels": head + (
            '<Relationships xmlns="%s/relationships">'
            '<Relationship Id="rId1" Type="%s/worksheet" Target="worksheets/sheet1.xml"/>'
            '<Relationship Id="rId2" Type="%s/styles" Target="styles.xml"/>'
            "</Relationships>" % (pkg, rel, rel)),
        "xl/styles.xml": head + (
            '<styleSheet xmlns="%s">'
            '<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font>'
            '<font><b/><sz val="11"/><name val="Calibri"/></font></fonts>'
            '<fills count="2"><fill><patternFill patternType="none"/></fill>'
            '<fill><patternFill patternType="gray125"/></fill></fills>'
            '<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>'
            '<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>'
            '<cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>'
            '<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>'
            '<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyAlignment="1">'
            '<alignment vertical="top" wrapText="1"/></xf></cellXfs>'
            '<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>'
            "</styleSheet>" % main),
        "xl/worksheets/sheet1.xml": head + (
            '<worksheet xmlns="%s"><dimension ref="A1:%s"/>'
            '<sheetViews><sheetView workbookViewId="0"><pane xSplit="2" ySplit="1" topLeftCell="C2" '
            'activePane="bottomRight" state="frozen"/></sheetView></sheetViews>'
            "<cols>%s</cols><sheetData>%s</sheetData>"
            '<autoFilter ref="A1:%s"/></worksheet>' % (main, last, cols, "".join(body), last)),
    }
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, content in parts.items():
            archive.writestr(name, content)


def main(argv=None):
    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    parser = argparse.ArgumentParser(description="导出各腔室同名别名 IO 信号定义对照表")
    parser.add_argument("--config", default=os.path.join(repo_root, "config"),
                        help="配置目录（含 IO_config.xml），默认为仓库根目录下的 config/")
    parser.add_argument("-o", "--output", default="io_aliases.xlsx",
                        help="输出文件，按扩展名选择 .xlsx 或 .csv（默认 io_aliases.xlsx）")
    parser.add_argument("--alias", action="append", metavar="PATTERN",
                        help="只导出匹配的别名，可重复；含 * ? [ 时按通配匹配，否则按子串匹配")
    parser.add_argument("--chamber-regex", default=DEFAULT_CHAMBER_REGEX,
                        help="别名 /IO/<腔室>Exports/... 中腔室名的正则（默认 %(default)s）")
    parser.add_argument("--include-single", action="store_true",
                        help="也导出只在一个腔室出现的别名")
    args = parser.parse_args(argv)

    extension = os.path.splitext(args.output)[1].lower()
    if extension not in (".xlsx", ".csv"):
        parser.error("输出文件扩展名必须是 .xlsx 或 .csv")
    try:
        re.compile(args.chamber_regex)
    except re.error as err:
        parser.error("--chamber-regex 无效: %s" % err)

    config_dir = resolve_config_dir(args.config)
    rows = collect(config_dir, args.chamber_regex, args.alias, args.include_single)
    columns = build_columns(rows)
    if extension == ".xlsx":
        write_xlsx(rows, columns, args.output)
    else:
        write_csv(rows, columns, args.output)

    print("已导出 %d 个通用别名、%d 条信号定义到 %s"
          % (len({r["generic"] for r in rows}), len(rows), args.output))


if __name__ == "__main__":
    main()
