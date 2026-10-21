package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
	"addex/internal/xmldoc"
)

// masterBodyFixture 构造最小装配：Control 片段(Control_Ch1) + IO 片段 + 两份 master。
// Control_config.xml 正文里放了三类"不属于任何片段"的节点：
//   - CleanRuleRtManager：单个正文节点(用户实际要改的那类)；
//   - PumpA/PumpB：同 class(FuncPumpGroup) 的两个正文实例，验证正文内 fan-out；
//   - Remote：与片段里的 Local 同 class(SharedClass)，验证片段优先、不被改写成 master 步骤。
func masterBodyFixture(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	writeFile(t, filepath.Join(configDir, "Control", "Control_config.xml"), `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE Control [
<!ENTITY Ctrl_Ch1 SYSTEM "./Control_Ch1">
]>
<Control>
    &Ctrl_Ch1;

    <CleanRuleRtManager class="CleanRuleRtManager" type="instance" alias="" simulated="false">
       <setSetup type="method">CleanRuleRtInfo</setSetup>
    </CleanRuleRtManager>

    <PumpA class="FuncPumpGroup" type="instance" alias="">
        <setGroupID type="method">1</setGroupID>
    </PumpA>

    <PumpB class="FuncPumpGroup" type="instance" alias="">
        <setGroupID type="method">2</setGroupID>
    </PumpB>

    <Remote class="SharedClass" type="instance">
        <seed type="method">2</seed>
    </Remote>
</Control>
`)
	writeFile(t, filepath.Join(configDir, "Control", "Control_Ch1"),
		"<Ch1 class=\"ITO\" alias=\"/Control/Ch1Exports/Ch1\">\n"+
			"    <setDbTable type=\"method\">mysql</setDbTable>\n"+
			"    <Local class=\"SharedClass\" type=\"instance\">\n"+
			"        <seed type=\"method\">1</seed>\n"+
			"    </Local>\n"+
			"</Ch1>\n")
	writeFile(t, filepath.Join(configDir, "IO_config.xml"), `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE IO [
<!ENTITY IO_Ch1 SYSTEM "./IOBridge/IO_Ch1">
]>
<IO>
    &IO_Ch1;
</IO>
`)
	writeFile(t, filepath.Join(configDir, "IOBridge", "IO_Ch1"),
		"<Ch1>\n    <IG class=\"PhyGauge\"/>\n</Ch1>\n")
	return configDir
}

