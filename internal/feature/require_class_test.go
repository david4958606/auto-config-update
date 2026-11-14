package feature

import (
	"errors"
	"strings"
	"testing"

	"addex/internal/config"
	"addex/internal/xmldoc"
	"gopkg.in/yaml.v3"
)

func TestRequireClassPaths(t *testing.T) {
	parse := func(src string) *xmldoc.Node {
		t.Helper()
		doc, err := xmldoc.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Roots[0]
	}
	idx := config.Indexes{"Control": {
		{Path: "Ch1.xml", Root: parse(`<Ch1 class="PVD"><Wrap><H1 class="FuncHeater"/><H2 class="FuncHeater"><PhyHeater3/><On type="method"/><!-- <Disabled/> --><Nested><Deep/></Nested></H2></Wrap><Heater/><Other><value>0</value></Other></Ch1>`)},
		{Path: "Ch2.xml", Root: parse(`<Ch2 class="PVD"><OnlyHere class="OtherHeater"><PhyHeater3/></OnlyHere></Ch2>`)},
		{Path: "master.xml", Master: true, Root: parse(`<Control><Manager class="ManagerClass"><Group><Unit class="FuncHeater"><PhyHeater3/></Unit></Group></Manager><Ch1 class="PVD"><Fallback class="MasterHeater"><PhyHeater3/></Fallback></Ch1></Control>`)},
	}}
	for _, tc := range []struct {
		name, path, want, file string
		tags                   map[string]string
	}{
		{"未绑定类及包装层", "/Control/{PVD}/{FuncHeater}/PhyHeater3", "/Control/Ch1/Wrap/H2/PhyHeater3", "Ch1.xml", nil},
		{"绑定腔室", "/Control/${PVD}/{FuncHeater}/PhyHeater3", "/Control/Ch1/Wrap/H2/PhyHeater3", "Ch1.xml", map[string]string{"PVD": "Ch1"}},
		{"已绑定花括号仍为变量", "/Control/{PVD}/{FuncHeater}/PhyHeater3", "/Control/Ch1/Wrap/H2/PhyHeater3", "Ch1.xml", map[string]string{"PVD": "Ch1", "FuncHeater": "Wrap/H2"}},
		{"绑定标签优先不再搜类", "/Control/{PVD}/{FuncHeater}/PhyHeater3", "", "", map[string]string{"PVD": "Ch1", "FuncHeater": "Heater"}},
		{"美元未绑定不搜类", "/Control/${PVD}/{FuncHeater}/PhyHeater3", "", "", nil},
		{"美元中间段未绑定", "/Control/Ch1/${FuncHeater}/PhyHeater3", "", "", nil},
		{"纯类不回退标签", "/Control/Ch1/{Heater}", "", "", nil},
		{"裸名不按类", "/Control/Ch1/FuncHeater/PhyHeater3", "", "", nil},
		{"裸名不跨层", "/Control/Ch1/{FuncHeater}/Deep", "", "", nil},
		{"裸名多层", "/Control/Ch1/{FuncHeater}/Nested/Deep", "/Control/Ch1/Wrap/H2/Nested/Deep", "Ch1.xml", nil},
		{"方法", "/Control/Ch1/{FuncHeater}/On", "/Control/Ch1/Wrap/H2/On", "Ch1.xml", nil},
		{"忽略注释", "/Control/Ch1/{FuncHeater}/Disabled", "", "", nil},
		{"不跨腔室", "/Control/Ch1/{OtherHeater}/PhyHeater3", "", "", nil},
		{"正文类", "/Control/{ManagerClass}/{FuncHeater}/PhyHeater3", "/Control/Manager/Group/Unit/PhyHeater3", "master.xml", nil},
		{"正文回退", "/Control/Ch1/{MasterHeater}/PhyHeater3", "/Control/Ch1/Fallback/PhyHeater3", "master.xml", nil},
		{"域缺失", "/IO/Ch1/{FuncHeater}/PhyHeater3", "", "", nil},
	} {
		for _, kind := range []string{"exist", "non-exist"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				var step Step
				if err := yaml.Unmarshal([]byte("require:\n  - "+kind+": { Target: \""+tc.path+"\" }\n"), &step); err != nil {
					t.Fatal(err)
				}
				tags, notes, err := ResolveBindings(step, tc.tags, idx)
				wantSkip := (tc.want == "") == (kind == "exist")
				if wantSkip {
					var skip *Skip
					if !errors.As(err, &skip) {
						t.Fatalf("期望跳过，得到 tags=%v err=%v", tags, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := tc.want
				if kind == "non-exist" {
					want = Format(tc.path, tc.tags)
				}
				if tags["Target"] != want {
					t.Fatalf("绑定=%q，期望=%q", tags["Target"], want)
				}
				if kind == "exist" && (len(notes) != 1 || notes[0].Path != tc.file || notes[0].Logical != want) {
					t.Fatalf("命中记录错误：%v", notes)
				}
				if kind == "non-exist" && len(notes) != 0 {
					t.Fatalf("不存在的路径不应记录命中：%v", notes)
				}
				if _, ok := tc.tags["Target"]; ok {
					t.Fatal("修改了输入绑定")
				}
			})
		}
	}
	// class 命中的真实路径可继续用于同一步后续守卫和动作；类名不额外导出为变量。
	var step Step
	if err := yaml.Unmarshal([]byte(`require:
  - exist: { Heater: "/Control/Ch1/{FuncHeater}" }
  - non-exist: { Missing: "${Heater}/Missing" }
bind:
  - Ref: "${Heater}/On"
`), &step); err != nil {
		t.Fatal(err)
	}
	tags, _, err := ResolveBindings(step, nil, idx)
	if err != nil || tags["Heater"] != "/Control/Ch1/Wrap/H1" || !strings.HasSuffix(tags["Ref"], "/H1/On") {
		t.Fatalf("后续变量引用错误：tags=%v err=%v", tags, err)
	}
	if _, ok := tags["FuncHeater"]; ok {
		t.Fatal("class 查询不应隐式导出同名变量")
	}
}
