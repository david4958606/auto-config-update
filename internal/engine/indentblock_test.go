package engine

import "testing"

// TestIndentBlockKeepsRelativeIndent 验证 add-xml 片段重排缩进时保留子元素的相对缩进，
// 而不是把所有行压平到同一层。
func TestIndentBlockKeepsRelativeIndent(t *testing.T) {
	raw := "\n  <A class=\"X\">\n      <m type=\"method\">1</m>\n\n      &Ent;\n  </A>\n"
	got := indentBlock(raw, 1)
	want := "    <A class=\"X\">\n        <m type=\"method\">1</m>\n\n        &Ent;\n    </A>"
	if got != want {
		t.Fatalf("indentBlock:\n got=%q\nwant=%q", got, want)
	}
	if got := indentBlock("<m type=\"method\">1</m>", 2); got != "        <m type=\"method\">1</m>" {
		t.Fatalf("单行片段缩进错误: %q", got)
	}
}
