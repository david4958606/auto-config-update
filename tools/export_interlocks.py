#!/usr/bin/env python3
"""把 config/Control/Interlock_* 中的全部互锁导出成一张表（每个互锁一行）。

VInterlock 为监控型互锁，硬件 IO 信号变化时生效；SInterlock 为设置型互锁，
对 IO 设值时生效。被 XML 注释掉的互锁未启用，不导出。

用法：
    python3 tools/export_interlocks.py                       # 输出 interlocks.xlsx
    python3 tools/export_interlocks.py -o interlocks.csv     # 按扩展名输出 CSV
    python3 tools/export_interlocks.py --config /path/to/config

只依赖 Python 3 标准库。
"""
import argparse
import csv
import glob
import os
import re
import sys
import xml.etree.ElementTree as ET
import zipfile
from xml.sax.saxutils import escape

TYPE_LABELS = {
    "VInterlock": "VInterlock（监控型）",
    "SInterlock": "SInterlock（设置型）",
}

# (表头, 字段, xlsx 列宽)
COLUMNS = [
    ("模块", "module", 12),
    ("类型", "type", 20),
    ("名称", "name", 30),
    ("逻辑路径", "path", 44),
    ("来源文件", "file", 22),
    ("备注", "comment", 14),
    ("Trigger", "trigger", 60),
    ("持续时间", "duration", 11),
    ("触发间隔", "interval", 11),
    ("互锁告警", "alarm", 46),
    ("Unknown 告警", "unknown", 40),
    ("Checker", "checkers", 90),
    ("Action", "actions", 70),
    ("模拟", "simulated", 8),
    ("其他", "other", 24),
]

ENTITY_DECL = re.compile(r'<!ENTITY\s+(\S+)\s+SYSTEM\s+"([^"]+)"\s*>')
ENTITY_REF = re.compile(r"&(?!(?:amp|lt|gt|quot|apos|#\w+);)([\w.-]+);")
COMMENT = re.compile(r"<!--.*?-->", re.S)
XML_DECL = re.compile(r"<\?xml.*?\?>", re.S)


def warn(message):
    print("警告: " + message, file=sys.stderr)


def load_entities(control_dir):
    """读取 Control_config.xml 声明的外部实体，返回 {实体名: 片段内容}。"""
    main = os.path.join(control_dir, "Control_config.xml")
    if not os.path.exists(main):
        return {}
    with open(main, encoding="utf-8") as f:
        text = COMMENT.sub("", f.read())
    entities = {}
    for name, rel in ENTITY_DECL.findall(text):
        path = os.path.normpath(os.path.join(control_dir, rel))
        # 这里只内联 SimulatedFlag 这类小片段；Interlock_* 由调用方逐个读取。
        if os.path.isfile(path) and not os.path.basename(path).startswith(("Control_", "Interlock_")):
            with open(path, encoding="utf-8") as f:
                entities[name] = f.read()
    return entities


def parse_fragment(path, entities):
    """解析一个可能含多个根节点、且引用外部实体的片段，返回包裹后的根。"""
    with open(path, encoding="utf-8") as f:
        text = XML_DECL.sub("", COMMENT.sub("", f.read()))

    def resolve(match):
        name = match.group(1)
        if name not in entities:
            warn("%s: 实体 &%s; 未声明或文件缺失，已忽略" % (os.path.basename(path), name))
        return entities.get(name, "")

    return ET.fromstring("<fragment>" + ENTITY_REF.sub(resolve, text) + "</fragment>")


def clean(text):
    return " ".join((text or "").split())


def split_alarm(value):
    """NAME,LEVEL,message；message 里可以有逗号。"""
    parts = value.split(",", 2)
    parts += [""] * (3 - len(parts))
    return parts[0], parts[1], parts[2]


def format_alarm(value):
    name, level, message = split_alarm(value)
    return "[%s] %s: %s" % (level, name, message)


