package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixture 在测试目录里落一份文件(自动建目录)。
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildFixture 构造最小装配：Control 片段(Control_Ch1) + IO 片段(IOBridge/IO_Ch1)，
// 两份 master 正文里各有一个不属于任何片段的节点(CleanRuleRtManager / SystemMonitor)。
func buildFixture(t *testing.T) (configDir, ctrlMaster, ioMaster string) {
	t.Helper()
	configDir = t.TempDir()
	ctrlMaster = filepath.Join(configDir, "Control", "Control_config.xml")
	ioMaster = filepath.Join(configDir, "IO_config.xml")
	writeFixture(t, ctrlMaster, `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE Control [
<!ENTITY Ctrl_Ch1 SYSTEM "./Control_Ch1">
]>
<Control>
    &Ctrl_Ch1;

    <CleanRuleRtManager class="CleanRuleRtManager" type="instance" alias="">
        <setSetup type="method">CleanRuleRtInfo</setSetup>
    </CleanRuleRtManager>
</Control>
`)
	writeFixture(t, filepath.Join(configDir, "Control", "Control_Ch1"),
		"<Ch1 class=\"ITO\" alias=\"/Control/Ch1Exports/Ch1\">\n    <setDbTable type=\"method\">mysql</setDbTable>\n</Ch1>\n")
	writeFixture(t, ioMaster, `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE IO [
<!ENTITY IO_Ch1 SYSTEM "./IOBridge/IO_Ch1">
]>
<IO>
    &IO_Ch1;

    <SystemMonitor class="SystemMonitor" type="instance"/>
</IO>
`)
	writeFixture(t, filepath.Join(configDir, "IOBridge", "IO_Ch1"),
		"<Ch1>\n    <IG class=\"PhyGauge\"/>\n</Ch1>\n")
	return configDir, ctrlMaster, ioMaster
}

// TestResolveLogicalFallsBackToMasterBody 校验片段之外的 master 正文节点也能按逻辑路径解析，
// 且片段优先(既有路径的命中来源不变)。
func TestResolveLogicalFallsBackToMasterBody(t *testing.T) {
	configDir, ctrlMaster, ioMaster := buildFixture(t)
	idx, err := BuildIndexes(ioMaster, ctrlMaster)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		logical  string
		wantPath string
		wantTag  string
	}{
		// 片段节点：命中片段文件。
		{"/Control/Ch1/setDbTable", filepath.Join(configDir, "Control", "Control_Ch1"), "setDbTable"},
		{"/Control/Ch1", filepath.Join(configDir, "Control", "Control_Ch1"), "Ch1"},
		// master 正文节点：片段里找不到，回退到 master 正文根下逐层解析。
		{"/Control/CleanRuleRtManager", ctrlMaster, "CleanRuleRtManager"},
		{"/Control/CleanRuleRtManager/setSetup", ctrlMaster, "setSetup"},
		{"/IO/SystemMonitor", ioMaster, "SystemMonitor"},
	}
	for _, c := range cases {
		path, node := ResolveLogical(idx, c.logical)
		if node == nil {
			t.Fatalf("%s 未解析到节点", c.logical)
		}
		if path != c.wantPath {
			t.Errorf("%s 命中文件 = %q, 期望 %q", c.logical, path, c.wantPath)
		}
		if node.Tag != c.wantTag {
			t.Errorf("%s 命中节点 = %q, 期望 %q", c.logical, node.Tag, c.wantTag)
		}
	}

	// 两边都没有的路径仍应解析失败；片段命中不到时不能把腔室标签当 master 正文节点。
	for _, logical := range []string{"/Control/CleanRuleRtManager/nope", "/Control/Ch1/Nope", "/Control/Nope"} {
		if path, node := ResolveLogical(idx, logical); node != nil {
			t.Errorf("%s 不应命中，却得到 %q (path %q)", logical, node.Tag, path)
		}
	}
}

// TestMasterSectionReported 校验每个域的索引末尾带一段 Master 正文根。
func TestMasterSectionReported(t *testing.T) {
	_, ctrlMaster, ioMaster := buildFixture(t)
	idx, err := BuildIndexes(ioMaster, ctrlMaster)
	if err != nil {
		t.Fatal(err)
	}
	for domain, wantPath := range map[string]string{"Control": ctrlMaster, "IO": ioMaster} {
		sec, ok := MasterSection(idx, domain)
		if !ok {
			t.Fatalf("%s 域缺少 master 正文段", domain)
		}
		if sec.Path != wantPath || !sec.Master {
			t.Fatalf("%s master 正文段 = %+v", domain, sec)
		}
		if sec.Root.Tag != domain {
			t.Fatalf("%s master 正文根 = %q", domain, sec.Root.Tag)
		}
	}
	if _, ok := MasterSection(idx, "IOBridge"); ok {
		t.Fatal("未加载的 IOBridge 域不应有 master 正文段")
	}
}
