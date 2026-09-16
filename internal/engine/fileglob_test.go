package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
	"addex/internal/setupcheck"
)

// globSetupDoc 生成一份最小 Setup 文件(根标签为 root，含一个 Param A + Value A)。
func globSetupDoc(root string) string {
	return "<" + root + ">\n" +
		"  <Param name=\"A\" dataObject=\"/A\" type=\"D\" min=\"0\" max=\"1\" units=\"\" accuracy=\"0.1\" default=\"0\"/>\n" +
		"  <Option index=\"1\">\n" +
		"    <Value paramName=\"A\">0</Value>\n" +
		"  </Option>\n" +
		"</" + root + ">\n"
}

// TestFileStepGlob 覆盖 file: 的 glob 展开：匹配到的每个文件各执行一次(按字典序)、
// 未匹配的文件不动、一致性校验通过、二次 apply 幂等。
func TestFileStepGlob(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")
	dir := filepath.Join(work, "config", "Setup")
	writeFile(t, filepath.Join(dir, "A1Setup.xml"), globSetupDoc("A1Setup"))
	writeFile(t, filepath.Join(dir, "B2Setup.xml"), globSetupDoc("B2Setup"))
	other := filepath.Join(dir, "Other.xml")
	writeFile(t, other, globSetupDoc("Other"))
	otherBefore, _ := os.ReadFile(other)

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: glob-demo
version: 1
steps:
  - name: 所有 *Setup.xml 加参数
    file: Setup/*Setup.xml
    add-setup:
      - param: NewParam
        dataObject: /New
        type: D
        min: 0
        max: 10
        units: "%"
        accuracy: 0.1
        default: 1
        value: 1
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

	for _, n := range []string{"A1Setup.xml", "B2Setup.xml"} {
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		out := string(raw)
		if !strings.Contains(out, `<Param name="NewParam"`) || !strings.Contains(out, `<Value paramName="NewParam">1</Value>`) {
			t.Fatalf("%s 未按 glob 展开命中:\n%s", n, out)
		}
		issues, err := setupcheck.CheckFile(filepath.Join(dir, n), "Setup/"+n)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 {
			t.Fatalf("%s 一致性校验不应有问题: %+v", n, issues)
		}
	}
	// 不匹配的文件必须逐字节不动。
	if after, _ := os.ReadFile(other); string(after) != string(otherBefore) {
		t.Fatalf("未匹配的 Other.xml 被改动:\n%s", after)
	}
	// glob 结果按字典序：A1 在 B2 之前。
	if i, j := strings.Index(logged, "[文件 Setup/A1Setup.xml]"), strings.Index(logged, "[文件 Setup/B2Setup.xml]"); i < 0 || j < 0 || i > j {
		t.Fatalf("glob 结果顺序不是字典序(i=%d, j=%d):\n%s", i, j, logged)
	}

	// 二次 apply：逐字节幂等。
	a1 := filepath.Join(dir, "A1Setup.xml")
	before, _ := os.ReadFile(a1)
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(a1); string(after) != string(before) {
		t.Fatalf("二次 apply 不幂等:\n%s", after)
	}
}

// TestFileStepGlobNoMatch 校验 glob 无匹配时只告警、不报错(区别于字面路径缺失会报错)。
func TestFileStepGlobNoMatch(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")
	writeFile(t, filepath.Join(work, "config", "Setup", "A.xml"), globSetupDoc("A"))

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: glob-none
version: 1
steps:
  - name: 不存在的模式
    file: Setup/Nope*.xml
    add-element: [ { tag: X, text: "1" } ]
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
		t.Fatalf("glob 无匹配不应报错: %v", err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "[文件 Setup/Nope*.xml] ! 跳过：无匹配文件") {
		t.Fatalf("应打印无匹配告警:\n%s", strings.Join(logs, "\n"))
	}
}

// TestFileStepGlobPerChamber 校验 glob 与按腔室展开组合：先按腔室替换占位符，再对结果做 glob。
func TestFileStepGlobPerChamber(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))
	dir := filepath.Join(work, "config", "Setup")
	writeFile(t, filepath.Join(dir, "Setup_Ch1.xml"), globSetupDoc("Ch1Setup"))
	writeFile(t, filepath.Join(dir, "Setup_Ch1Extra.xml"), globSetupDoc("Ch1ExtraSetup"))
	writeFile(t, filepath.Join(dir, "Setup_Ch2.xml"), globSetupDoc("Ch2Setup"))

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: glob-per-chamber
version: 1
steps:
  - name: 各腔室 *Setup.xml 加参数
    file: Setup/Setup_${Chamber}*.xml
    add-setup:
      - param: P${Chamber}
        dataObject: /SETUP/${Chamber}/P
        type: D
        min: 0
        max: 10
        units: "%"
        accuracy: 0.1
        default: 1
        value: 1
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

	// Ch1 的两个文件都命中(参数名带 Ch1)；Ch2 的文件只命中自己的那个。
	for _, tc := range []struct{ file, param string }{
		{"Setup_Ch1.xml", "PCh1"}, {"Setup_Ch1Extra.xml", "PCh1"}, {"Setup_Ch2.xml", "PCh2"},
	} {
		raw, err := os.ReadFile(filepath.Join(dir, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		out := string(raw)
		if !strings.Contains(out, `<Param name="`+tc.param+`"`) {
			t.Fatalf("%s 未命中 %s:\n%s", tc.file, tc.param, out)
		}
		if strings.Contains(out, "PCh2") && tc.param == "PCh1" {
			t.Fatalf("%s 串入了 Ch2:\n%s", tc.file, out)
		}
	}
}
