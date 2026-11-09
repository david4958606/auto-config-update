"""互锁导出脚本测试；夹具在临时目录构造，不依赖现场 config/。"""
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
import export_interlocks as tool

CONTROL_CONFIG = """<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE Control [
<!ENTITY SimulatedFlag_Ch1 SYSTEM "../SimulatedFlag_Ch1">
<!ENTITY Intlk_Ch1 SYSTEM "./Interlock_Ch1">
]>
<Control>&Intlk_Ch1;</Control>
"""

INTERLOCK_CH1 = """<Ch1>
    <VInterlocks>
        <SourceStatus type="data" dataType="I" accessMode="RW" alias="">
            <DescriptorList>On:0,Off:1</DescriptorList>
        </SourceStatus>
        <Slow class="VInterlock" type="instance" alias="">
            <setDurationTrigger type="method">((/IO/A &lt; 1) &amp;&amp; (/IO/B == On)),4000</setDurationTrigger>
            <setTriggerInterval type="method">5000</setTriggerInterval>
            <setIntlkAlarm type="method">Alarm,ERROR,Too slow, check it.</setIntlkAlarm>
            &SimulatedFlag_Ch1;
            <acceptUnknown type="method">TriggerUnknown,FATAL,Unknown.</acceptUnknown>
            <createActionAlarm type="method">Off,NOTICE,Turned off.</createActionAlarm>
            <addIntDescriptorAction type="method">/IO/C,Off,Off</addIntDescriptorAction>
            <addDoubleValueAction type="method">/IO/D,0.0,Off</addDoubleValueAction>
            <addIntDescriptorActionNoAlarm type="method">/IO/E,On</addIntDescriptorActionNoAlarm>
            <addIntDescriptorDefaultActionNoAlarm type="method">/IO/F,Off</addIntDescriptorDefaultActionNoAlarm>
        </Slow>
        <!-- <Disabled class="VInterlock" type="instance" alias="">
            <setTrigger type="method">(/IO/A -- 1)</setTrigger>
        </Disabled> -->
    </VInterlocks>
    <SInterlocks>
        <Open type="instance" class="SInterlock" comment="rough valve">
            <setTrigger type="method">(/IO/V == Open)</setTrigger>
            <createCheckerAlarm type="method">LidOpen,ERROR,Lid, is open.</createCheckerAlarm>
            <addChecker type="method">(/IO/Lid == Closed),LidOpen</addChecker>
            <createCheckerAlarm type="method">NoVac,WARNING,No vacuum.</createCheckerAlarm>
            <addChecker type="method">(/IO/P &lt; 0.2),NoVac</addChecker>
        </Open>
    </SInterlocks>
</Ch1>
<ATR>
    <Aligner>
        <SInterlocks>
            <Rotate class="SInterlock" type="instance" alias="">
                <setTrigger type="method">(/IO/R == Go)</setTrigger>
            </Rotate>
        </SInterlocks>
    </Aligner>
</ATR>
"""


class ExportTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.temp = Path(temp.name)
        self.config = self.temp / "config"
        (self.config / "Control").mkdir(parents=True)
        (self.config / "SimulatedFlag_Ch1").write_text('<setSimulated type="method">true</setSimulated>\n')
        (self.config / "Control" / "Control_config.xml").write_text(CONTROL_CONFIG)
        self.write_interlock(INTERLOCK_CH1)

    def write_interlock(self, text):
        (self.config / "Control" / "Interlock_Ch1").write_text(text)

    def collect(self):
        """返回 ({逻辑路径: 行}, 告警文本)。"""
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rows = tool.collect(str(self.config))
        return {row["path"]: row for row in rows}, stderr.getvalue()

    def test_collects_active_interlocks_across_roots(self):
        rows, warnings = self.collect()
        self.assertEqual(warnings, "")
        self.assertEqual(sorted(rows), [
            "/Control/ATR/Aligner/SInterlocks/Rotate",
            "/Control/Ch1/SInterlocks/Open",
            "/Control/Ch1/VInterlocks/Slow",
        ])
        self.assertEqual(rows["/Control/ATR/Aligner/SInterlocks/Rotate"]["module"], "ATR/Aligner")

    def test_vinterlock_fields(self):
        row = self.collect()[0]["/Control/Ch1/VInterlocks/Slow"]
        self.assertEqual(row["type"], "VInterlock（监控型）")
        self.assertEqual(row["trigger"], "((/IO/A < 1) && (/IO/B == On))")
        self.assertEqual(row["duration"], "4000")
        self.assertEqual(row["interval"], "5000")
        self.assertEqual(row["alarm"], "[ERROR] Alarm: Too slow, check it.")
        self.assertEqual(row["unknown"], "[FATAL] TriggerUnknown: Unknown.")
        self.assertEqual(row["simulated"], "true")
        self.assertEqual(row["actions"].split("\n"), [
            "/IO/C = Off => [NOTICE] Off: Turned off.",
            "/IO/D = 0.0 => [NOTICE] Off: Turned off.",
            "/IO/E = On",
            "/IO/F = Off（默认）",
        ])

    def test_sinterlock_pairs_checker_with_alarm(self):
        row = self.collect()[0]["/Control/Ch1/SInterlocks/Open"]
        self.assertEqual(row["type"], "SInterlock（设置型）")
        self.assertEqual(row["comment"], "rough valve")
        self.assertEqual(row["checkers"].split("\n"), [
            "(/IO/Lid == Closed) => [ERROR] LidOpen: Lid, is open.",
            "(/IO/P < 0.2) => [WARNING] NoVac: No vacuum.",
        ])

    def test_warns_instead_of_dropping_unexpected_data(self):
        self.write_interlock("""<Ch1><SInterlocks>
            <Bad class="SInterlock" type="instance">
                <setTrigger type="method">(/IO/A == 1)</setTrigger>
                <addChecker type="method">(/IO/B == 1),Missing</addChecker>
                <newMethod type="method">x</newMethod>
            </Bad></SInterlocks></Ch1>""")
        rows, warnings = self.collect()
        row = rows["/Control/Ch1/SInterlocks/Bad"]
        self.assertIn("[未定义] Missing", row["checkers"])
        self.assertEqual(row["other"], "newMethod: x")
        self.assertIn("Missing", warnings)
        self.assertIn("newMethod", warnings)

    def run_main(self, name):
        output = self.temp / name
        with contextlib.redirect_stdout(io.StringIO()):
            tool.main(["--config", str(self.config), "-o", str(output)])
        return output

    def test_csv_output(self):
        with self.run_main("out.csv").open(encoding="utf-8-sig", newline="") as f:
            rows = list(csv.DictReader(f))
        self.assertEqual(len(rows), 3)
        self.assertEqual(rows[1]["Checker"].count("\n"), 1)

    def test_xlsx_output_is_well_formed(self):
        with zipfile.ZipFile(self.run_main("out.xlsx")) as archive:
            for name in archive.namelist():
                ET.fromstring(archive.read(name))
            sheet = archive.read("xl/worksheets/sheet1.xml").decode("utf-8")
        self.assertIn("&amp;&amp;", sheet)
        self.assertIn('<autoFilter ref="A1:O4"/>', sheet)

    def test_missing_interlocks_is_an_error(self):
        (self.config / "Control" / "Interlock_Ch1").unlink()
        with self.assertRaises(SystemExit):
            tool.collect(str(self.config))


if __name__ == "__main__":
    unittest.main()
