package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// exampleLoadRack 复刻 example-wrong_anchor/config/IOBridge/IO_LoadRack：
// <LoadRack> 下既有空的直接子级 <Plc>，也有 <Dnstatus> 包裹的同名 <Plc>。
const exampleLoadRack = `<LoadRack>
    <Plc>
    </Plc>

    <Dnstatus>
        <Plc datatype="I" accessMode="RW">
            <Bd>0</Bd>
            <Ch>1999</Ch>
        </Plc>
    </Dnstatus>
</LoadRack>`

// TestFileStepTagFallbackDirectChild 是 example-wrong_anchor 的端到端回归：
// 文件级步骤 anchor `LoadRack/Plc` 只应落在直接子级 <Plc> 上，
// 不得因为 tag 回退深入子孙而连带写进 <Dnstatus>/<Plc>；且二次 apply 幂等。
func TestFileStepTagFallbackDirectChild(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")
	writeFile(t, filepath.Join(work, "config", "IOBridge", "IO_LoadRack"), exampleLoadRack)

	featYAML := `id: wrong-anchor-regression
version: 1
steps:
  - name: 添加 IO
    file: IOBridge/IO_LoadRack
    anchor: LoadRack/Plc
    add-io:
      - name: DR3DI
        attrs: { dataType: "I", accessMode: "R" }
        Bd: 0
        Ch: 1036
`
	featPath := filepath.Join(work, "probe.yaml")
	if err := os.WriteFile(featPath, []byte(featYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(work, "config", "IOBridge", "IO_LoadRack")
	apply := func() {
		t.Helper()
		eng, err := New(filepath.Join(work, "config"))
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, out string) {
		t.Helper()
		if n := strings.Count(out, "<DR3DI"); n != 1 {
			t.Errorf("期望只新增 1 个 <DR3DI>，实得 %d 个:\n%s", n, out)
		}
		lo := strings.Index(out, "<Plc>")
		hi := strings.Index(out, "</Plc>")
		if lo < 0 || hi < lo {
			t.Fatalf("找不到直接子级 <Plc> 段:\n%s", out)
		}
		if !strings.Contains(out[lo:hi], "<DR3DI") {
			t.Errorf("直接子级 <Plc> 内未写入点位:\n%s", out)
		}
		dlo := strings.Index(out, "<Dnstatus>")
		dhi := strings.Index(out, "</Dnstatus>")
		if dlo < 0 || dhi < dlo {
			t.Fatalf("找不到 <Dnstatus> 段:\n%s", out)
		}
		if strings.Contains(out[dlo:dhi], "<DR3DI") {
			t.Errorf("<Dnstatus>/<Plc> 不应被写入:\n%s", out)
		}
	}

	apply()
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	check(t, string(raw))

	// 二次 apply：幂等(不得再插一份)。
	apply()
	raw, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	check(t, string(raw))
}
