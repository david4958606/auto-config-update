package ops

import (
	"strings"
	"testing"

	"addex/internal/splice"
	"addex/internal/xmldoc"
)

// 一个小夹具：包含方法、实体、空标签、被注释掉的块，覆盖新原语的典型场景。
const fixture = `<Ch1>
    <Heater class="PhyHeater">
        <setTemperatureVp type="method">/IO/Ch1/Heater/PV</setTemperatureVp>
        <setIntlkAlarm type="method">Old,ERROR,old alarm.</setIntlkAlarm>
        <Max>600</Max>
    </Heater>
    <addCmdAI type="method" comment="Ch = 1080 Spare">80</addCmdAI>
    <V6DO dataType="I" alias="/IO/BufferExports/Solenoid_V6DO">
        <Ch>2516</Ch>
        &SimulatedFlag_Ch1;
    </V6DO>
    <createCheckerAlarm type="method">RobotExToCh1,ERROR,robot extended.</createCheckerAlarm>
    <addChecker type="method">(A == B),RobotExToCh1</addChecker>
    <!--<Disabled class="X" type="instance"> <a/> </Disabled>-->
</Ch1>
`

func parse(t *testing.T, src string) (*xmldoc.Document, *xmldoc.Node, *xmldoc.Node) {
	t.Helper()
	doc, err := xmldoc.Parse([]byte(src))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	root := doc.Roots[0]
	return doc, root, xmldoc.FindChild(root, "Heater")
}

func str(s string) *string { return &s }

// applyAll 把 Result 里的编辑(单个 Edit 或一组 Edits)应用到源字节。
func applyAll(doc *xmldoc.Document, r Result) string {
	var edits []xmldoc.Edit
	if r.Edit != nil {
		edits = append(edits, *r.Edit)
	}
	edits = append(edits, r.Edits...)
	return string(splice.Apply(doc.Src, edits))
}

func TestSetTextReplacesInnerAndIsIdempotent(t *testing.T) {
	doc, _, heater := parse(t, fixture)
	r := SetText(heater, Sel{Tag: "Max"}, "", "600", true, "3276.7")
	if !r.Changed {
		t.Fatalf("期望有改动，却 no-op: %s", r.Message)
	}
	out := applyAll(doc, r)
	if !strings.Contains(out, "<Max>3276.7</Max>") {
		t.Fatalf("文本未替换:\n%s", out)
	}
	// 二次执行应幂等。
	_, _, heater2 := parse(t, out)
	if again := SetText(heater2, Sel{Tag: "Max"}, "", "", false, "3276.7"); again.Changed {
		t.Fatalf("二次 set-text 不应有改动: %s", again.Message)
	}
	// old 不匹配时不动。
	_, _, heater3 := parse(t, fixture)
	if miss := SetText(heater3, Sel{Tag: "Max"}, "", "999", true, "1"); miss.Changed {
		t.Fatalf("old 不匹配时不应改动")
	}
}

func TestSetAttrReplacesValueAndIsIdempotent(t *testing.T) {
	doc, root, _ := parse(t, fixture)
	_ = root
	sel := Sel{Tag: "addCmdAI"}
	r := SetAttr(root, sel, "", "Ch = 1080 Spare", true, "comment", "Ch = 1080 PcwWtrFlowAI")
	if !r.Changed {
		t.Fatalf("期望有改动: %s", r.Message)
	}
	out := applyAll(doc, r)
	if !strings.Contains(out, `comment="Ch = 1080 PcwWtrFlowAI"`) {
		t.Fatalf("属性未替换:\n%s", out)
	}
	_, root2, _ := parse(t, out)
	if again := SetAttr(root2, sel, "", "Ch = 1080 Spare", true, "comment", "Ch = 1080 PcwWtrFlowAI"); again.Changed {
		t.Fatalf("二次 set-attr 不应有改动")
	}
	// 缺失属性 → 插入到开标签 '>' 之前。
	doc3, root3, heater := parse(t, fixture)
	ins := SetAttr(heater, Sel{Tag: "setIntlkAlarm"}, "", "", false, "simulated", "true")
	if !ins.Changed {
		t.Fatalf("期望新增属性")
	}
	out3 := applyAll(doc3, ins)
	if !strings.Contains(out3, `<setIntlkAlarm type="method" simulated="true">`) {
		t.Fatalf("属性未插入:\n%s", out3)
	}
	_ = root3
}

func TestRemoveNodeWithHasCondition(t *testing.T) {
	doc, root, _ := parse(t, fixture)
	// 只删 Ch=2516 的 V6DO。
	r := RemoveNode(root, Sel{Tag: "V6DO", Has: &Sel{Tag: "Ch", Value: str("2516")}}, "")
	if !r.Changed {
		t.Fatalf("期望删除 V6DO: %s", r.Message)
	}
	out := applyAll(doc, r)
	if strings.Contains(out, "<V6DO") {
		t.Fatalf("V6DO 未删除:\n%s", out)
	}
	if !strings.Contains(out, "<Heater") {
		t.Fatalf("误删了其它节点:\n%s", out)
	}
	// 条件不满足 → no-op。
	doc2, root2, _ := parse(t, fixture)
	if miss := RemoveNode(root2, Sel{Tag: "V6DO", Has: &Sel{Tag: "Ch", Value: str("9999")}}, ""); miss.Changed {
		t.Fatalf("条件不满足时不应删除")
	}
	_ = doc2
}

