package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/xmldoc"
)

// multiRootFragmentFixture 构造最小装配：Control_config.xml 用实体引入一个多顶层元素的
// 片段 Control_Platform(<ATR>、<VTR> 两个"根")。装配后它们是 <Control> 的直接子节点。
func multiRootFragmentFixture(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, "Control", "Control_config.xml"), `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE Control [
<!ENTITY Ctrl_Platform SYSTEM "./Control_Platform">
]>
<Control>
    &Ctrl_Platform;
</Control>
`)
	// 片段故意用 CRLF，验证新增顶层元素沿用原文换行。
	writeFile(t, filepath.Join(configDir, "Control", "Control_Platform"),
		"<ATR class=\"OprRobot\" type=\"instance\" alias=\"/Control/ATRExports/ATR\">\r\n"+
			"    <exportState type=\"method\">/IO/ATRExports/State</exportState>\r\n"+
			"</ATR>\r\n"+
			"\r\n"+
			"<VTR class=\"OprRobot\" type=\"instance\" alias=\"/Control/VTRExports/VTR\">\r\n"+
			"    <exportState type=\"method\">/IO/VTRExports/State</exportState>\r\n"+
			"</VTR>\r\n")
	writeFile(t, filepath.Join(configDir, "IO_config.xml"), "<IO></IO>\n")
	return configDir
}

// TestFileStepAddNodeToMultiRootFragment 是用户用例的端到端回归：
// file: 指向 master 用实体引入的多根片段时，空 anchor 表示 master 根(<Control>)，
// add-node 应新增一个顶层元素(装配后 <Control> 的又一个子节点)，而不是塞进每个既有根。
// 同时覆盖 plan 不写盘、apply 只插入目标字节、二次 apply 幂等。
func TestFileStepAddNodeToMultiRootFragment(t *testing.T) {
	configDir := multiRootFragmentFixture(t)
	fragPath := filepath.Join(configDir, "Control", "Control_Platform")
	masterPath := filepath.Join(configDir, "Control", "Control_config.xml")
	before, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	masterBefore, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}

	feat := loadFeatureYAML(t, `id: HM_SystemMonitor
description: Sysmonitor监控功能
version: 1
steps:
  - name: 新建SystemMonitor节点
    file: Control/Control_Platform
    add-node:
      - tag: SystemMonitor
        class: SystemMonitor
        attrs: { type: instance, alias: "" }
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}

	// plan 不写盘。
	runEngine(t, eng, feat, false)
	if got, _ := os.ReadFile(fragPath); string(got) != string(before) {
		t.Fatalf("plan 不应写盘，文件已变化:\n%s", got)
	}

	// apply：只在最后一个顶层元素之后插入一个新顶层元素，其余字节逐字保留(含 CRLF)。
	logs := runEngine(t, eng, feat, true)
	after, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	newNode := "<SystemMonitor class=\"SystemMonitor\" type=\"instance\" alias=\"\"/>\r\n"
	want := strings.Replace(string(before), "</VTR>\r\n", "</VTR>\r\n\r\n"+newNode, 1)
	if string(after) != want {
		t.Fatalf("期望只插入一个顶层节点，实得:\n%s", after)
	}
	if n := strings.Count(string(after), "<SystemMonitor"); n != 1 {
		t.Fatalf("期望恰好 1 个 <SystemMonitor>，实得 %d 个", n)
	}
	doc, err := xmldoc.Parse(after)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Roots) != 3 {
		t.Fatalf("期望 3 个顶层元素(ATR/VTR/SystemMonitor)，实得 %d", len(doc.Roots))
	}
	added := doc.Roots[2]
	if added.Tag != "SystemMonitor" || added.Class() != "SystemMonitor" ||
		added.Attr("type") != "instance" || added.Attr("alias") != "" {
		t.Fatalf("新增节点属性不符: %+v", added)
	}
	if !containsLog(logs, "新增对象 <SystemMonitor") {
		t.Fatalf("验证 diff 日志缺少新增行: %v", logs)
	}
	// master 与其它文件不动。
	if got, _ := os.ReadFile(masterPath); string(got) != string(masterBefore) {
		t.Fatal("Control_config.xml 不应被改动")
	}

	// 二次 apply：幂等，字节不变。
	runEngine(t, eng, feat, true)
	if again, _ := os.ReadFile(fragPath); string(again) != string(after) {
		t.Fatalf("二次 apply 应幂等，实得:\n%s", again)
	}
}

// TestFileStepAddNodeToFragmentAfterSibling 覆盖 after 定位：新顶层元素插到指定顶层元素之后
// (即其下一个顶层元素所在行之前)，保持文件缩进/换行。
func TestFileStepAddNodeToFragmentAfterSibling(t *testing.T) {
	configDir := multiRootFragmentFixture(t)
	fragPath := filepath.Join(configDir, "Control", "Control_Platform")
	before, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	feat := loadFeatureYAML(t, `id: sysmon-after
version: 1
steps:
  - name: ATR 之后插入 SystemMonitor
    file: Control/Control_Platform
    add-node:
      - tag: SystemMonitor
        class: SystemMonitor
        attrs: { type: instance, alias: "" }
        after: { tag: ATR }
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}
	runEngine(t, eng, feat, true)

	after, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	newNode := "<SystemMonitor class=\"SystemMonitor\" type=\"instance\" alias=\"\"/>\r\n"
	want := strings.Replace(string(before), "\r\n<VTR", "\r\n"+newNode+"<VTR", 1)
	if string(after) != want {
		t.Fatalf("after 定位不符，实得:\n%s", after)
	}
	doc, err := xmldoc.Parse(after)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Roots) != 3 || doc.Roots[0].Tag != "ATR" || doc.Roots[1].Tag != "SystemMonitor" || doc.Roots[2].Tag != "VTR" {
		t.Fatalf("期望顶层顺序 ATR/SystemMonitor/VTR，实得 %d 个根", len(doc.Roots))
	}
}

