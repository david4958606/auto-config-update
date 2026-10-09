"""现场入口的端到端选择测试；用替身程序避免修改真实配置。"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("run.sh")


class FeatureMenuTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copy2(SCRIPT, self.root / SCRIPT.name)
        (self.root / "features" / "v1.0").mkdir(parents=True)
        (self.root / "features" / "v1.1").mkdir()
        (self.root / "features" / "v1.0" / "aaa.yaml").write_text(
            'id: ig-auto-close\ndescription: IG规自动关闭\nversion: 9\nsteps: []\n'
        )
        (self.root / "features" / "v1.1" / "bbb.yml").write_text(
            'id: bbb\ndescription: "bbb功能"\nversion: 1\nsteps: []\n'
        )
        (self.root / "features" / "general.yaml").write_text(
            'id: general\ndescription: 通用功能\nsteps: []\n'
        )
        exe = self.root / "auto-config-update-32"
        exe.write_text(
            '#!/bin/bash\nprintf "%s\\n" "$*" >> "$CALLS"\n'
            '[[ -z ${FAIL_ON:-} || $3 != "$FAIL_ON" ]]\n'
        )
        exe.chmod(0o755)
        self.calls = self.root / "calls"

    def run_menu(self, inputs, **env):
        return subprocess.run(
            ["bash", str(self.root / SCRIPT.name)], input=inputs,
            text=True, capture_output=True, cwd="/", timeout=10,
            env={**os.environ, "CALLS": str(self.calls), **env},
        )

    def test_nested_selection_and_empty_chambers(self):
        # 排序为 general、v1.0/aaa、v1.1/bbb；只选择后两项。
        result = self.run_menu("2\n1\n2\n5\n\n3\n\nr\nyes\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("v1.0/ig-auto-close: IG规自动关闭", result.stdout)
        self.assertIn("v1.1/bbb: bbb功能", result.stdout)
        self.assertNotIn("v1.0/ig-auto-close v9", result.stdout)
        calls = self.calls.read_text().splitlines()
        self.assertEqual(len(calls), 2)
        self.assertIn("--chamber Ch1 --chamber Ch2 --chamber Ch5", calls[0])
        self.assertIn("v1.0/aaa.yaml", calls[0])
        self.assertIn("v1.1/bbb.yml", calls[1])
        self.assertNotIn("--chamber", calls[1])

    def test_cancel_selection_clears_chambers(self):
        result = self.run_menu("2\n1\n\n2\n2\n\nr\nyes\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("--chamber", self.calls.read_text())

    def test_cancel_and_eof_do_not_run(self):
        for inputs in ("q\n", "1\n\n", "1\n\nr\nno\n"):
            with self.subTest(inputs=inputs):
                result = self.run_menu(inputs)
                self.assertIn(result.returncode, (0, 1))
                self.assertFalse(self.calls.exists())

    def test_failure_stops_subsequent_features(self):
        first = self.root / "features" / "general.yaml"
        result = self.run_menu("1\n\n2\n\nr\nyes\n", FAIL_ON=str(first))
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(self.calls.read_text().splitlines()), 1)
        self.assertIn("已停止", result.stderr)


if __name__ == "__main__":
    unittest.main()
