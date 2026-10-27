"""prepare-onsite.sh 与现场一键脚本的端到端测试。

用替身 auto-config-update-32 避免修改真实配置；现场脚本会核对功能定义 sha256，
因此篡改用例必须先生成、后改文件。
"""
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import tempfile
import time
import unittest


SCRIPT = Path(__file__).with_name("prepare-onsite.sh")

FAKE_RUNNER = """#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$CALLS"
if [[ -n ${SLEEP_CHECK:-} && $1 == check ]]; then
    sleep "$SLEEP_CHECK"
fi
if [[ -n ${SLEEP_APPLY:-} && $1 == apply ]]; then
    sleep "$SLEEP_APPLY"
fi
if [[ -n ${FAIL_ON:-} && $1 == apply && $3 == "$FAIL_ON" ]]; then
    printf '模拟失败：%s\\n' "$3" >&2
    exit 7
fi
if [[ -n ${MUTATE:-} && $1 == apply ]]; then
    printf 'changed-by-fake\\n' >> "$MUTATE"
fi
printf 'fake-output %s\\n' "$*"
"""


class OnsiteTestBase(unittest.TestCase):
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
        (self.root / "config" / "Setup").mkdir(parents=True)
        (self.root / "config" / "Setup" / "Setup_Ch1.xml").write_text("<Setup/>\n")
        self.runner = self.root / "auto-config-update-32"
        self.runner.write_text(FAKE_RUNNER)
        self.runner.chmod(0o755)
        self.calls = self.root / "calls"
        self.env = {**os.environ, "CALLS": str(self.calls)}

    # ---- helpers -----------------------------------------------------
    def run_generator(self, inputs, *args):
        return subprocess.run(
            ["bash", str(self.root / SCRIPT.name), *args],
            input=inputs, text=True, capture_output=True, cwd="/", timeout=30,
            env=self.env,
        )

    def plan_files(self):
        return sorted(self.root.glob("updateConfig-*.sh"))

    # 默认选择：v1.0/aaa 的 Ch1 + v1.1/bbb 的全部腔室。
    DEFAULT_INPUTS = "2\n1\n\n3\n\ng\n\n工位A\n备注\ny\n"

    def generate(self, inputs=None, *args):
        result = self.run_generator(self.DEFAULT_INPUTS if inputs is None else inputs, *args)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        plans = self.plan_files()
        self.assertEqual(len(plans), 1, plans)
        return plans[0]

    def run_plan(self, plan, inputs="1\n", *args, **extra_env):
        env = {**self.env, **extra_env}
        return subprocess.run(
            ["bash", str(plan), *args],
            input=inputs, text=True, capture_output=True, cwd="/", timeout=30,
            env=env,
        )

    def apply_calls(self):
        if not self.calls.exists():
            return []
        return [line for line in self.calls.read_text().splitlines() if line.startswith("apply ")]

    def logs(self):
        return sorted((self.root / "logs").glob("upgrade-*.log"), key=lambda p: p.stat().st_mtime)

    def latest_log(self):
        logs = self.logs()
        self.assertTrue(logs, "没有生成日志")
        return logs[-1].read_text()

    def backups(self):
        return sorted((self.root / "logs").glob("config-backup-*.tar.gz"))


