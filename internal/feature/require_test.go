package feature

import (
	"errors"
	"strings"
	"testing"

	"addex/internal/config"
	"addex/internal/xmldoc"
	"gopkg.in/yaml.v3"
)

// require.exist 按逻辑路径检查节点，因此也能检查方法（有值或标志型）。
func TestRequireExistMethod(t *testing.T) {
	doc, err := xmldoc.Parse([]byte(`<Ch1><Gauge><setOnOffVp type="method">/IO/Ch1/IG/OnOffDI</setOnOffVp><enableModeSwitch type="method"/><!-- <disabled type="method"/> --><Wrapper><nested type="method"/></Wrapper></Gauge></Ch1>`))
	if err != nil {
		t.Fatal(err)
	}
	idx := config.Indexes{"Control": {{Path: "Control_Ch1.xml", Root: doc.Roots[0]}}}
	for _, tc := range []struct {
		name     string
		path     string
		wantSkip bool
	}{
		{"有值方法", "/Control/{Chamber}/Gauge/setOnOffVp", false},
		{"标志型方法", "/Control/${Chamber}/Gauge/enableModeSwitch", false},
		{"方法缺失", "/Control/{Chamber}/Gauge/missing", true},
		{"注释方法不算存在", "/Control/{Chamber}/Gauge/disabled", true},
		{"不跨层查找", "/Control/{Chamber}/Gauge/nested", true},
		{"完整嵌套路径", "/Control/{Chamber}/Gauge/Wrapper/nested", false},
	} {
		for _, kind := range []string{"exist", "non-exist"} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				var step Step
				if err := yaml.Unmarshal([]byte("require:\n  - "+kind+": { Method: \""+tc.path+"\" }\n"), &step); err != nil {
					t.Fatal(err)
				}
				input := map[string]string{"Chamber": "Ch1"}
				tags, notes, err := ResolveBindings(step, input, idx)
				logical := Format(tc.path, input)
				wantSkip := tc.wantSkip
				if kind == "non-exist" {
					wantSkip = !wantSkip
				}
				if wantSkip {
					var skip *Skip
					if !errors.As(err, &skip) || !strings.Contains(skip.Reason, logical) {
						t.Fatalf("期望包含路径 %s 的 Skip，得到 %v", logical, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if tags["Method"] != logical {
					t.Fatalf("绑定错误：tags=%v", tags)
				}
				if kind == "exist" && (len(notes) != 1 || notes[0] != (Note{Var: "Method", Logical: logical, Path: "Control_Ch1.xml"})) || kind == "non-exist" && len(notes) != 0 {
					t.Fatalf("绑定或命中记录错误：tags=%v notes=%v", tags, notes)
				}
				if _, ok := input["Method"]; ok {
					t.Fatal("不应修改输入变量")
				}
			})
		}
	}
}
