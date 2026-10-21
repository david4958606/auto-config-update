package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstPositionMaster 的 <Control> 正文以 &实体; 引用开头——这是真实 Control_config.xml 的写法，
// 也是 position: first 必须把新节点插到实体引用之前的回归场景。
const firstPositionMaster = `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE Control [
<!ENTITY Ctrl_Platform SYSTEM "./Control_Platform">
]>
<Control>

    &Ctrl_Platform;    

    <ModuleDockingManager class="ModuleDockingManager" type="instance" alias="/Control/SystemExports/ModuleDockingManager">
        <exportModuleNames type="method">/IO/SystemExports/DockModuleNames</exportModuleNames>
    </ModuleDockingManager>
</Control>
`

func firstPositionFixture(t *testing.T) (configDir, masterPath string) {
	t.Helper()
	configDir = t.TempDir()
	writeFile(t, filepath.Join(configDir, "Control", "Control_config.xml"), firstPositionMaster)
	writeFile(t, filepath.Join(configDir, "Control", "Control_Platform"), `<ATR class="OprRobot" type="instance" alias="/Control/ATRExports/ATR">
    <exportState type="method">/IO/ATRExports/State</exportState>
</ATR>
`)
	writeFile(t, filepath.Join(configDir, "IO_config.xml"), "<IO></IO>\n")
	return configDir, filepath.Join(configDir, "Control", "Control_config.xml")
}

// TestAddNodePositionFirstBeforeEntities 回归 position: first：新对象必须紧接 <Control> 开标签，
// 先于 &实体; 引用与既有节点；plan 不写盘，apply 只插入目标字节，二次 apply 幂等。
func TestAddNodePositionFirstBeforeEntities(t *testing.T) {
	configDir, masterPath := firstPositionFixture(t)
	before, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}

	feat := loadFeatureYAML(t, `id: first-position
version: 1
steps:
  - name: Control 开头新建 SystemMonitor
    file: Control/Control_config.xml
    anchor: Control
    add-node:
      - tag: SystemMonitor
        class: SystemMonitor
        attrs: { type: instance, alias: "" }
        position: first
  - name: 追加方法
    file: Control/Control_config.xml
    anchor: Control/SystemMonitor
    add-method:
      - { name: setInterval, value: "3000", attrs: { comment: ms } }
      - { name: setNohupOutputFileName, value: nohup_mec_output.txt, attrs: { comment: "" } }
`)
	eng, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}

	// plan 不写盘。
	runEngine(t, eng, feat, false)
	if got, _ := os.ReadFile(masterPath); string(got) != string(before) {
		t.Fatalf("plan 不应写盘，文件已变化:\n%s", got)
	}

	logs := runEngine(t, eng, feat, true)
	after, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(firstPositionMaster,
		"<Control>\n\n    &Ctrl_Platform;",
		"<Control>\n"+
			"    <SystemMonitor class=\"SystemMonitor\" type=\"instance\" alias=\"\">\n"+
			"        <setInterval type=\"method\" comment=\"ms\">3000</setInterval>\n"+
			"        <setNohupOutputFileName type=\"method\" comment=\"\">nohup_mec_output.txt</setNohupOutputFileName>\n"+
			"    </SystemMonitor>\n\n"+
			"    &Ctrl_Platform;", 1)
	if string(after) != want {
		t.Fatalf("首位置插入结果不符:\n--- got ---\n%s\n--- want ---\n%s", after, want)
	}
	sm := strings.Index(string(after), "<SystemMonitor")
	ent := strings.Index(string(after), "&Ctrl_Platform;")
	mod := strings.Index(string(after), "<ModuleDockingManager")
	if sm < 0 || ent < 0 || mod < 0 || !(sm < ent && sm < mod) {
		t.Fatalf("SystemMonitor 应位于所有实体引用与既有节点之前(sm=%d ent=%d mod=%d):\n%s", sm, ent, mod, after)
	}
	if n := strings.Count(string(after), "<SystemMonitor"); n != 1 {
		t.Fatalf("期望恰好 1 个 <SystemMonitor>，实得 %d 个", n)
	}
	if !containsLog(logs, "新增对象 <SystemMonitor") {
		t.Fatalf("验证 diff 日志缺少新增行: %v", logs)
	}

	// 二次 apply：幂等，字节不变。
	runEngine(t, eng, feat, true)
	if again, _ := os.ReadFile(masterPath); string(again) != string(after) {
		t.Fatalf("二次 apply 应幂等，实得:\n%s", again)
	}
}

// TestAddNodePositionFirstInFragment 覆盖 position: first 作用于多根片段文件：插入片段文件
// 第一个顶层元素之前(装配后仍是 <Control> 的首个子节点)，并沿用片段的 CRLF。
func TestAddNodePositionFirstInFragment(t *testing.T) {
	configDir := multiRootFragmentFixture(t)
	fragPath := filepath.Join(configDir, "Control", "Control_Platform")
	before, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}

	feat := loadFeatureYAML(t, `id: first-position-fragment
version: 1
steps:
  - name: 片段首个顶层元素之前加节点
    file: Control/Control_Platform
    add-node:
      - tag: SysMon
        class: SystemMonitor
        attrs: { alias: "" }
        position: first
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
	want := "<SysMon class=\"SystemMonitor\" alias=\"\"/>\r\n" + string(before)
	if string(after) != want {
		t.Fatalf("片段首位置插入结果不符:\n--- got ---\n%q\n--- want ---\n%q", after, want)
	}

	runEngine(t, eng, feat, true)
	if again, _ := os.ReadFile(fragPath); string(again) != string(after) {
		t.Fatalf("二次 apply 应幂等，实得:\n%s", again)
	}
}
