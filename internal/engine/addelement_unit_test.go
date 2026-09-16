package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// add-element 的 include-entity：按 glob 从顶层声明解析真名，于元素内部末尾追加 &真名;(同 add-node)。
// 同一 anchor 下并存多个同 tag 元素时，实体必须落在 attrs/text 匹配的那一个上
// (不能用 FindChild 按 tag 取首个)，且二次执行幂等(实体不重复)。
func TestAddElementIncludeEntity(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))

	featYAML := `id: addelement-entity-probe
version: 1
steps:
  - name: probe
    anchor: /Control/{Degas}/{PhyHeater}
    where:
      tag-glob: Heater
    add-element:
      - tag: Spare
        attrs: { kind: plain }
      - tag: Spare
        attrs: { kind: probe }
        include-entity: "SimulatedFlag*{Degas}"
`
	featPath := filepath.Join(work, "probe.yaml")
	if err := os.WriteFile(featPath, []byte(featYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
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
	apply()

	out, err := os.ReadFile(filepath.Join(work, "config", "Control", "Control_ChC"))
	if err != nil {
		t.Fatal(err)
	}
	xml := string(out)

	// 无 include-entity 的那个同 tag 元素保持自闭合，实体不能误挂到它上面。
	if !strings.Contains(xml, `<Spare kind="plain"/>`) {
		t.Errorf("未命中 include-entity 的同 tag 元素应保持自闭合，写盘内容:\n%s", excerpt(xml, "Spare"))
	}
	// 带 include-entity 的那个元素：实体须在其内部末尾(</Spare> 之前)。
	probe := elementSegment(xml, `<Spare kind="probe"`)
	if probe == "" {
		t.Fatalf("未找到 <Spare kind=\"probe\"> 元素，写盘内容:\n%s", excerpt(xml, "Spare"))
	}
	if !strings.Contains(probe, "&SimulatedFlag_ChC;") {
		t.Errorf("元素内未追加解析出的实体 &SimulatedFlag_ChC;，元素段:\n%s", probe)
	}

	// 二次 apply：add-element 与实体追加都应幂等，实体只出现一次。
	apply()
	out2, err := os.ReadFile(filepath.Join(work, "config", "Control", "Control_ChC"))
	if err != nil {
		t.Fatal(err)
	}
	seg2 := elementSegment(string(out2), `<Spare kind="probe"`)
	if n := strings.Count(seg2, "&SimulatedFlag_ChC;"); n != 1 {
		t.Errorf("二次 apply 后实体引用应恰好一次，实际 %d 次，元素段:\n%s", n, seg2)
	}
}

// elementSegment 返回从 open(含)到其后首个 </tag> 的整段；open 未出现时返回 ""。
func elementSegment(s, open string) string {
	lo := strings.Index(s, open)
	if lo < 0 {
		return ""
	}
	tag := open[1:]
	if i := strings.IndexByte(tag, ' '); i >= 0 {
		tag = tag[:i]
	}
	close := "</" + tag + ">"
	hi := strings.Index(s[lo:], close)
	if hi < 0 {
		return ""
	}
	return s[lo : lo+hi+len(close)]
}
