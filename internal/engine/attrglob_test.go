package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// TestSetTextAttrGlobOnFragments 端到端覆盖 attr-glob：file glob 遍历 IO 片段，按 alias
// 通配命中所有 CG_PressureAI 点位改 Accuracy；保留实体引用、其它字节不动、二次 apply 幂等。
func TestSetTextAttrGlobOnFragments(t *testing.T) {
	work := t.TempDir()
	cfg := filepath.Join(work, "config")
	writeFile(t, filepath.Join(cfg, "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(cfg, "IO_config.xml"), `<?xml version="1.0"?>
<!DOCTYPE IO [
<!ENTITY IO_Platform SYSTEM "./IOBridge/IO_Platform">
<!ENTITY IO_Ch9 SYSTEM "./IOBridge/IO_Ch9">
]>
<IO>
    &IO_Platform;
    &IO_Ch9;
</IO>
`)
	platform := `  <CG_LA>
    <PressureAI dataType="D" accessMode="R" alias="/IO/LAExports/CG_PressureAI">
      <Accuracy>0.0001</Accuracy>
      &SimulatedFlag_Platform;
    </PressureAI>
  </CG_LA>
  <IG_ChA>
    <PressureAI dataType="D" accessMode="R" alias="/IO/ChAExports/IG_PressureAI">
      <Accuracy>0.01</Accuracy>
    </PressureAI>
  </IG_ChA>
`
	ch9 := "<CG_Ch9>\r\n  <PressureAI alias=\"/IO/Ch9Exports/CG_PressureAI\">\r\n    <Accuracy>0.001</Accuracy>\r\n  </PressureAI>\r\n</CG_Ch9>\r\n"
	writeFile(t, filepath.Join(cfg, "IOBridge", "IO_Platform"), platform)
	writeFile(t, filepath.Join(cfg, "IOBridge", "IO_Ch9"), ch9)

	featPath := filepath.Join(work, "f.yaml")
	writeFile(t, featPath, `id: cg
version: 1
steps:
  - name: CG Accuracy
    file: IOBridge/IO_*
    set-text:
      - { attr-glob: { alias: "*CG_PressureAI*" }, child: Accuracy, value: "1e-10" }
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	run := func() string {
		eng, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var logs []string
		if err := eng.ApplyFeature(feat, nil, true, func(s string) { logs = append(logs, s) }); err != nil {
			t.Fatal(err)
		}
		return strings.Join(logs, "\n")
	}
	run()

	read := func(n string) string {
		b, err := os.ReadFile(filepath.Join(cfg, "IOBridge", n))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got, want := read("IO_Platform"), strings.Replace(platform, "0.0001", "1e-10", 1); got != want {
		t.Fatalf("IO_Platform 产物不符:\n%s", got)
	}
	if got, want := read("IO_Ch9"), strings.Replace(ch9, "0.001", "1e-10", 1); got != want {
		t.Fatalf("IO_Ch9 产物不符(应保留 CRLF):\n%q", got)
	}

	logs := run()
	if strings.Contains(logs, "+ ") {
		t.Fatalf("二次 apply 不应有改动:\n%s", logs)
	}
}
