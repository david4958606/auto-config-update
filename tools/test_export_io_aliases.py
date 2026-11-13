"""IO 别名对照表导出脚本测试；夹具在临时目录构造，不依赖现场 config/。"""
import contextlib
import csv
import io
import os
from pathlib import Path
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import export_io_aliases as tool

IO_CONFIG = """<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE IAP[
<!ENTITY SimulatedFlag_Ch1 SYSTEM "./SimulatedFlag_Ch1">
<!ENTITY IO_Ch1 SYSTEM "./IOBridge/IO_Ch1">
<!ENTITY IO_Ch2 SYSTEM "./IOBridge/IO_Ch2">
<!ENTITY IO_Facility SYSTEM "./IOBridge/IO_Facility">
]>
<IO>
    &IO_Facility;
    &IO_Ch1;
    <!-- &IO_Ch2; -->
</IO>
"""

IO_CH1 = """<Ch1>
    <PG>
        <PressureAI dataType="D" accessMode="R" alias="/IO/Ch1Exports/PG_PressureAI">
            <Bd>1</Bd>
            <Ch>10</Ch>
            <Min>0</Min>
            <Max>100</Max> <!-- 注释不进表 -->
            <Accuracy>0.01</Accuracy>
            <Unit>Torr</Unit>
            &SimulatedFlag_Ch1;
        </PressureAI>
        <SpareAI dataType="D" accessMode="R" alias=""><Bd>1</Bd></SpareAI>
    </PG>
</Ch1>
"""

IO_CH2 = """<Ch2>
    <PG>
        <PressureAI dataType="D" accessMode="R" alias="/IO/Ch2Exports/PG_PressureAI">
            <Bd>2</Bd>
            <Min>0</Min>
            <Max>200</Max>
            <Unit>Pa</Unit>
            <Extra>x</Extra>
        </PressureAI>
    </PG>
</Ch2>
"""

IO_FACILITY = """
<Motor>
    <Ch1><OnlyCh1DI dataType="I" accessMode="R" alias="/IO/Ch1Exports/Only_DI"><Min>0</Min></OnlyCh1DI></Ch1>
</Motor>
<Ch1>
    <LA><StatusDI dataType="I" accessMode="R" alias="/IO/LAExports/StatusDI"/></LA>
</Ch1>
<Ch2>
    <LB><StatusDI dataType="I" accessMode="R" simulated="" alias="/IO/LBExports/StatusDI"/></LB>
</Ch2>
"""


class ExportIOAliasesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.config = self.root / "config"
        (self.config / "IOBridge").mkdir(parents=True)
        (self.config / "IO_config.xml").write_text(IO_CONFIG, encoding="utf-8")
        (self.config / "SimulatedFlag_Ch1").write_text(
            '<setSimulated type="method">true</setSimulated>', encoding="utf-8")
        for name, text in (("IO_Ch1", IO_CH1), ("IO_Ch2", IO_CH2), ("IO_Facility", IO_FACILITY)):
            (self.config / "IOBridge" / name).write_text(text, encoding="utf-8")

    def tearDown(self):
        self.temp.cleanup()

    def run_main(self, *args):
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            tool.main(["--config", str(self.config)] + list(args))
        return stdout.getvalue(), stderr.getvalue()

    def test_groups_same_alias_across_chambers(self):
        rows = tool.collect(str(self.config))
        self.assertEqual([r["chamber"] for r in rows], ["Ch1", "Ch2"])
        ch1, ch2 = rows
        self.assertEqual(ch1["generic"], "/IO/ChXExports/PG_PressureAI")
        self.assertEqual(ch1["path"], "/IO/Ch1/PG/PressureAI")
        self.assertEqual((ch1["Min"], ch1["Max"], ch1["Accuracy"], ch1["Unit"]),
                         ("0", "100", "0.01", "Torr"))
        self.assertEqual(ch1["setSimulated"], "true")
        self.assertEqual(ch1["enabled"], "是")
        self.assertEqual(ch2["enabled"], "否")
        self.assertNotIn("Accuracy", ch2)
        self.assertEqual(ch2["Extra"], "x")

    def test_include_single_and_chamber_regex(self):
        rows = tool.collect(str(self.config), include_single=True)
        self.assertIn("/IO/ChXExports/Only_DI", {r["generic"] for r in rows})
        rows = tool.collect(str(self.config), chamber_regex="LA|LB")
        self.assertEqual([(r["generic"], r["chamber"]) for r in rows],
                         [("/IO/ChXExports/StatusDI", "LA"), ("/IO/ChXExports/StatusDI", "LB")])
        self.assertEqual(rows[1]["@simulated"], "")

    def test_alias_filter(self):
        self.assertEqual(len(tool.collect(str(self.config), patterns=["PG_Pressure"])), 2)
        self.assertEqual(len(tool.collect(str(self.config), patterns=["PG_*AI"])), 2)
        self.assertEqual(tool.collect(str(self.config), patterns=["CG_*"]), [])

    def test_columns_put_known_fields_first(self):
        columns = [c[1] for c in tool.build_columns(tool.collect(str(self.config)))]
        self.assertEqual(columns[:8], [c[1] for c in tool.BASE_COLUMNS])
        self.assertLess(columns.index("Min"), columns.index("Max"))
        self.assertLess(columns.index("Unit"), columns.index("Extra"))

    def test_csv_output(self):
        output = self.root / "out.csv"
        stdout, _ = self.run_main("-o", str(output))
        self.assertIn("1 个通用别名、2 条信号定义", stdout)
        with open(output, encoding="utf-8-sig", newline="") as f:
            rows = list(csv.DictReader(f))
        self.assertEqual([r["腔室"] for r in rows], ["Ch1", "Ch2"])
        self.assertEqual(rows[1]["Unit"], "Pa")

    def test_xlsx_output(self):
        output = self.root / "out.xlsx"
        self.run_main("-o", str(output))
        with zipfile.ZipFile(output) as archive:
            sheet = ET.fromstring(archive.read("xl/worksheets/sheet1.xml"))
        ns = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
        self.assertEqual(len(sheet.findall(".//m:sheetData/m:row", ns)), 3)

    def test_nested_config_dir(self):
        self.assertEqual(tool.resolve_config_dir(str(self.root)), str(self.config))
        with self.assertRaises(SystemExit):
            tool.resolve_config_dir(str(self.root / "missing"))

    def test_rejects_bad_extension(self):
        with self.assertRaises(SystemExit), contextlib.redirect_stderr(io.StringIO()):
            tool.main(["-o", "out.txt"])


if __name__ == "__main__":
    unittest.main()
