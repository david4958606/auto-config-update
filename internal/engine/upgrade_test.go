package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"addex/internal/feature"
	"addex/internal/setupcheck"
	"addex/internal/xmlcmp"
	"addex/internal/xmldoc"
)

// upgradeFeatures 是按顺序施加的升级 feature(见 doc/config-upgrade-design.md §5)。
var upgradeFeatures = []string{
	"upgrade-files.yaml",
	"upgrade-io.yaml",
	"upgrade-control.yaml",
}

// TestUpgradeExampleConfig 以 example-config/config_old 为"旧配置"，依次 apply 升级 feature，
// 再与用户提供的 example-config/config 做**语义比对**(见 internal/xmlcmp)。
// 同时验证二次 apply 幂等。
func TestUpgradeExampleConfig(t *testing.T) {
	root := filepath.Join("..", "..")
	oldDir := filepath.Join(root, "example-config", "config_old")
	newDir := filepath.Join(root, "example-config", "config")
	if _, err := os.Stat(oldDir); err != nil {
		t.Skipf("缺少 %s，跳过升级校验", oldDir)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Skipf("缺少 %s，跳过升级校验", newDir)
	}

	work := t.TempDir()
	copyTree(t, oldDir, filepath.Join(work, "config"))

	run := func() {
		for _, name := range upgradeFeatures {
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
	checkSetupParamValueAlignment(t, filepath.Join(work, "config"), newDir)

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

// setupSequences 返回 Setup 文件里 <Param name> 与 <Option> 内 <Value paramName> 的名字序列。
// 二者必须"数量与顺序都一一对应"——这是 Setup 的结构约定(见目标要求)。
// 提取逻辑与 CLI 的 `check` 子命令共用 internal/setupcheck，避免两处口径漂移。
func setupSequences(t *testing.T, path string) (params, values []string, ok bool) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	doc, err := xmldoc.Parse(src)
	if err != nil || len(doc.Roots) == 0 {
		return nil, nil, false
	}
	params, values = setupcheck.Sequences(doc)
	return params, values, true
}

// checkSetupParamValueAlignment 校验 Setup/*.xml 的 Param 与 Value 一一对应(数量+顺序)，
// 且顺序与目标配置一致——这是目标里明确要求的结构约束。
func checkSetupParamValueAlignment(t *testing.T, gotDir, wantDir string) {
	t.Helper()
	gotFiles := listFiles(t, gotDir)
	for rel := range gotFiles {
		if !strings.HasPrefix(rel, "Setup/") || !strings.HasSuffix(rel, ".xml") || ignoreFile(rel) {
			continue
		}
		gp, gv, ok := setupSequences(t, filepath.Join(gotDir, rel))
		if !ok {
			continue
		}
		if strings.HasPrefix(filepath.Base(rel), "SetupRepository") {
			continue // 清单文件没有 Param/Value
		}
		if len(gp) != len(gv) {
			t.Errorf("%s: Param(%d) 与 Value(%d) 数量不一致", rel, len(gp), len(gv))
			continue
		}
		for i := range gp {
			if gp[i] != gv[i] {
				t.Errorf("%s: 第 %d 项不对应: Param=%s vs Value=%s", rel, i, gp[i], gv[i])
				break
			}
		}
		wp, wv, ok := setupSequences(t, filepath.Join(wantDir, rel))
		if !ok {
			continue
		}
		if strings.Join(gp, "|") != strings.Join(wp, "|") {
			t.Errorf("%s: Param 顺序与目标不一致", rel)
		}
		if strings.Join(gv, "|") != strings.Join(wv, "|") {
			t.Errorf("%s: Value 顺序与目标不一致", rel)
		}
	}
}

// compareTree 逐文件做语义比对(配置片段多为 XML；非 XML 文件按字节比较)。
func compareTree(t *testing.T, gotDir, wantDir string) {
	t.Helper()
	wantFiles := listFiles(t, wantDir)
	gotFiles := listFiles(t, gotDir)
	for rel := range wantFiles {
		if ignoreFile(rel) {
			continue
		}
		if _, ok := gotFiles[rel]; !ok {
			t.Errorf("升级产物缺少文件: %s", rel)
			continue
		}
		got, _ := os.ReadFile(filepath.Join(gotDir, rel))
		want, _ := os.ReadFile(filepath.Join(wantDir, rel))
		if strings.HasSuffix(rel, ".xml") || !strings.Contains(rel, ".") {
			diffs, err := xmlcmp.Compare(got, want)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				continue
			}
			for _, d := range diffs {
				if d.Kind == "order" {
					// 同级子节点顺序差异视为语义无关(配置按名/按标签查找)，仅记录。
					t.Logf("%s %s", rel, d)
					continue
				}
				t.Errorf("%s %s", rel, d)
			}
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s: 字节不一致(非 XML 文件)", rel)
		}
	}
	for rel := range gotFiles {
		if ignoreFile(rel) {
			continue
		}
		if _, ok := wantFiles[rel]; !ok {
			t.Errorf("升级产物多出文件: %s", rel)
		}
	}
}

// ignoreFile 跳过备份文件(以 ~ 结尾)——它们是历史残留，不参与升级语义。
func ignoreFile(rel string) bool { return strings.HasSuffix(rel, "~") }

func listFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s: %v", dir, err)
	}
	return out
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := listFiles(t, dir)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, k := range keys {
		b, err := os.ReadFile(filepath.Join(dir, k))
		if err != nil {
			t.Fatal(err)
		}
		out[k] = string(b)
	}
	return out
}
