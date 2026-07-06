package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// add-data 的 DescriptorList / Unit 支持：Unit:NULL → <Unit></Unit>，Unit:值 → <Unit>值</Unit>，
// 缺省 Unit → 不出现；DescriptorList → <DescriptorList>OFF:0,ON:1</DescriptorList>。
// 顺序对齐 add-io：Accuracy 之后先 DescriptorList、再 Unit。
func TestAddDataUnitDescriptorList(t *testing.T) {
	cases := []struct {
		name     string
		unitYAML string
		wantUnit string // 期望出现在写盘 XML 中的 Unit 片段；"" 表示不应出现 <Unit
	}{
		{"unit-null", "        Unit: NULL", "<Unit></Unit>"},
		{"unit-empty", "        Unit: \"\"", "<Unit></Unit>"},
		{"unit-value", "        Unit: mTorr", "<Unit>mTorr</Unit>"},
		{"unit-absent", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work := t.TempDir()
			copyTree(t, "../../config", filepath.Join(work, "config"))

			featYAML := `id: adddata-unit-probe
version: 1
steps:
  - name: probe
    anchor: /Control/{Degas}/{PhyHeater}
    where:
      tag-glob: Heater
    add-data:
      - name: ProbeVp
        attrs: { dataType: "D", accessMode: "R" }
        Bd: NULL
        Ch: NULL
        Accuracy: 0.001
        DescriptorList:
          - { name: OFF, value: 0 }
          - { name: ON, value: 1 }
` + func() string {
				if c.unitYAML == "" {
					return ""
				}
				return c.unitYAML + "\n"
			}()

			featPath := filepath.Join(work, "probe.yaml")
			if err := os.WriteFile(featPath, []byte(featYAML), 0o644); err != nil {
				t.Fatal(err)
			}
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

			// temp-diff 家族 anchor 命中 ChC/ChD。检查 Control_ChC 写盘结果。
			out, err := os.ReadFile(filepath.Join(work, "config", "Control", "Control_ChC"))
			if err != nil {
				t.Fatal(err)
			}
			xml := string(out)

			if !strings.Contains(xml, `<DescriptorList>OFF:0,ON:1</DescriptorList>`) {
				t.Errorf("缺少 DescriptorList，写盘内容:\n%s", excerpt(xml, "ProbeVp"))
			}
			if c.wantUnit == "" {
				if strings.Contains(xml, "<Unit") {
					t.Errorf("Unit 缺省时不应出现 <Unit>，却出现了:\n%s", excerpt(xml, "ProbeVp"))
				}
			} else if !strings.Contains(xml, c.wantUnit) {
				t.Errorf("期望 Unit 片段 %q 未出现，写盘内容:\n%s", c.wantUnit, excerpt(xml, "ProbeVp"))
			}
		})
	}
}

// add-data 的 include-entity：按 glob 从顶层声明解析真名，于数据点位内部末尾追加 &真名;
// (与 add-io / add-node 一致)。此处 SimulatedFlag*{Degas} → &SimulatedFlag_ChC;，在 ProbeVp 内部末尾。
func TestAddDataIncludeEntity(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))

	featYAML := `id: adddata-entity-probe
version: 1
steps:
  - name: probe
    anchor: /Control/{Degas}/{PhyHeater}
    where:
      tag-glob: Heater
    add-data:
      - name: ProbeVp
        attrs: { dataType: "D", accessMode: "R" }
        include-entity: "SimulatedFlag*{Degas}"
        Accuracy: 0.001
`
	featPath := filepath.Join(work, "probe.yaml")
	if err := os.WriteFile(featPath, []byte(featYAML), 0o644); err != nil {
		t.Fatal(err)
	}
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

	out, err := os.ReadFile(filepath.Join(work, "config", "Control", "Control_ChC"))
	if err != nil {
		t.Fatal(err)
	}
	seg := probeSegment(string(out), "ProbeVp")
	// 实体须在点位内部末尾(</ProbeVp> 之前)。
	if !strings.Contains(seg, "&SimulatedFlag_ChC;") {
		t.Errorf("数据点位内未追加解析出的实体 &SimulatedFlag_ChC;，点位段:\n%s", seg)
	}
}

// probeSegment 返回从 <marker 到 </marker> 的整段(含实体行)。
func probeSegment(s, marker string) string {
	lo := strings.Index(s, "<"+marker)
	hi := strings.Index(s, "</"+marker+">")
	if lo < 0 || hi < 0 {
		return s
	}
	return s[lo : hi+len("</"+marker+">")]
}

// excerpt 截取包含 marker 的一段用于报错定位。
func excerpt(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return s
	}
	lo := i - 80
	if lo < 0 {
		lo = 0
	}
	hi := i + 240
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}