// loadFeatureYAML 把 YAML 原文写到临时文件并加载。
func loadFeatureYAML(t *testing.T, yamlText string) *feature.Feature {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feature.yaml")
	writeFile(t, path, yamlText)
	f, err := feature.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// runEngine 执行一次 plan/apply，返回全部日志。
func runEngine(t *testing.T, e *Engine, f *feature.Feature, write bool) []string {
	t.Helper()
	var logs []string
	if err := e.ApplyFeature(f, nil, write, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	return logs
}

// semanticLogs 只保留"  步[...]"这类语义结果行(plan 与 apply 应完全一致)。
func semanticLogs(logs []string) []string {
	var out []string
	for _, l := range logs {
		if strings.HasPrefix(l, "  步[") {
			out = append(out, l)
		}
	}
	return out
}

func readXML(t *testing.T, path string) (*xmldoc.Document, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xmldoc.Parse(raw)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	return doc, raw
}

// TestMasterBodyAnchorRoutedAsFileStep 是用户用例的端到端回归：
// anchor /Control/CleanRuleRtManager 只在 master 正文命中时，应自动按文件级步骤执行一次，
// plan 不落盘、apply 只改目标字节、二次 apply 幂等。
func TestMasterBodyAnchorRoutedAsFileStep(t *testing.T) {
	configDir := masterBodyFixture(t)
	masterPath := filepath.Join(configDir, "Control", "Control_config.xml")
	chamberPath := filepath.Join(configDir, "Control", "Control_Ch1")
	_, masterBefore := readXML(t, masterPath)
	_, chamberBefore := readXML(t, chamberPath)

	f := loadFeatureYAML(t, `id: HM_MaxCleanTime
description: CleanBySequence最多执行一个Clean
version: 1
steps:
  - name: CleanRuleRtManager
    anchor: /Control/CleanRuleRtManager
    add-method:
      - name: setMaxCleanTime
        value: 10
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}

	// plan：不落盘，但语义结果与 apply 一致，并明确提示"按 master 正文步骤执行"。
	planLogs := runEngine(t, eng, f, false)
	if _, raw := readXML(t, masterPath); string(raw) != string(masterBefore) {
		t.Fatalf("plan 修改了 master:\n%s", raw)
	}
	if !strings.Contains(strings.Join(planLogs, "\n"), "正文节点") {
		t.Fatalf("缺少正文节点路由日志:\n%s", strings.Join(planLogs, "\n"))
	}
	planSemantic := semanticLogs(planLogs)
	if !strings.Contains(strings.Join(planSemantic, "\n"), "+ 新增方法 setMaxCleanTime(10)") {
		t.Fatalf("plan 未新增方法:\n%s", strings.Join(planSemantic, "\n"))
	}

	// apply：只改 master 目标字节，片段不动。
	applyLogs := runEngine(t, eng, f, true)
	if got, want := strings.Join(semanticLogs(applyLogs), "\n"), strings.Join(planSemantic, "\n"); got != want {
		t.Fatalf("plan/apply 语义不一致:\nplan:\n%s\napply:\n%s", want, got)
	}
	doc, afterFirst := readXML(t, masterPath)
	manager := xmldoc.FindChild(doc.Roots[0], "CleanRuleRtManager")
	if manager == nil {
		t.Fatal("master 正文缺少 CleanRuleRtManager")
	}
	m := xmldoc.FindChild(manager, "setMaxCleanTime")
	if m == nil || m.Attr("type") != "method" || m.Text != "10" {
		t.Fatalf("master 未正确新增方法: %+v", m)
	}
	if _, raw := readXML(t, chamberPath); string(raw) != string(chamberBefore) {
		t.Fatalf("腔室片段不应被改动:\n%s", raw)
	}

	// 二次 apply 幂等：字节不变，并打印"已存在"。
	secondLogs := runEngine(t, eng, f, true)
	if _, raw := readXML(t, masterPath); string(raw) != string(afterFirst) {
		t.Fatalf("二次 apply 改动了文件:\n%s", raw)
	}
	if !strings.Contains(strings.Join(secondLogs, "\n"), "已存在") {
		t.Fatalf("二次 apply 未判定幂等:\n%s", strings.Join(secondLogs, "\n"))
	}
}

// TestMasterBodyClassFanOut 校验 master 正文里同类多实例逐实例执行，且 {X} 绑定为该实例标签。
func TestMasterBodyClassFanOut(t *testing.T) {
	configDir := masterBodyFixture(t)
	masterPath := filepath.Join(configDir, "Control", "Control_config.xml")
	chamberPath := filepath.Join(configDir, "Control", "Control_Ch1")
	_, chamberBefore := readXML(t, chamberPath)

	f := loadFeatureYAML(t, `id: master-fanout
version: 1
steps:
  - name: 给每个正文泵组加方法
    anchor: /Control/{FuncPumpGroup}
    where:
      tag-glob: "Pump*"
    add-method:
      - name: setBodyTag
        value: "{FuncPumpGroup}"
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(runEngine(t, eng, f, true), "\n")
	if !strings.Contains(logs, "正文节点") {
		t.Fatalf("多实例正文节点应改写为文件级步骤:\n%s", logs)
	}
	doc, _ := readXML(t, masterPath)
	for _, tag := range []string{"PumpA", "PumpB"} {
		pump := xmldoc.FindChild(doc.Roots[0], tag)
		if pump == nil {
			t.Fatalf("master 缺少 %s", tag)
		}
		m := xmldoc.FindChild(pump, "setBodyTag")
		if m == nil || m.Text != tag {
			t.Fatalf("%s 的 setBodyTag 值应为自身标签 %q: %+v", tag, tag, m)
		}
	}
	if _, raw := readXML(t, chamberPath); string(raw) != string(chamberBefore) {
		t.Fatalf("腔室片段不应被改动:\n%s", raw)
	}
}

// TestChamberAnchorStillWinsOverMasterBody 校验同 class 在两处都有时保持腔室语义：
// 只改本腔室片段，不误改写为 master 正文步骤。
func TestChamberAnchorStillWinsOverMasterBody(t *testing.T) {
	configDir := masterBodyFixture(t)
	masterPath := filepath.Join(configDir, "Control", "Control_config.xml")
	chamberPath := filepath.Join(configDir, "Control", "Control_Ch1")

	f := loadFeatureYAML(t, `id: chamber-wins
version: 1
steps:
  - name: 只改腔室里的 SharedClass
    anchor: /Control/{SharedClass}
    add-method:
      - name: setChamberOnly
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(runEngine(t, eng, f, true), "\n")
	if strings.Contains(logs, "正文节点") {
		t.Fatalf("腔室里能命中的 anchor 不应被改写为 master 步骤:\n%s", logs)
	}
	chamberDoc, _ := readXML(t, chamberPath)
	local := xmldoc.FindChild(chamberDoc.Roots[0], "Local")
	if local == nil || xmldoc.FindChild(local, "setChamberOnly") == nil {
		t.Fatalf("腔室片段未新增方法:\n%s", chamberDoc.Src)
	}
	masterDoc, _ := readXML(t, masterPath)
	remote := xmldoc.FindChild(masterDoc.Roots[0], "Remote")
	if remote == nil {
		t.Fatal("master 缺少 Remote")
	}
	if xmldoc.FindChild(remote, "setChamberOnly") != nil {
		t.Fatalf("master 正文 Remote 不应被改动:\n%s", masterDoc.Src)
	}
}

// TestMasterBodyRequireLogicalPath 校验 require 的逻辑路径也能解析 master 正文节点：
// 守卫命中才执行；不扩展 ResolveLogical 时这一步会被跳过。
func TestMasterBodyRequireLogicalPath(t *testing.T) {
	configDir := masterBodyFixture(t)
	masterPath := filepath.Join(configDir, "Control", "Control_config.xml")

	f := loadFeatureYAML(t, `id: master-require
version: 1
steps:
  - name: 仅当已有 setSetup 时加方法
    anchor: /Control/CleanRuleRtManager
    require:
      - exist: { Setup: "/Control/CleanRuleRtManager/setSetup" }
    add-method:
      - name: setGuardOk
        value: "{Setup}"
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(runEngine(t, eng, f, true), "\n")
	if strings.Contains(logs, "跳过") {
		t.Fatalf("require.exist 应能解析 master 正文路径:\n%s", logs)
	}
	doc, _ := readXML(t, masterPath)
	manager := xmldoc.FindChild(doc.Roots[0], "CleanRuleRtManager")
	if manager == nil {
		t.Fatal("master 缺少 CleanRuleRtManager")
	}
	m := xmldoc.FindChild(manager, "setGuardOk")
	if m == nil || m.Text != "/Control/CleanRuleRtManager/setSetup" {
		t.Fatalf("绑定值/新增方法不符: %+v", m)
	}
}

// TestMasterBodyStepRunsIndependentlyOfChamberSelection 校验 master 正文步骤是全局步骤：
// --chamber 只筛选腔室步骤，master 正文只改一次。
func TestMasterBodyStepRunsIndependentlyOfChamberSelection(t *testing.T) {
	configDir := masterBodyFixture(t)
	masterPath := filepath.Join(configDir, "Control", "Control_config.xml")

	f := loadFeatureYAML(t, `id: master-selection
version: 1
steps:
  - name: 正文加方法
    anchor: /Control/CleanRuleRtManager
    add-method:
      - name: setGlobal
  - name: 腔室加方法(仅 Ch1)
    anchor: /Control/{ITO}
    add-method:
      - name: setChamber
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(f, []string{"Ch1"}, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(logs, "\n")
	if strings.Count(joined, "setGlobal") != 1 {
		t.Fatalf("master 正文步骤应全局执行一次:\n%s", joined)
	}
	doc, _ := readXML(t, masterPath)
	manager := xmldoc.FindChild(doc.Roots[0], "CleanRuleRtManager")
	if manager == nil || xmldoc.FindChild(manager, "setGlobal") == nil {
		t.Fatalf("master 正文未新增 setGlobal:\n%s", doc.Src)
	}
	chamberDoc, _ := readXML(t, filepath.Join(configDir, "Control", "Control_Ch1"))
	root := chamberDoc.Roots[0]
	if xmldoc.FindChild(root, "setChamber") == nil {
		t.Fatalf("选中的 Ch1 未新增 setChamber:\n%s", chamberDoc.Src)
	}
}