func TestRemoveNodeAttrPresenceDistinguishesEmptyValue(t *testing.T) {
	// attr: {alias: ""} 只命中"确实写了 alias"的节点；没写 alias 的节点不应命中。
	src := `<Root><A alias="">1</A><B>2</B></Root>`
	_, root, _ := parse(t, src)
	sel := Sel{Tag: "A", Attrs: map[string]string{"alias": ""}}
	if got := SelectNodes(root, sel, ""); len(got) != 1 || got[0].Tag != "A" {
		t.Fatalf("期望命中 1 个 A，实得 %d", len(got))
	}
	sel2 := Sel{Tag: "B", Attrs: map[string]string{"alias": ""}}
	if got := SelectNodes(root, sel2, ""); len(got) != 0 {
		t.Fatalf("B 没有 alias 属性，不应命中，实得 %d", len(got))
	}
}

func TestWrapRangeCommentOutAndIdempotent(t *testing.T) {
	doc, root, _ := parse(t, fixture)
	sels := []Sel{
		{Tag: "createCheckerAlarm", Value: str("RobotExToCh1,ERROR,robot extended.")},
		{Tag: "addChecker", Value: str("(A == B),RobotExToCh1")},
	}
	r := WrapRange(root, sels, "", "<!--", "-->", doc.Src)
	if !r.Changed {
		t.Fatalf("期望注释包裹: %s", r.Message)
	}
	out := applyAll(doc, r)
	if !strings.Contains(out, `<!--<createCheckerAlarm`) || !strings.Contains(out, `</addChecker>-->`) {
		t.Fatalf("注释标记位置不对:\n%s", out)
	}
	// 被注释后解析器跳过该区间 → 再次包裹应 no-op(找不到节点)。
	doc2, root2, _ := parse(t, out)
	if again := WrapRange(root2, sels, "", "<!--", "-->", doc2.Src); again.Changed {
		t.Fatalf("二次 wrap 不应有改动")
	}
}

func TestUncommentAndDrop(t *testing.T) {
	doc, _, _ := parse(t, fixture)
	r := Uncomment(doc.Src, "Disabled", "<!--", "-->", false)
	if !r.Changed {
		t.Fatalf("期望放开注释: %s", r.Message)
	}
	out := applyAll(doc, r)
	if !strings.Contains(out, `<Disabled class="X" type="instance">`) {
		t.Fatalf("注释标记未去掉:\n%s", out)
	}
	if strings.Contains(out, "<!--<Disabled") {
		t.Fatalf("仍残留注释标记:\n%s", out)
	}
	// 放开后再次执行 → no-op。
	if again := Uncomment([]byte(out), "Disabled", "<!--", "-->", false); again.Changed {
		t.Fatalf("二次 uncomment 不应有改动")
	}
	// drop=true 整段删除。
	doc2, _, _ := parse(t, fixture)
	d := Uncomment(doc2.Src, "Disabled", "<!--", "-->", true)
	if !d.Changed {
		t.Fatalf("期望删除注释块")
	}
	out2 := applyAll(doc2, d)
	if strings.Contains(out2, "Disabled") {
		t.Fatalf("注释块未删除:\n%s", out2)
	}
}

func TestAddElementIdempotentAndPosition(t *testing.T) {
	src := "<Root>\n  <Param name=\"A\"/>\n  <Param name=\"B\"/>\n</Root>\n"
	doc, root, _ := parse(t, src)
	_ = root
	_ = root
	before := xmldoc.FindChild(root, "Param") // A
	el := xmldoc.NewElement("Param")
	el.Attrs = []xmldoc.Attr{{Name: "name", Value: "NEW"}}
	r := AddElement(root, "Param", el.Attrs, "", true, false, before)
	if !r.Changed {
		t.Fatalf("期望新增元素: %s", r.Message)
	}
	out := applyAll(doc, r)
	iNew := strings.Index(out, `name="NEW"`)
	iA := strings.Index(out, `name="A"`)
	if iNew < 0 || iNew > iA {
		t.Fatalf("新元素未插到 A 之前:\n%s", out)
	}
	_, root2, _ := parse(t, out)
	if again := AddElement(root2, "Param", el.Attrs, "", true, false, nil); again.Changed {
		t.Fatalf("二次 add-element 不应有改动")
	}
}

func TestAddRawFragmentPreservesEntities(t *testing.T) {
	doc, _, heater := parse(t, fixture)
	fragSrc := `<setDurationTrigger type="method">((A == 0.0)&amp;&amp;(B == Off)),4000</setDurationTrigger>`
	sub, err := xmldoc.Parse([]byte(fragSrc))
	if err != nil {
		t.Fatal(err)
	}
	frag := sub.Roots[0]
	xmldoc.MarkSynthetic(frag)
	r := AddRawFragment(heater, frag, "        "+fragSrc, nil)
	if !r.Changed {
		t.Fatalf("期望新增片段: %s", r.Message)
	}
	out := applyAll(doc, r)
	if !strings.Contains(out, "((A == 0.0)&amp;&amp;(B == Off)),4000") {
		t.Fatalf("实体未被逐字保留:\n%s", out)
	}
	// 二次执行幂等(结构一致)。
	_, _, heater2 := parse(t, out)
	sub2, _ := xmldoc.Parse([]byte(fragSrc))
	frag2 := sub2.Roots[0]
	xmldoc.MarkSynthetic(frag2)
	if again := AddRawFragment(heater2, frag2, fragSrc, nil); again.Changed {
		t.Fatalf("二次 add-xml 不应有改动")
	}
}
