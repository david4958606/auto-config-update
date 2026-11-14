package config

import (
	"testing"

	"addex/internal/xmldoc"
)

func TestResolveRequirePathCandidates(t *testing.T) {
	parse := func(src string) *xmldoc.Node {
		t.Helper()
		doc, err := xmldoc.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Roots[0]
	}
	first := parse(`<Ch1><H class="Heater"><Leaf/></H></Ch1>`)
	first.Children[0].Removed = true
	idx := Indexes{"Control": {
		{Path: "removed", Root: first},
		{Path: "incomplete", Root: parse(`<Ch1><H class="Heater"/></Ch1>`)},
		{Path: "complete", Root: parse(`<Ch1><H class="Heater"><Group/><Group><Leaf/></Group></H></Ch1>`)},
		{Path: "master", Master: true, Root: parse(`<Control><Ch1><H class="Heater"><Group><Leaf/></Group></H></Ch1></Control>`)},
	}}
	file, node, logical := ResolveRequirePath(idx, "/Control/Ch1/{Heater}/Group/Leaf")
	if file != "complete" || node == nil || logical != "/Control/Ch1/H/Group/Leaf" {
		t.Fatalf("必须尝试完整候选并优先片段：%q %v %q", file, node, logical)
	}
	file, _, _ = ResolveRequirePath(idx, "/Control/Ch1/{Heater}/Leaf")
	if file != "" {
		t.Fatalf("不应命中已移除的子树：%q", file)
	}
	file, _, _ = ResolveRequirePath(idx, "/Control/{Heater}")
	if file != "" {
		t.Fatalf("首段不应深入其他腔室查找：%q", file)
	}
}