def build_row(elem, parents, filename):
    """把一个互锁节点转换成表格行。"""
    label = "/".join(parents + [elem.tag])
    row = {
        "module": "/".join(parents[:-1]),
        "type": TYPE_LABELS[elem.get("class")],
        "name": elem.tag,
        "path": "/Control/" + label,
        "file": filename,
        "comment": elem.get("comment", ""),
    }

    # 告警先于引用它的 checker/action 声明，但这里不依赖声明顺序。
    alarms = {}
    for child in elem:
        if child.tag in ("createCheckerAlarm", "createActionAlarm"):
            value = clean(child.text)
            name = split_alarm(value)[0]
            key = (child.tag, name)
            if key in alarms and alarms[key] != value:
                warn("%s: 告警 %s 重复定义且内容不同，取最后一个" % (label, name))
            alarms[key] = value

    def alarm_text(tag, name):
        if (tag, name) in alarms:
            return format_alarm(alarms[(tag, name)])
        warn("%s: 引用了未定义的告警 %s" % (label, name))
        return "[未定义] " + name

    triggers, checkers, actions, others = [], [], [], []
    used = set()
    for child in elem:
        tag, value = child.tag, clean(child.text)
        if tag == "setTrigger":
            triggers.append(value)
        elif tag == "setDurationTrigger":
            condition, _, duration = value.rpartition(",")
            triggers.append(condition)
            row["duration"] = duration
        elif tag == "setTriggerInterval":
            row["interval"] = value
        elif tag == "setIntlkAlarm":
            row["alarm"] = format_alarm(value)
        elif tag == "acceptUnknown":
            row["unknown"] = format_alarm(value)
        elif tag == "setSimulated":
            row["simulated"] = value
        elif tag == "addChecker":
            condition, _, name = value.rpartition(",")
            used.add(("createCheckerAlarm", name))
            checkers.append("%s => %s" % (condition, alarm_text("createCheckerAlarm", name)))
        elif tag in ("addIntDescriptorAction", "addDoubleValueAction"):
            target, set_to, name = (value.split(",", 2) + ["", ""])[:3]
            used.add(("createActionAlarm", name))
            actions.append("%s = %s => %s" % (target, set_to, alarm_text("createActionAlarm", name)))
        elif tag == "addIntDescriptorActionNoAlarm":
            target, _, set_to = value.partition(",")
            actions.append("%s = %s" % (target, set_to))
        elif tag == "addIntDescriptorDefaultActionNoAlarm":
            target, _, set_to = value.partition(",")
            actions.append("%s = %s（默认）" % (target, set_to))
        elif tag not in ("createCheckerAlarm", "createActionAlarm"):
            others.append("%s: %s" % (tag, value))
            warn("%s: 未识别的方法 %s，已放入“其他”列" % (label, tag))

    for key in alarms:
        if key not in used:
            warn("%s: 告警 %s 已定义但未被引用" % (label, key[1]))

    if len(triggers) > 1:
        warn("%s: 存在 %d 个 trigger" % (label, len(triggers)))
    row["trigger"] = "\n".join(triggers)
    row["checkers"] = "\n".join(checkers)
    row["actions"] = "\n".join(actions)
    row["other"] = "\n".join(others)
    return row


def walk(elem, parents, filename, rows):
    for child in elem:
        if child.get("class") in TYPE_LABELS:
            container = parents[-1] if parents else ""
            if container != child.get("class") + "s":
                warn("%s: %s 位于 %s 容器内" % (child.tag, child.get("class"), container or "顶层"))
            rows.append(build_row(child, parents, filename))
        else:
            walk(child, parents + [child.tag], filename, rows)


def collect(config_dir):
    control_dir = os.path.join(config_dir, "Control")
    paths = sorted(p for p in glob.glob(os.path.join(control_dir, "Interlock_*")) if os.path.isfile(p))
    if not paths:
        raise SystemExit("错误: %s 下没有 Interlock_* 文件" % control_dir)
    entities = load_entities(control_dir)
    rows = []
    for path in paths:
        try:
            root = parse_fragment(path, entities)
        except ET.ParseError as err:
            raise SystemExit("错误: 解析 %s 失败: %s" % (path, err))
        walk(root, [], os.path.basename(path), rows)
    return rows


def write_csv(rows, output):
    # utf-8-sig 让 Excel 直接识别中文。
    with open(output, "w", encoding="utf-8-sig", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([c[0] for c in COLUMNS])
        for row in rows:
            writer.writerow([row.get(c[1], "") for c in COLUMNS])


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


def write_xlsx(rows, output):
    """写最小可用的 xlsx：冻结表头、自动筛选、单元格换行。"""
    last = "%s%d" % (column_name(len(COLUMNS) - 1), len(rows) + 1)
    cols = "".join('<col min="%d" max="%d" width="%d" customWidth="1"/>' % (i + 1, i + 1, c[2])
                   for i, c in enumerate(COLUMNS))
    body = ['<row r="1">' + "".join(cell_xml(i, 1, c[0], 1) for i, c in enumerate(COLUMNS)) + "</row>"]
    for no, row in enumerate(rows, 2):
        cells = "".join(cell_xml(i, no, row.get(c[1], ""), 2) for i, c in enumerate(COLUMNS))
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
            '<sheet name="Interlocks" sheetId="1" r:id="rId1"/>'
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
            '<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" '
            'activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>'
            "<cols>%s</cols><sheetData>%s</sheetData>"
            '<autoFilter ref="A1:%s"/></worksheet>' % (main, last, cols, "".join(body), last)),
    }
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, content in parts.items():
            archive.writestr(name, content)


def main(argv=None):
    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    parser = argparse.ArgumentParser(description="导出 VInterlock/SInterlock 互锁总表")
    parser.add_argument("--config", default=os.path.join(repo_root, "config"),
                        help="配置目录，默认为仓库根目录下的 config/")
    parser.add_argument("-o", "--output", default="interlocks.xlsx",
                        help="输出文件，按扩展名选择 .xlsx 或 .csv（默认 interlocks.xlsx）")
    args = parser.parse_args(argv)

    extension = os.path.splitext(args.output)[1].lower()
    if extension not in (".xlsx", ".csv"):
        parser.error("输出文件扩展名必须是 .xlsx 或 .csv")

    rows = collect(args.config)
    (write_xlsx if extension == ".xlsx" else write_csv)(rows, args.output)

    v_count = sum(1 for row in rows if row["type"] == TYPE_LABELS["VInterlock"])
    print("已导出 %d 个互锁（VInterlock %d，SInterlock %d）到 %s"
          % (len(rows), v_count, len(rows) - v_count, args.output))


if __name__ == "__main__":
    main()
