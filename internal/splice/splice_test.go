package splice

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"addex/internal/xmldoc"
)

// TestApplyPreservesInsertionIndent 覆盖不同缩进、换行及空父节点推断。
func TestApplyPreservesInsertionIndent(t *testing.T) {
	for _, unit := range []string{"  ", "    ", "\t"} {
		for _, nl := range []string{"\n", "\r\n"} {
			for _, empty := range []bool{false, true} {
				t.Run(fmt.Sprintf("%q/%q/empty=%v", unit, nl, empty), func(t *testing.T) {
					old := unit + unit + "<Old/>" + nl
					if empty {
						old = ""
					}
					src := []byte("<R>" + nl + unit + "<P>" + nl + old + unit + "</P>" + nl + "</R>" + nl)
					doc, err := xmldoc.Parse(src)
					if err != nil {
						t.Fatal(err)
					}
					p := xmldoc.FindChild(doc.Roots[0], "P")
					child := xmldoc.NewElement("New")
					xmldoc.AppendChild(p, child)
					out := Apply(src, []xmldoc.Edit{{Kind: xmldoc.Insert, Parent: p, Child: child}})
					want := strings.Replace(string(src), unit+"</P>", unit+unit+"<New/>"+nl+unit+"</P>", 1)
					if string(out) != want {
						t.Fatalf("output mismatch:\n got %q\nwant %q", out, want)
					}
				})
			}
		}
	}
}

// TestApplyExpandSelfClosingParent 验证只移除 /> 中的斜杠，属性和外部字节不重渲染。
func TestApplyExpandSelfClosingParent(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		for _, kind := range []xmldoc.EditKind{xmldoc.Insert, xmldoc.InsertRaw} {
			for _, inline := range []bool{false, true} {
				name := "LF"
				if nl == "\r\n" {
					name = "CRLF"
				}
				if kind == xmldoc.InsertRaw {
					name += "/raw"
				} else {
					name += "/node"
				}
				if inline {
					name += "/inline"
				}
				t.Run(name, func(t *testing.T) {
					prefix := "<?xml version='1.0'?>" + nl + "<R>" + nl + "\t "
					suffix := " <!--keep-->" + nl + "  &Keep;" + nl + "</R>"
					indent := "\t "
					if inline {
						prefix = "<!--keep-->" + nl + "<R><left/>"
						suffix = "<right/></R>"
						indent = "    "
					}
					open := "<P z = 'a&amp;b'\t a=\"two\"  />"
					src := []byte(prefix + open + suffix)
					doc, err := xmldoc.Parse(src)
					if err != nil {
						t.Fatal(err)
					}
					parent := xmldoc.FindChild(doc.Roots[0], "P")
					if parent.CloseStart != -1 || parent.End != parent.OpenEnd || string(src[parent.End-2:parent.End]) != "/>" {
						t.Fatalf("unexpected self-closing offsets: %+v", parent)
					}
					child := xmldoc.NewElement("C")
					xmldoc.AppendChild(parent, child)
					edits := []xmldoc.Edit{{Kind: kind, Parent: parent, Child: child, Text: "        <C/>\r\n"}}
					out := Apply(src, edits)
					childText := "        <C/>"
					if !inline && kind == xmldoc.Insert {
						childText = "\t \t <C/>"
					}
					want := prefix + strings.TrimSuffix(open, "/>") + ">" + nl + childText + nl + indent + "</P>" + suffix
					if string(out) != want {
						t.Fatalf("output mismatch:\n got %q\nwant %q", out, want)
					}
					if _, err := xmldoc.Parse(out); err != nil {
						t.Fatalf("invalid XML: %v\n%s", err, out)
					}
					if !bytes.Equal(Apply(src, nil), src) {
						t.Fatal("no edits changed source")
					}
				})
			}
		}
	}
}

