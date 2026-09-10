package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"addex/internal/feature"
	"addex/internal/setupcheck"
)

// upgrade16196Features 是按顺序施加的升级 feature（见 doc/config-upgrade-design.md）。
var upgrade16196Features = []string{
	"upgrade-16196-setup.yaml",
	"upgrade-16196-io.yaml",
	"upgrade-16196-control.yaml",
}

// TestUpgradeExample16196 以 example-16196/config_old 为"旧配置"，依次 apply 升级 feature，
// 再与用户提供的 example-16196/config 做**语义比对**(见 internal/xmlcmp)，并校验：
//   - Setup/*.xml 的 <Param name> 与 <Option>/<Value paramName> 一一对应(数量+顺序)；
//   - 二次 apply 逐字节幂等。
//
// Recipe/ 下新增 recipe 与对应 recipe 文件按需求不处理(本夹具中两侧一致)。
func TestUpgradeExample16196(t *testing.T) {
	root := filepath.Join("..", "..")
	oldDir := filepath.Join(root, "example-16196", "config_old")
	newDir := filepath.Join(root, "example-16196", "config")
	if _, err := os.Stat(oldDir); err != nil {
		t.Skipf("缺少 %s，跳过升级校验", oldDir)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Skipf("缺少 %s，跳过升级校验", newDir)
	}

	work := t.TempDir()
	copyTree(t, oldDir, filepath.Join(work, "config"))

	run := func() {
		for _, name := range upgrade16196Features {
			feat, err := feature.Load(filepath.Join(root, "features", name))
			if err != nil {
				t.Fatalf("加载 %s: %v", name, err)
			}
			eng, err := New(filepath.Join(work, "config"))
			if err != nil {
				t.Fatalf("构造引擎: %v", err)
			}
			if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
				t.Fatalf("apply %s: %v", name, err)
			}
		}
	}

	run()
	compareTree(t, filepath.Join(work, "config"), newDir)
	checkSetup16196(t, filepath.Join(work, "config"), newDir)
	checkSetup16196Issues(t, filepath.Join(work, "config"), newDir)

	// 幂等：再跑一遍，产物必须逐字节不变。
	before := snapshot(t, filepath.Join(work, "config"))
	run()
	after := snapshot(t, filepath.Join(work, "config"))
	for p, b := range before {
		if after[p] != b {
			t.Errorf("二次 apply 非幂等，文件被改动: %s", p)
		}
	}
}

// checkSetup16196Issues 断言 internal/setupcheck 能**检出**目标里那处供应商笔误，且产物与
// 目标触发的问题集合完全一致（工具按目标保真复现，同时把问题暴露出来）。
func checkSetup16196Issues(t *testing.T, gotDir, wantDir string) {
	t.Helper()
	got, err := setupcheck.CheckDir(gotDir)
	if err != nil {
		t.Fatalf("校验升级产物: %v", err)
	}
	want, err := setupcheck.CheckDir(wantDir)
	if err != nil {
		t.Fatalf("校验目标配置: %v", err)
	}
	if len(want) == 0 {
		t.Fatalf("目标配置应带 GasFlowCompens 笔误，setupcheck 却未检出")
	}
	if len(got) != len(want) {
		t.Errorf("产物问题数 %d 与目标 %d 不一致\n产物: %v\n目标: %v", len(got), len(want), got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 处问题与目标不一致:\n产物 %v\n目标 %v", i, got[i], want[i])
		}
	}
	for _, is := range want {
		if is.Severity != setupcheck.Error || is.Kind != "orphan-param" {
			continue
		}
		if !strings.Contains(is.Detail, "AlONGasFlowPieceCompens") {
			t.Errorf("笔误未被指名: %v", is)
		}
	}
	t.Logf("setupcheck 检出目标自带笔误 %d 处（均为 error）：%v", len(want), want[0])
}

// checkSetup16196 校验 Setup/*.xml 的 <Param name> 与 <Option>/<Value paramName> 一一对应。
//
// 判定口径（对齐需求）：
//   - 产物内部 Param[i] 与 Value[i] 必须同名（这是一一对应的核心）；
//   - 产物的 Param/Value **集合**必须与目标一致（不丢项、不多项，内容不退化）；
//   - 目标自身在该位置就不一致时（供应商配置笔误，如 AlONGasFlowPieceCompens vs
//     AlOGasFlowPieceCompens）产物与目标保持一致；该笔误由 setupcheck（`check` 子命令）
//     判为 error 并报错，见 checkSetup16196Issues——"按目标保真"与"必须检出"并不矛盾。
//
// 注意：不要求索引顺序与目标完全相同——被挪位但两侧都在的节点会就地保留，顺序差异按
// internal/xmlcmp 的口径视为语义无关（只记 order）。
func checkSetup16196(t *testing.T, gotDir, wantDir string) {
	t.Helper()
	for rel := range listFiles(t, gotDir) {
		if !strings.HasPrefix(rel, "Setup/") || !strings.HasSuffix(rel, ".xml") || ignoreFile(rel) {
			continue
		}
		if strings.HasPrefix(filepath.Base(rel), "SetupRepository") {
			continue
		}
		gp, gv, ok := setupSequences(t, filepath.Join(gotDir, rel))
		if !ok {
			continue
		}
		wp, wv, ok := setupSequences(t, filepath.Join(wantDir, rel))
		if !ok {
			continue
		}
		if len(gp) != len(gv) {
			t.Errorf("%s: Param(%d) 与 Value(%d) 数量不一致", rel, len(gp), len(gv))
		}
		if !sameMultiset(gp, wp) {
			t.Errorf("%s: Param 集合与目标不一致", rel)
		}
		if !sameMultiset(gv, wv) {
			t.Errorf("%s: Value 集合与目标不一致", rel)
		}
		for i := range gp {
			if i >= len(gv) {
				t.Errorf("%s: 第 %d 项只有 Param=%s，没有对应 Value", rel, i, gp[i])
				break
			}
			if gp[i] == gv[i] {
				continue
			}
			// 目标自身在该位置也不对应 → 属于目标既有笔误，产物按目标保真。
			if i < len(wp) && i < len(wv) && wp[i] != wv[i] {
				t.Logf("%s: 第 %d 项 Param=%s 与 Value=%s 不对应；目标自身同样如此，按目标保真",
					rel, i, gp[i], gv[i])
				continue
			}
			t.Errorf("%s: 第 %d 项不对应: Param=%s vs Value=%s", rel, i, gp[i], gv[i])
			break
		}
		if strings.Join(gp, "|") != strings.Join(wp, "|") {
			t.Logf("%s: Param 顺序与目标不同（顺序语义无关，仅记录）", rel)
		}
	}
}

// sameMultiset 报告两个字符串切片是否为同一多重集合。
func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
