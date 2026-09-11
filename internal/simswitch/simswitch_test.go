package simswitch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write 在 dir 下写一个文件，返回完整路径。
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSwitchToTrue 校验 false→true、true 保持不动，且重复运行为 no-op(幂等)。
func TestSwitchToTrue(t *testing.T) {
	dir := t.TempDir()
	on := write(t, dir, "SimulatedFlag_Ch1", "<setSimulated type=\"method\">true</setSimulated>\n")
	off := write(t, dir, "SimulatedFlag_Ch2", "<setSimulated type=\"method\">false</setSimulated>\n")
	// 名字不含 Simulated 的文件必须原样不动。
	other := write(t, dir, "IO_config.xml", "<X><setSimulated type=\"method\">false</setSimulated></X>")

	rep, err := Switch(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("应只处理 2 个 *Simulated* 文件，得到 %d：%+v", len(rep.Results), rep.Results)
	}
	if rep.ChangedFiles() != 1 || rep.UnchangedFiles() != 1 || rep.WarnedFiles() != 0 {
		t.Fatalf("汇总不符：改写 %d，已是目标 %d，告警 %d",
			rep.ChangedFiles(), rep.UnchangedFiles(), rep.WarnedFiles())
	}
	if got := read(t, off); got != "<setSimulated type=\"method\">true</setSimulated>\n" {
		t.Errorf("Ch2 未切到 true：%q", got)
	}
	if got := read(t, on); got != "<setSimulated type=\"method\">true</setSimulated>\n" {
		t.Errorf("Ch1 本应逐字节不动：%q", got)
	}
	if got := read(t, other); !strings.Contains(got, ">false<") {
		t.Errorf("非 *Simulated* 文件不应被改：%q", got)
	}

	// 幂等：再跑一遍没有任何改写。
	rep2, err := Switch(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.ChangedFiles() != 0 {
		t.Fatalf("二次运行应无改写，实际 %d", rep2.ChangedFiles())
	}
}

// TestSwitchToFalse 校验 true→false。
func TestSwitchToFalse(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "SimulatedFlag_System", "<setSimulated type=\"method\">true</setSimulated>\n")
	rep, err := Switch(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ChangedFiles() != 1 {
		t.Fatalf("应改写 1 个文件，实际 %d", rep.ChangedFiles())
	}
	if got := read(t, p); got != "<setSimulated type=\"method\">false</setSimulated>\n" {
		t.Errorf("System 未切到 false：%q", got)
	}
}

// TestSurgicalReplace 校验只动文本、属性/缩进/换行逐字保留。
func TestSurgicalReplace(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "Simulated_Ch1", "  <setSimulated  type=\"method\" > false </setSimulated>\n\n")
	if _, err := Switch(dir, true); err != nil {
		t.Fatal(err)
	}
	want := "  <setSimulated  type=\"method\" > true </setSimulated>\n\n"
	if got := read(t, p); got != want {
		t.Errorf("外科式替换不符\n得到 %q\n期望 %q", got, want)
	}
}

// TestSwitchMultipleElements 校验一个文件里的多处 setSimulated 全部切换。
func TestSwitchMultipleElements(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "SimulatedFlag_Multi",
		"<A><setSimulated type=\"method\">false</setSimulated></A>\n"+
			"<B><setSimulated type=\"method\">true</setSimulated></B>\n")
	rep, err := Switch(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Results[0].Edits != 1 || rep.Results[0].Matched != 2 {
		t.Fatalf("多处结果不符：%+v", rep.Results[0])
	}
	if got := read(t, p); strings.Count(got, ">true<") != 2 {
		t.Errorf("两处都应变成 true：%q", got)
	}
}

// TestWarnCases 校验"没有 setSimulated"与"文本里没有布尔值"都只告警、不改写。
func TestWarnCases(t *testing.T) {
	dir := t.TempDir()
	none := write(t, dir, "SimulatedFlag_None", "<setSimulated type=\"method\"/>\n")
	weird := write(t, dir, "SimulatedFlag_Weird", "<setSimulated type=\"method\">yes</setSimulated>\n")
	rep, err := Switch(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.WarnedFiles() != 2 {
		t.Fatalf("应告警 2 个，实际 %d：%+v", rep.WarnedFiles(), rep.Results)
	}
	if got := read(t, none); got != "<setSimulated type=\"method\"/>\n" {
		t.Errorf("无元素文件不应被改：%q", got)
	}
	if got := read(t, weird); got != "<setSimulated type=\"method\">yes</setSimulated>\n" {
		t.Errorf("无布尔值文件不应被改：%q", got)
	}
}

// TestSkipDir 校验名字命中 glob 的目录被跳过，不报错。
func TestSkipDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "SimulatedDir"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "SimulatedFlag_Ch1", "<setSimulated type=\"method\">false</setSimulated>")
	rep, err := Switch(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || !rep.Results[0].Changed {
		t.Fatalf("目录应被跳过，仅处理文件：%+v", rep.Results)
	}
}

// TestNoMatch 校验目录下没有任何 *Simulated* 文件时返回空 Report 且不报错。
func TestNoMatch(t *testing.T) {
	rep, err := Switch(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 0 {
		t.Fatalf("不应有结果：%+v", rep.Results)
	}
}
