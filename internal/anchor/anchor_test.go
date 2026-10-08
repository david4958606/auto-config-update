package anchor

import (
	"testing"

	"addex/internal/xmldoc"
)

// loadRackSrc 复刻 example-wrong_anchor 的结构：直接子级 <Plc> 与 <Dnstatus> 下的
// 同名 <Plc> 并存。tag 回退若仍深入子孙，anchor `LoadRack/Plc` 会命中两个。
const loadRackSrc = `<LoadRack>
    <Plc>
    </Plc>

    <Dnstatus>
        <Plc datatype="I" accessMode="RW">
            <Bd>0</Bd>
            <Ch>1999</Ch>
        </Plc>
    </Dnstatus>
</LoadRack>`

func mustParseRoot(t *testing.T, src string) *xmldoc.Node {
	t.Helper()
	doc, err := xmldoc.Parse([]byte(src))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(doc.Roots) != 1 {
		t.Fatalf("期望 1 个顶层元素，实得 %d", len(doc.Roots))
	}
	return doc.Roots[0]
}

// plainSegs 造裸名段(= class 匹配，无果回退 tag)。
func plainSegs(parts ...string) []Seg {
	segs := make([]Seg, 0, len(parts))
	for _, p := range parts {
		segs = append(segs, Seg{Match: p, Bind: p})
	}
	return segs
}

// TestTagFallbackDirectChildOnly 是 example-wrong_anchor 的最小回归：
// `LoadRack/Plc` 只能命中直接子级 <Plc>，不得把 <Dnstatus>/<Plc> 也算进来。
func TestTagFallbackDirectChildOnly(t *testing.T) {
	root := mustParseRoot(t, loadRackSrc)
	ms := Resolve(root, plainSegs("LoadRack", "Plc"), nil)
	if len(ms) != 1 {
		t.Fatalf("期望 1 个命中(直接子级 Plc)，实得 %d 个", len(ms))
	}
	got := ms[0].Node
	if bd := xmldoc.FindChild(got, "Bd"); bd != nil {
		t.Errorf("命中的是被 <Dnstatus> 包着的 <Plc>（含 <Bd>），应为空的那个直接子级")
	}
	if attr := got.Attr("datatype"); attr != "" {
		t.Errorf("命中的 <Plc> 带 datatype=%q，应为不带该属性的直接子级", attr)
	}
}

// TestTagFallbackFirstSegmentMatchesRoot：首段 tag 回退仍允许命中【自身】——
// 文件级步骤的根元素(如 <LoadRack> / <Motor> / <EFEM>)靠这条匹配。
func TestTagFallbackFirstSegmentMatchesRoot(t *testing.T) {
	root := mustParseRoot(t, loadRackSrc)
	ms := Resolve(root, plainSegs("LoadRack"), nil)
	if len(ms) != 1 || ms[0].Node != root {
		t.Fatalf("期望命中根元素自身，实得 %d 个命中", len(ms))
	}
}

// TestTagFallbackDeepPathStillReachable：写全路径时，深层同名节点照旧可达。
func TestTagFallbackDeepPathStillReachable(t *testing.T) {
	root := mustParseRoot(t, loadRackSrc)
	ms := Resolve(root, plainSegs("LoadRack", "Dnstatus", "Plc"), nil)
	if len(ms) != 1 {
		t.Fatalf("期望 1 个命中(Dnstatus 下的 Plc)，实得 %d 个", len(ms))
	}
	if attr := ms[0].Node.Attr("datatype"); attr != "I" {
		t.Errorf("命中节点 datatype=%q，期望 I", attr)
	}
}

// TestTagFallbackFanOutDirectChildren：同一层有多个同名直接子元素时仍全部返回(fan-out)。
func TestTagFallbackFanOutDirectChildren(t *testing.T) {
	root := mustParseRoot(t, `<Rack>
    <Slot><Name>A</Name></Slot>
    <Slot><Name>B</Name></Slot>
</Rack>`)
	ms := Resolve(root, plainSegs("Rack", "Slot"), nil)
	if len(ms) != 2 {
		t.Fatalf("期望 2 个命中(两个直接子级 Slot)，实得 %d 个", len(ms))
	}
}

// TestClassSegmentStillDescendant：class 段语义不变，仍在整棵子树里找——
// class 是逻辑类别，同层实例可散落在不同深度的包装节点内。
func TestClassSegmentStillDescendant(t *testing.T) {
	root := mustParseRoot(t, `<Root>
    <Wrap>
        <Gauge01 class="PhyGauge"/>
    </Wrap>
</Root>`)
	ms := Resolve(root, plainSegs("Root", "PhyGauge"), nil)
	if len(ms) != 1 {
		t.Fatalf("期望 class 段命中深处的 Gauge01，实得 %d 个命中", len(ms))
	}
	if ms[0].Tags["PhyGauge"] != "Gauge01" {
		t.Errorf("绑定 {PhyGauge}=%q，期望 Gauge01", ms[0].Tags["PhyGauge"])
	}
}

// TestByNameSegmentStillDescendant：`${X}` 名称段语义不变(仍是 descendant)，
// 它由调用方解析出确定标签名，用于 class 缺失且需跨层匹配的片段。
func TestByNameSegmentStillDescendant(t *testing.T) {
	root := mustParseRoot(t, `<Root>
    <Ch1>
        <Deep>
            <IG/>
        </Deep>
    </Ch1>
</Root>`)
	segs := []Seg{
		{ByName: true, Match: "Ch1", Bind: "Chamber"},
		{ByName: true, Match: "IG", Bind: "IG"},
	}
	ms := Resolve(root, segs, nil)
	if len(ms) != 1 {
		t.Fatalf("期望 ${X} 名称段仍能命中深层 IG，实得 %d 个命中", len(ms))
	}
}
