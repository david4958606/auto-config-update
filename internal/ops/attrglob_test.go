package ops

import (
	"strings"
	"testing"
)

const attrGlobFixture = `<IO>
  <CG_LA>
    <PressureAI dataType="D" alias="/IO/LAExports/CG_PressureAI">
      <Accuracy>0.0001</Accuracy>
      &SimulatedFlag_Platform;
    </PressureAI>
  </CG_LA>
  <IG_ChA>
    <PressureAI dataType="D" alias="/IO/ChAExports/IG_PressureAI">
      <Accuracy>0.01</Accuracy>
    </PressureAI>
  </IG_ChA>
  <Deep>
    <Wrap>
      <P2 dataType="I" alias="/IO/ChXExports/CG_PressureAI">
        <Accuracy>0.001</Accuracy>
      </P2>
    </Wrap>
  </Deep>
  <NoAlias>
    <Accuracy>0.5</Accuracy>
  </NoAlias>
</IO>
`

// TestSelAttrGlob 覆盖 attr-glob：按属性值 glob 命中任意深度、任意标签；缺属性不命中；
// 与 attr 全等条件同时生效；set-text 二次执行幂等。
func TestSelAttrGlob(t *testing.T) {
	doc, root, _ := parse(t, attrGlobFixture)
	sel := Sel{AttrGlob: map[string]string{"alias": "*CG_PressureAI*"}}
	r := SetText(root, sel, "Accuracy", "", false, "1e-10")
	if !r.Changed {
		t.Fatalf("期望有改动: %s", r.Message)
	}
	out := applyAll(doc, r)
	if got := strings.Count(out, "<Accuracy>1e-10</Accuracy>"); got != 2 {
		t.Fatalf("应只命中 2 个 CG_PressureAI 点位，实际 %d:\n%s", got, out)
	}
	for _, keep := range []string{"<Accuracy>0.01</Accuracy>", "<Accuracy>0.5</Accuracy>", "&SimulatedFlag_Platform;"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("不应改动 %q:\n%s", keep, out)
		}
	}
	if want := strings.Replace(strings.Replace(attrGlobFixture, "0.0001", "1e-10", 1), "<Accuracy>0.001<", "<Accuracy>1e-10<", 1); out != want {
		t.Fatalf("除目标文本外不应有其它字节改动:\n%s", out)
	}
	_, root2, _ := parse(t, out)
	if again := SetText(root2, sel, "Accuracy", "", false, "1e-10"); again.Changed {
		t.Fatalf("二次 set-text 不应有改动: %s", again.Message)
	}

	// 与 attr 组合：两者都须满足。
	_, root3, _ := parse(t, attrGlobFixture)
	both := Sel{Attrs: map[string]string{"dataType": "I"}, AttrGlob: map[string]string{"alias": "*CG_PressureAI"}}
	if hits := SelectNodes(root3, both, "Accuracy"); len(hits) != 1 || hits[0].InnerText != "0.001" {
		t.Fatalf("attr + attr-glob 组合应只命中 P2，实际 %d 个", len(hits))
	}
	// 缺属性不命中：即便 glob 为 "*"。
	any := Sel{Tag: "NoAlias", AttrGlob: map[string]string{"alias": "*"}}
	if hits := SelectNodes(root3, any, ""); len(hits) != 0 {
		t.Fatalf("没有 alias 属性的节点不应被 attr-glob 命中")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"*CG_PressureAI*", "/IO/LAExports/CG_PressureAI", true},
		{"*CG_PressureAI*", "/IO/LAExports/IG_PressureAI", false},
		{"*CG_PressureAI", "/IO/X/CG_PressureAI2", false},
		{"/IO/*Exports/CG_*", "/IO/ChCExports/CG_PressureAI", true},
		{"/IO/Ch?Exports/*", "/IO/ChCExports/a", true},
		{"/IO/Ch?Exports/*", "/IO/LAExports/a", false},
		{"*", "", true},
		{"", "", true},
		{"", "x", false},
		{`a\*b`, "a*b", true},
		{`a\*b`, "axb", false},
		{"[x]", "[x]", true},
		{"**a**", "bab", true},
	}
	for _, c := range cases {
		if got := GlobMatch(c.pat, c.s); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
}