// TestFileStepFragmentAnchorStillResolves 确认包裹伪根后，非空 anchor 仍能按原有方式
// 定位片段内的顶层元素；首段既可写顶层标签，也可带 master 根标签作前缀。
func TestFileStepFragmentAnchorStillResolves(t *testing.T) {
	for _, anchor := range []string{"ATR", "Control/ATR"} {
		t.Run(anchor, func(t *testing.T) {
			configDir := multiRootFragmentFixture(t)
			fragPath := filepath.Join(configDir, "Control", "Control_Platform")
			before, err := os.ReadFile(fragPath)
			if err != nil {
				t.Fatal(err)
			}
			feat := loadFeatureYAML(t, `id: sysmon-anchor
version: 1
steps:
  - name: 在 ATR 内加方法
    file: Control/Control_Platform
    anchor: `+anchor+`
    add-method:
      - { name: setProbe, value: "1" }
`)
			eng, err := New(configDir)
			if err != nil {
				t.Fatal(err)
			}
			runEngine(t, eng, feat, true)

			after, err := os.ReadFile(fragPath)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(string(before),
				"    <exportState type=\"method\">/IO/ATRExports/State</exportState>\r\n",
				"    <exportState type=\"method\">/IO/ATRExports/State</exportState>\r\n"+
					"    <setProbe type=\"method\">1</setProbe>\r\n", 1)
			if string(after) != want {
				t.Fatalf("anchor %q 的修改不符，实得:\n%s", anchor, after)
			}
			// 仍保持 ATR/VTR 两个顶层元素，没有额外顶层节点。
			doc, err := xmldoc.Parse(after)
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Roots) != 2 {
				t.Fatalf("期望仍为 2 个顶层元素，实得 %d", len(doc.Roots))
			}
		})
	}
}

// containsLog 报告日志里是否有一行包含 substr。
func containsLog(logs []string, substr string) bool {
	for _, l := range logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
