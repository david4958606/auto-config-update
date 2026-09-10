package feature

import "testing"

// TestFormatBracesEquivalence 锁定两种占位符写法在**取值字段**里同解：
// `{X}` 与 `${X}` 都替换为 tags[X]。区别只在 anchor 段（见 engine/anchor：`${X}` 按标签名、
// `{X}` 按 class），在 attr/value/xml/text/find 等取值里没有区别。
func TestFormatBracesEquivalence(t *testing.T) {
	tags := map[string]string{"X": "Ch1", "Y": "Heater"}
	cases := []struct{ in, want string }{
		{"{X}", "Ch1"},
		{"${X}", "Ch1"},
		{"a-{X}-b", "a-Ch1-b"},
		{"a-${X}-b", "a-Ch1-b"},
		{"/IO/{X}/IG", "/IO/Ch1/IG"},
		{"/IO/${X}/IG", "/IO/Ch1/IG"},
		{"{X}{Y}", "Ch1Heater"},
		{"${X}${Y}", "Ch1Heater"},
		{"/Control/{X}Exports/{Y}", "/Control/Ch1Exports/Heater"},
		// 未绑定的占位符原样保留（不会被误删），便于发现问题。
		{"{Z}-${Z}", "{Z}-${Z}"},
		// 无占位符则原样返回。
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := Format(c.in, tags); got != c.want {
			t.Errorf("Format(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 空 tags 不应 panic，且不改动内容。
	if got := Format("a-{X}", nil); got != "a-{X}" {
		t.Errorf("空 tags: Format = %q", got)
	}
}