class GeneratorTest(OnsiteTestBase):
    def test_generates_self_contained_script(self):
        plan = self.generate()
        self.assertTrue(os.access(plan, os.X_OK))
        text = plan.read_text()
        self.assertIn("PLAN_FEATURES=('features/v1.0/aaa.yaml' 'features/v1.1/bbb.yml' )", text)
        self.assertIn("PLAN_CHAMBERS=('Ch1' '' )", text)
        self.assertIn("PLAN_LABELS=('v1.0/ig-auto-close: IG规自动关闭'", text)
        self.assertIn("IG规自动关闭", text)
        self.assertIn("bbb功能", text)
        self.assertIn("SITE_NAME='工位A'", text)
        self.assertIn("SITE_NOTE='备注'", text)
        self.assertIn("1) 应用升级配置", text)
        self.assertIn("2) 退出", text)
        self.assertEqual(subprocess.run(["bash", "-n", str(plan)]).returncode, 0)

    def test_chamber_selection_is_embedded(self):
        plan = self.generate("2\n1\n2\n\n3\n\ng\n\n\n\ny\n")
        self.assertIn("PLAN_CHAMBERS=('Ch1 Ch2' '' )", plan.read_text())
        result = self.run_plan(plan)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        config = self.root / "config"
        self.assertEqual(
            self.apply_calls(),
            [
                f"apply --feature features/v1.0/aaa.yaml --config {config} "
                "--chamber Ch1 --chamber Ch2",
                f"apply --feature features/v1.1/bbb.yml --config {config}",
            ],
        )

    def test_config_option_is_embedded(self):
        site = self.root / "config-site"
        (site / "Setup").mkdir(parents=True)
        (site / "Setup" / "Setup_Ch1.xml").write_text("<Setup/>\n")
        plan = self.generate("1\n\ng\n\n\n\ny\n", "--config", "config-site")
        self.assertIn("PLAN_CONFIG='config-site'", plan.read_text())
        result = self.run_plan(plan)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn(f"--config {site}", self.apply_calls()[0])
        self.assertIn(f"check --config {site}", self.calls.read_text())
        self.assertIn("配置目录：" + str(site), self.latest_log())

    def test_default_name_is_updateconfig_date_and_collision_safe(self):
        first = self.run_generator("1\n\ng\n\n\n\ny\n")
        self.assertEqual(first.returncode, 0, first.stderr + first.stdout)
        second = self.run_generator("1\n\ng\n\n\n\ny\n")
        self.assertEqual(second.returncode, 0, second.stderr + second.stdout)
        names = sorted(p.name for p in self.root.glob("updateConfig-*.sh"))
        self.assertEqual(len(names), 2, names)
        self.assertTrue(any(re.fullmatch(r"updateConfig-\d{8}\.sh", n) for n in names), names)
        self.assertTrue(any(re.fullmatch(r"updateConfig-\d{8}-\d+\.sh", n) for n in names), names)

    def test_custom_output_and_refuses_overwrite(self):
        custom = self.root / "custom.sh"
        result = self.run_generator("1\n\ng\n\n\n\ny\n", "-o", str(custom))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertTrue(custom.is_file())
        result = self.run_generator("1\n\ng\n\n\n\ny\n", "-o", str(custom))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("拒绝覆盖", result.stderr)

    def test_generator_cancel_and_empty_selection(self):
        self.assertEqual(self.run_generator("q\n").returncode, 0)
        self.assertFalse(self.plan_files())
        result = self.run_generator("g\nq\n")
        self.assertEqual(result.returncode, 0)
        self.assertIn("至少选择一个功能", result.stderr)
        self.assertFalse(self.plan_files())

    def test_generator_eof_and_bad_args(self):
        self.assertEqual(self.run_generator("").returncode, 1)
        self.assertEqual(self.run_generator("", "--bogus").returncode, 2)
        self.assertEqual(self.run_generator("", "-o").returncode, 2)

    def test_generator_requires_features(self):
        shutil.rmtree(self.root / "features")
        result = self.run_generator("")
        self.assertEqual(result.returncode, 1)
        self.assertIn("缺少 features/", result.stderr)


