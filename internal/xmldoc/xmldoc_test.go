package xmldoc

import (
	"os"
	"strings"
	"testing"
)

// countEntities 递归统计树里名为 name 的实体引用节点数。
func countEntities(nodes []*Node, name string) int {
	n := 0
	for _, nd := range nodes {
		if nd.IsEntity && nd.EntName == name {
			n++
		}
		n += countEntities(nd.Children, name)
	}
	return n
}

// find 深度优先找到第一个标签为 tag 的元素。
func find(nodes []*Node, tag string) *Node {
	for _, nd := range nodes {
		if nd.Tag == tag {
			return nd
		}
		if g := find(nd.Children, tag); g != nil {
			return g
		}
	}
	return nil
}

func mustParse(t *testing.T, path string) *Document {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	return doc
}

func TestParseControlCh1(t *testing.T) {
	doc := mustParse(t, "../../config/Control/Control_Ch1")

	if len(doc.Roots) != 1 {
		t.Fatalf("顶层元素数 = %d, 期望 1", len(doc.Roots))
	}
	root := doc.Roots[0]

	if root.Tag != "Ch1" {
		t.Errorf("根标签 = %q, 期望 Ch1", root.Tag)
	}
	if root.Class() != "ITO" {
		t.Errorf("根 class = %q, 期望 ITO", root.Class())
	}
	if got := root.Attr("alias"); got != "/Control/Ch1Exports/Ch1" {
		t.Errorf("根 alias = %q", got)
	}

	// 字节区间必须恰好覆盖 <Ch1 ...>...</Ch1>。
	raw := doc.Raw(root)
	if !strings.HasPrefix(raw, "<Ch1 ") {
		t.Errorf("根原文未以 <Ch1 开头: %.20q", raw)
	}
	if !strings.HasSuffix(raw, "</Ch1>") {
		t.Errorf("根原文未以 </Ch1> 结尾: ...%q", raw[max(0, len(raw)-10):])
	}
	if root.CloseStart <= root.Start {
		t.Errorf("CloseStart(%d) 应在 Start(%d) 之后", root.CloseStart, root.Start)
	}
	if !strings.HasPrefix(string(doc.Src[root.CloseStart:]), "</Ch1>") {
		t.Errorf("CloseStart 未指向 </Ch1>")
	}

	// 实体容忍：36 处 &Simulated_Ch1; 应全部作为实体节点保留(与源文件 grep 一致)。
	if got := countEntities(doc.Roots, "Simulated_Ch1"); got != 36 {
		t.Errorf("Simulated_Ch1 实体节点数 = %d, 期望 36", got)
	}

	// 首个子节点即实体引用(源文件第 2 行 &Simulated_Ch1;)。
	if len(root.Children) == 0 || !root.Children[0].IsEntity || root.Children[0].EntName != "Simulated_Ch1" {
		t.Errorf("根首个子节点应为实体 Simulated_Ch1")
	}

	// 自闭合识别：<enableITO type="method"/>。
	e := find(doc.Roots, "enableITO")
	if e == nil {
		t.Fatal("未找到 enableITO")
	}
	if !e.SelfClose {
		t.Errorf("enableITO 应为自闭合")
	}
	if e.CloseStart != -1 {
		t.Errorf("自闭合节点 CloseStart 应为 -1, got %d", e.CloseStart)
	}
	if r := doc.Raw(e); r != `<enableITO type="method"/>` {
		t.Errorf("enableITO 原文 = %q", r)
	}

	// 带文本的方法节点：字节区间覆盖首尾标签与文本。
	x := find(doc.Roots, "exportState")
	if x == nil {
		t.Fatal("未找到 exportState")
	}
	if r := doc.Raw(x); r != `<exportState type="method">/IO/Ch1Exports/State</exportState>` {
		t.Errorf("exportState 原文 = %q", r)
	}
}

// IO_Motor 是"多顶层元素"片段(实体体，非独立文档)：应解析出 Ch1 与 Ch4 两个根。
func TestParseMultiRootFragment(t *testing.T) {
	doc := mustParse(t, "../../config/IOBridge/IO_Motor")
	if len(doc.Roots) != 2 {
		t.Fatalf("IO_Motor 顶层元素数 = %d, 期望 2", len(doc.Roots))
	}
	if doc.Roots[0].Tag != "Ch1" || doc.Roots[1].Tag != "Ch4" {
		t.Errorf("顶层标签 = [%s %s], 期望 [Ch1 Ch4]", doc.Roots[0].Tag, doc.Roots[1].Tag)
	}
}

// 注释里含类标签文本不应干扰解析(如 <!--<exportMatIncrement ...>-->)。
func TestCommentTolerance(t *testing.T) {
	src := []byte(`<A><!-- <fake attr="x"/> --><real type="method">v</real></A>`)
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	root := doc.Roots[0]
	if len(root.Children) != 1 || root.Children[0].Tag != "real" {
		t.Fatalf("注释未被跳过, children=%d", len(root.Children))
	}
}
