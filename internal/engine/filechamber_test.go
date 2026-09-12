package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// setupFileFor 生成一份最小 Setup 文件内容(带一个既有 Param/Value)。
func setupFileFor(name string) string {
	return "<" + name + "Setup>\n" +
		"  <Param name=\"A\" dataObject=\"/A\" type=\"D\" min=\"0\" max=\"1\" units=\"\" accuracy=\"0.1\" default=\"0\"/>\n" +
		"  <Option index=\"1\">\n" +
		"    <Value paramName=\"A\">0</Value>\n" +
		"  </Option>\n" +
		"</" + name + "Setup>\n"
}

// TestFileStepExpandsPerChamber 覆盖 `file:` 按腔室展开：
// 路径含占位符的步骤逐腔室解析后各执行一次；缺失的文件只告警跳过；字面量步骤仍全局一次。
func TestFileStepExpandsPerChamber(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))
	setupDir := filepath.Join(work, "config", "Setup")
	// 只给 Ch1/Ch2 准备文件；Ch3/Ch4/ChC/ChD 缺失 → 应告警跳过。
	writeFile(t, filepath.Join(setupDir, "Setup_Ch1.xml"), setupFileFor("Ch1"))
	writeFile(t, filepath.Join(setupDir, "Setup_Ch2.xml"), setupFileFor("Ch2"))
	// 字面量路径的共享文件：应只执行一次(即使 feature 里同时有按腔室展开的步骤)。
	writeFile(t, filepath.Join(setupDir, "Shared.xml"), "<SharedSetup>\n</SharedSetup>\n")

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: per-chamber
version: 1
steps:
  - name: Setup/Setup_${Chamber}.xml 升级
    file: Setup/Setup_${Chamber}.xml
    add-setup:
      - param: P${Chamber}
        dataObject: /SETUP/${Chamber}/P
        type: D
        min: 0
        max: 100
        units: "%"
        accuracy: 0.1
        default: 1
        value: 1

  - name: 共享文件只处理一次
    file: Setup/Shared.xml
    anchor: SharedSetup
    add-element:
      - { tag: Marker, text: "x" }
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	logged := strings.Join(logs, "\n")

	// Ch1/Ch2 的 Param/Value 名带腔室名，且互不串台。
	ch1, _ := os.ReadFile(filepath.Join(setupDir, "Setup_Ch1.xml"))
	ch2, _ := os.ReadFile(filepath.Join(setupDir, "Setup_Ch2.xml"))
	if !strings.Contains(string(ch1), `<Param name="PCh1" dataObject="/SETUP/Ch1/P"`) ||
		!strings.Contains(string(ch1), `<Value paramName="PCh1">1</Value>`) {
		t.Fatalf("Ch1 未按腔室替换占位符:\n%s", ch1)
	}
	if strings.Contains(string(ch1), "Ch2") {
		t.Fatalf("Ch1 文件串入了 Ch2 的内容:\n%s", ch1)
	}
	if !strings.Contains(string(ch2), `<Param name="PCh2" dataObject="/SETUP/Ch2/P"`) ||
		!strings.Contains(string(ch2), `<Value paramName="PCh2">1</Value>`) {
		t.Fatalf("Ch2 未按腔室替换占位符:\n%s", ch2)
	}

	// 缺失的腔室文件：告警跳过，而非中止。
	for _, ch := range []string{"Ch3", "Ch4", "ChC", "ChD"} {
		if !strings.Contains(logged, "[文件 Setup/Setup_"+ch+".xml] ! 跳过：文件不存在") {
			t.Errorf("缺少 %s 的跳过告警:\n%s", ch, logged)
		}
	}
	if _, err := os.Stat(filepath.Join(setupDir, "Setup_Ch3.xml")); !os.IsNotExist(err) {
		t.Errorf("不应为缺失的腔室建文件: %v", err)
	}

	// 字面量路径步骤只处理一次。
	shared, _ := os.ReadFile(filepath.Join(setupDir, "Shared.xml"))
	if got := strings.Count(string(shared), "<Marker>x</Marker>"); got != 1 {
		t.Fatalf("共享文件应只被处理一次，Marker 实得 %d 个:\n%s", got, shared)
	}

	// 二次 apply 逐字节幂等。
	before1, _ := os.ReadFile(filepath.Join(setupDir, "Setup_Ch1.xml"))
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	after1, _ := os.ReadFile(filepath.Join(setupDir, "Setup_Ch1.xml"))
	if string(before1) != string(after1) {
		t.Fatalf("二次 apply 不幂等:\n%s", after1)
	}
}

// TestFileStepPerChamberRespectsSelection 校验 --chamber 限定同样作用于按腔室展开的文件步骤。
func TestFileStepPerChamberRespectsSelection(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))
	setupDir := filepath.Join(work, "config", "Setup")
	writeFile(t, filepath.Join(setupDir, "Setup_Ch1.xml"), setupFileFor("Ch1"))
	writeFile(t, filepath.Join(setupDir, "Setup_Ch2.xml"), setupFileFor("Ch2"))

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: per-chamber-sel
version: 1
steps:
  - name: Setup/Setup_${Chamber}.xml 升级
    file: Setup/Setup_${Chamber}.xml
    add-element:
      - { tag: OnlySelected, text: "1" }
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(feat, []string{"Ch2"}, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	ch1, _ := os.ReadFile(filepath.Join(setupDir, "Setup_Ch1.xml"))
	ch2, _ := os.ReadFile(filepath.Join(setupDir, "Setup_Ch2.xml"))
	if strings.Contains(string(ch1), "OnlySelected") {
		t.Fatalf("未选中的 Ch1 不应被改动:\n%s", ch1)
	}
	if !strings.Contains(string(ch2), "<OnlySelected>1</OnlySelected>") {
		t.Fatalf("选中的 Ch2 未被改动:\n%s", ch2)
	}
	if logged := strings.Join(logs, "\n"); strings.Contains(logged, "Setup_Ch1.xml") {
		t.Fatalf("未选中的腔室不应产生日志:\n%s", logged)
	}
}

// TestNewFileStepExpandsPerChamber 校验 new-file 路径也能按腔室展开；content 仍按字面写入。
func TestNewFileStepExpandsPerChamber(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))

	const content = "<New>\n  <Option index=\"1\"></Option>\n</New>\n"
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: new-per-chamber
version: 1
steps:
  - name: 新建 Setup/New_${Chamber}.xml
    new-file: Setup/New_${Chamber}.xml
    content: |
      <New>
        <Option index="1"></Option>
      </New>
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"Ch1", "Ch2", "Ch3", "Ch4", "ChC", "ChD"} {
		p := filepath.Join(work, "config", "Setup", "New_"+ch+".xml")
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("未按腔室新建 %s: %v", ch, err)
		}
		// content 逐字写入(含块标量自带的缩进与行尾换行)，不做占位符替换。
		if string(got) != content {
			t.Fatalf("%s 内容不符(应为字面 content):\n%q", ch, got)
		}
	}

	// 已存在 → no-op(不覆盖)：改写其中一个再跑一遍。
	target := filepath.Join(work, "config", "Setup", "New_Ch1.xml")
	writeFile(t, target, "KEEP\n")
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "KEEP\n" {
		t.Fatalf("已存在的新文件被覆盖: %q", got)
	}
}
