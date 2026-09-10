package splice

import (
	"strings"
	"testing"

	"addex/internal/xmldoc"
)

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