class OnsiteScriptTest(OnsiteTestBase):
    def test_menu_has_two_options_and_plan(self):
        plan = self.generate()
        result = self.run_plan(plan, "2\n")
        self.assertIn("===== 自动升级脚本 =====", result.stdout)
        self.assertIn("1) 应用升级配置", result.stdout)
        self.assertIn("2) 退出", result.stdout)
        self.assertIn("预置方案：工位A", result.stdout)
        self.assertIn("全部腔室", result.stdout)

    def test_apply_writes_log_without_backup_by_default(self):
        plan = self.generate()
        result = self.run_plan(plan)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        log = self.latest_log()
        for needle in (
            "自动升级执行日志", "生成人", "方案指纹", "预置方案", "预检通过", "运行参数",
            "apply --feature features/v1.0/aaa.yaml", "fake-output",
            "结果：成功", "日志：",
        ):
            self.assertIn(needle, log, needle)
        self.assertEqual(self.backups(), [], "默认不应自动备份")
        self.assertIn("缺省不备份", log)
        self.assertIn("备份：未自动备份", log)

    def test_backup_option_creates_backup(self):
        plan = self.generate()
        result = self.run_plan(plan, "", "--yes", "--backup")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertTrue(self.backups(), "加 --backup 后应自动备份")
        log = self.latest_log()
        self.assertIn("配置备份：", log)
        self.assertIn("备份：" + str(self.backups()[0]), log)

    def test_exit_option_does_not_run(self):
        plan = self.generate()
        result = self.run_plan(plan, "2\n")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(self.apply_calls(), [])
        self.assertIn("已退出，未做任何修改", self.latest_log())

    def test_invalid_menu_input_is_retried(self):
        plan = self.generate()
        result = self.run_plan(plan, "x\n9\n1\n")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(len(self.apply_calls()), 2)
        self.assertIn("无效输入", self.latest_log())

    def test_yes_skips_menu(self):
        plan = self.generate()
        result = self.run_plan(plan, "", "--yes")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertNotIn("===== 自动升级脚本 =====", result.stdout)
        self.assertEqual(len(self.apply_calls()), 2)

    def test_tampered_feature_aborts_before_apply(self):
        plan = self.generate()
        (self.root / "features" / "v1.0" / "aaa.yaml").write_text(
            'id: ig-auto-close\ndescription: 被篡改\nsteps: []\n'
        )
        result = self.run_plan(plan, "", "--yes")
        self.assertEqual(result.returncode, 3)
        self.assertEqual(self.apply_calls(), [])
        log = self.latest_log()
        self.assertIn("功能定义与预置方案不一致", log)
        self.assertIn("预检未通过", log)

    def test_force_bypasses_hash_check(self):
        plan = self.generate()
        (self.root / "features" / "v1.0" / "aaa.yaml").write_text(
            'id: ig-auto-close\ndescription: 被篡改\nsteps: []\n'
        )
        result = self.run_plan(plan, "", "--yes", "--force")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(len(self.apply_calls()), 2)
        self.assertIn("--force", self.latest_log())

    def test_failure_stops_remaining_and_keeps_exit_code(self):
        plan = self.generate()
        result = self.run_plan(plan, "", "--yes", FAIL_ON="features/v1.0/aaa.yaml")
        self.assertEqual(result.returncode, 7)
        self.assertEqual(len(self.apply_calls()), 1)
        log = self.latest_log()
        self.assertIn("[失败]", log)
        self.assertIn("未执行后续 1 项功能", log)

    def test_default_runs_skip_backup(self):
        plan = self.generate()
        result = self.run_plan(plan, "", "--yes")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(self.backups(), [])
        self.assertIn("备份：未自动备份", self.latest_log())

    def test_custom_log_dir(self):
        plan = self.generate()
        log_dir = self.root / "mylogs"
        result = self.run_plan(plan, "", "--yes", "--log-dir", str(log_dir))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertTrue(list(log_dir.glob("upgrade-*.log")))

    def test_changed_files_are_reported(self):
        plan = self.generate()
        target = self.root / "config" / "Setup" / "Setup_Ch1.xml"
        result = self.run_plan(plan, "", "--yes", MUTATE=str(target))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        log = self.latest_log()
        self.assertIn("配置变更：", log)
        self.assertIn("修改：" + str(target), log)

    def test_missing_binary_aborts_with_log(self):
        plan = self.generate()
        self.runner.unlink()
        result = self.run_plan(plan, "", "--yes")
        self.assertEqual(result.returncode, 3)
        log = self.latest_log()
        self.assertIn("找不到可执行文件", log)
        self.assertIn("预检未通过", log)

    def test_missing_config_dir_aborts(self):
        plan = self.generate()
        shutil.rmtree(self.root / "config")
        result = self.run_plan(plan, "", "--yes")
        self.assertEqual(result.returncode, 3)
        self.assertIn("配置目录不存在", self.latest_log())

    def test_eof_at_menu_exits_without_apply(self):
        plan = self.generate()
        result = self.run_plan(plan, "")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(self.apply_calls(), [])
        self.assertIn("未收到输入", self.latest_log())

    def test_log_and_backup_are_private(self):
        plan = self.generate()
        result = self.run_plan(plan, "", "--yes", "--backup")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        log = self.logs()[-1]
        backup = self.backups()[0]
        self.assertEqual(stat.S_IMODE(log.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(backup.stat().st_mode), 0o600)

    def test_backup_failure_stops_before_apply(self):
        plan = self.generate()
        fake_bin = self.root / "fakebin"
        fake_bin.mkdir()
        fake_tar = fake_bin / "tar"
        fake_tar.write_text("#!/bin/sh\nexit 1\n")
        fake_tar.chmod(0o755)
        result = self.run_plan(
            plan, "", "--yes", "--backup",
            PATH=str(fake_bin) + os.pathsep + os.environ.get("PATH", ""),
        )
        self.assertEqual(result.returncode, 5)
        self.assertEqual(self.apply_calls(), [])
        log = self.latest_log()
        self.assertIn("配置备份失败", log)
        self.assertIn("自动备份失败", log)
        self.assertIn("已停止，未修改任何配置", log)
        self.assertIn("可不加 --backup", log)

    def test_concurrent_run_is_blocked(self):
        plan = self.generate()
        env = {**self.env, "SLEEP_CHECK": "2"}
        first = subprocess.Popen(
            ["bash", str(plan), "--yes"], cwd="/", env=env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            lock = self.root / ".updateConfig.lock"
            deadline = time.time() + 10
            while not lock.exists() and time.time() < deadline:
                time.sleep(0.05)
            self.assertTrue(lock.exists(), "第一个进程未获取到锁")
            second = self.run_plan(plan, "", "--yes")
            self.assertEqual(second.returncode, 6)
            self.assertIn("已有升级进程在执行", self.latest_log())
            out, err = first.communicate(timeout=30)
            self.assertEqual(first.returncode, 0, out + err)
            self.assertFalse(lock.exists(), "锁未释放")
        finally:
            if first.poll() is None:
                first.kill()
                first.communicate()

    def test_interrupt_writes_partial_log(self):
        plan = self.generate()
        env = {**self.env, "SLEEP_APPLY": "5"}
        proc = subprocess.Popen(
            ["bash", str(plan), "--yes"], cwd="/", env=env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
            start_new_session=True,
        )
        try:
            deadline = time.time() + 10
            while time.time() < deadline:
                if self.calls.exists() and "apply " in self.calls.read_text():
                    break
                time.sleep(0.05)
            os.killpg(os.getpgid(proc.pid), signal.SIGINT)
            proc.communicate(timeout=20)
            self.assertEqual(proc.returncode, 130)
            log = self.latest_log()
            self.assertIn("结果：已中断", log)
            self.assertIn("中断时正在处理第 1/2 项", log)
            self.assertFalse((self.root / ".updateConfig.lock").exists(), "中断后锁未释放")
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.communicate()

    def test_symlinked_runner_uses_real_config(self):
        plan = self.generate()
        real = self.root / "real"
        (real / "config" / "Setup").mkdir(parents=True)
        (real / "config" / "Setup" / "Setup_Ch1.xml").write_text("<Setup/>\n")
        self.runner.rename(real / "auto-config-update-32")
        (self.root / "auto-config-update-32").symlink_to(real / "auto-config-update-32")
        result = self.run_plan(plan, "", "--yes")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        real_config = str(real / "config")
        self.assertIn(f"--config {real_config}", self.apply_calls()[0])
        self.assertIn(f"check --config {real_config}", self.calls.read_text())
        self.assertIn("配置目录：" + real_config, self.latest_log())

    def test_empty_log_dir_option_is_rejected(self):
        plan = self.generate()
        result = self.run_plan(plan, "", "--yes", "--log-dir=")
        self.assertEqual(result.returncode, 2)
        self.assertEqual(self.apply_calls(), [])


if __name__ == "__main__":
    unittest.main()
