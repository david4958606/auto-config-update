package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// writeFile 在测试工作目录里落一份文件(自动建目录)。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNewFilePrimitive 覆盖 new-file 原语：新建、plan 不落盘、已存在不覆盖、二次 apply 幂等。
func TestNewFilePrimitive(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")

	step := feature.Step{
		Name:    "新建 Setup/New.xml",
		NewFile: "Setup/New.xml",
		Content: "<New>\n  <Param name=\"X\" />\n</New>",
	}
	feat := &feature.Feature{ID: "newfile", Steps: []feature.Step{step}}

	// plan：不落盘。
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, false, func(string) {}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(work, "config", "Setup", "New.xml")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("plan 模式不应新建文件: %v", err)
	}

	// apply：逐字新建(内容原样，含是否带行尾换行)。
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != step.Content {
		t.Fatalf("新建内容不符:\n%q", string(got))
	}

	// 已存在 → 不覆盖(幂等)。
	writeFile(t, target, "KEEP\n")
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "KEEP\n" {
		t.Fatalf("已存在文件被覆盖: %q", string(got))
	}
}

// TestNewFileSplitSteps 校验 new-file 步骤被归入文件级步骤(不参与腔室循环)。
func TestNewFileSplitSteps(t *testing.T) {
	chamber, files := SplitSteps([]feature.Step{
		{Name: "chamber", Anchor: "Ch1"},
		{Name: "new", NewFile: "Setup/A.xml", Content: "x"},
		{Name: "file", File: "SysLog_config.xml", Anchor: "SysLog"},
	})
	if len(chamber) != 1 || chamber[0].Name != "chamber" {
		t.Fatalf("chamber 分组错误: %+v", chamber)
	}
	if len(files) != 2 || !strings.Contains(files[0].NewFile, "A.xml") || files[1].File == "" {
		t.Fatalf("files 分组错误: %+v", files)
	}
}