// 同一父的混合插入只展开一次；synthetic 子树在祖先渲染中输出一次。
func TestApplySelfClosingMixedInsertionsAndSyntheticSubtree(t *testing.T) {
	src := []byte("<R>\r\n  <P/>\r\n  <Q/>\r\n</R>\r\n")
	doc, err := xmldoc.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	p := xmldoc.FindChild(doc.Roots[0], "P")
	q := xmldoc.FindChild(doc.Roots[0], "Q")
	branch := xmldoc.NewElement("Branch")
	xmldoc.AppendChild(p, branch)
	leaf := xmldoc.NewElement("Leaf")
	leaf.Text = "a & b"
	xmldoc.AppendChild(branch, leaf)
	last := xmldoc.NewElement("Last")
	xmldoc.AppendChild(p, last)
	edits := []xmldoc.Edit{
		{Kind: xmldoc.Insert, Parent: p, Child: branch},
		{Kind: xmldoc.Insert, Parent: branch, Child: leaf},
		{Kind: xmldoc.InsertRaw, Parent: p, Text: "        <Raw>A &amp;&amp; B</Raw>\n        <!--keep-->\n"},
		{Kind: xmldoc.Insert, Parent: p, Child: last},
		{Kind: xmldoc.InsertRaw, Parent: q, Text: "        &Entity;"},
	}
	out := Apply(src, edits)
	want := "<R>\r\n  <P>\r\n    <Branch>\r\n      <Leaf>a &amp; b</Leaf>\r\n    </Branch>\r\n        <Raw>A &amp;&amp; B</Raw>\r\n        <!--keep-->\r\n    <Last/>\r\n  </P>\r\n  <Q>\r\n        &Entity;\r\n  </Q>\r\n</R>\r\n"
	if string(out) != want {
		t.Fatalf("output mismatch:\n got %q\nwant %q", out, want)
	}
	if _, err := xmldoc.Parse(out); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, out)
	}
}

func TestApplySelfClosingRootWithAttributeReplacement(t *testing.T) {
	// 开标签跨行且包含实体和单引号；属性补丁与 /> 展开不重叠。
	src := []byte("<!--outside-->\n<Root\n a = 'old'\n z=\"&Keep;\" />\n<!--after-->")
	doc, err := xmldoc.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	root := doc.Roots[0]
	attr := root.Attrs[0]
	edits := []xmldoc.Edit{
		{Kind: xmldoc.InsertRaw, Parent: root, Text: "    <C/>\r\n    <E/>"},
		{Kind: xmldoc.Replace, Start: attr.ValueStart, End: attr.ValueEnd, Text: "new"},
		{Kind: xmldoc.InsertRaw, Parent: root, Text: "    <D/>"},
	}
	out := Apply(src, edits)
	want := "<!--outside-->\n<Root\n a = 'new'\n z=\"&Keep;\" >\n    <C/>\n    <E/>\n    <D/>\n</Root>\n<!--after-->"
	if string(out) != want {
		t.Fatalf("output mismatch:\n got %q\nwant %q", out, want)
	}
	parsed, err := xmldoc.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Roots[0].SelfClose || len(parsed.Roots[0].Children) != 3 {
		t.Fatalf("root was not expanded correctly: %+v", parsed.Roots[0])
	}
}

// TestApplyInsertAndDeleteSameOffset 回归：新节点插到"即将被删除的兄弟"之前时，插入点与删除
// 区间的起点会重合。必须先删后插——否则先插入的文本会被随后按原偏移执行的删除一并吃掉，
// 产物出现半截标签。此前 sort.Slice 对同起点补丁顺序不确定，属于时隐时现的写坏。
func TestApplyInsertAndDeleteSameOffset(t *testing.T) {
	src := []byte("<R>\n  <a type=\"m\">1</a>\n  <b type=\"m\">2</b>\n</R>\n")
	doc, err := xmldoc.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	root := doc.Roots[0]
	b := xmldoc.FindChild(root, "b")
	if b == nil {
		t.Fatal("夹具缺 <b>")
	}

	edits := []xmldoc.Edit{
		// 插到 <b> 之前（不会进入内存树，只产出字节插入）
		{Kind: xmldoc.InsertRaw, Parent: root, Before: b, Text: `  <new type="m">x</new>`},
		// 删掉 <b> 所在整行（与本行行首同偏移）
		{Kind: xmldoc.Delete, Targets: []*xmldoc.Node{b}},
	}

	out := Apply(src, edits)
	if _, err := xmldoc.Parse(out); err != nil {
		t.Fatalf("产物无法解析(标签被写坏): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "<new") {
		t.Fatalf("插入的节点被删除吃掉:\n%s", out)
	}
	if strings.Contains(string(out), "<b ") {
		t.Fatalf("被删除的节点仍在:\n%s", out)
	}
}
