package setupcheck

import (
	"strings"
	"testing"

	"addex/internal/xmldoc"
)

func check(t *testing.T, xml string) []Issue {
	t.Helper()
	doc, err := xmldoc.Parse([]byte(xml))
	if err != nil {
		t.Fatalf("解析夹具: %v", err)
	}
	return CheckDoc("Setup/X.xml", doc)
}

func kinds(issues []Issue) map[string]int {
	m := map[string]int{}
	for _, i := range issues {
		m[string(i.Severity)+":"+i.Kind]++
	}
	return m
}

func TestCheckDocGood(t *testing.T) {
	xml := `<X>
  <Param name="A" type="D" />
  <Param name="B" type="D" />
  <Option index="1">
    <Value paramName="A">1</Value>
    <Value paramName="B">2</Value>
  </Option>
</X>`
	if issues := check(t, xml); len(issues) != 0 {
		t.Fatalf("一致配置不应有问题: %v", issues)
	}
}

// TestCheckDocTypo 复现 example-16196 里 GasFlowCompens 的笔误：
// <Param name="AlONGasFlowPieceCompens"> vs <Value paramName="AlOGasFlowPieceCompens">。
func TestCheckDocTypo(t *testing.T) {
	xml := `<X>
  <Param name="AlGasFlowPieceCompens" type="S" />
  <Param name="AlONGasFlowPieceCompens" type="S" />
  <Option index="1">
    <Value paramName="AlGasFlowPieceCompens">0</Value>
    <Value paramName="AlOGasFlowPieceCompens">0</Value>
  </Option>
</X>`
	issues := check(t, xml)
	k := kinds(issues)
	if k["error:orphan-param"] != 1 || k["error:orphan-value"] != 1 {
		t.Fatalf("应各报 1 处 orphan: %v", issues)
	}
	if !HasError(issues) {
		t.Fatalf("笔误必须判为 error")
	}
	found := false
	for _, i := range issues {
		if i.Kind == "orphan-param" {
			found = i.Detail == "Param name=AlONGasFlowPieceCompens 没有对应的 Value"
		}
	}
	if !found {
		t.Fatalf("orphan-param 描述未指名道姓: %v", issues)
	}
}

func TestCheckDocCountAndDuplicate(t *testing.T) {
	xml := `<X>
  <Param name="A" type="D" />
  <Param name="A" type="D" />
  <Option index="1">
    <Value paramName="A">1</Value>
  </Option>
</X>`
	k := kinds(check(t, xml))
	if k["error:count"] != 1 {
		t.Fatalf("应报数量不一致: %v", k)
	}
	if k["error:duplicate-param"] != 1 {
		t.Fatalf("应报 Param 重复: %v", k)
	}
	if !HasError(check(t, xml)) {
		t.Fatalf("数量/重复必须判为 error")
	}
}

// TestCheckDocOrderIsError 设备按数组下标读取 Param/Value，因此"集合一致、仅顺序不同"
// 也必须判为 error（而不是提示）。
func TestCheckDocOrderIsError(t *testing.T) {
	xml := `<X>
  <Param name="A" type="D" />
  <Param name="B" type="D" />
  <Option index="1">
    <Value paramName="B">2</Value>
    <Value paramName="A">1</Value>
  </Option>
</X>`
	issues := check(t, xml)
	if len(issues) != 2 {
		t.Fatalf("两项都错位应报 2 处: %v", issues)
	}
	for _, is := range issues {
		if is.Kind != "index" || is.Severity != Error {
			t.Fatalf("顺序不同必须判为 error(index): %v", issues)
		}
		if !strings.Contains(is.Detail, "不同名") {
			t.Fatalf("描述未按下标指出: %v", issues)
		}
	}
	if !HasError(issues) {
		t.Fatalf("顺序不同必须判为 error")
	}
	// 顺序一致则通过。
	ok := `<X>
  <Param name="A" type="D" />
  <Param name="B" type="D" />
  <Option index="1">
    <Value paramName="A">1</Value>
    <Value paramName="B">2</Value>
  </Option>
</X>`
	if got := check(t, ok); len(got) != 0 {
		t.Fatalf("顺序一致不应有问题: %v", got)
	}
}

// TestCheckDocAttrNameCaseTypo 覆盖"属性名大小写写错"：<Value paramname="A"> 在 XML 里是
// 另一个属性，严格匹配会漏掉整个取值，只剩数量/下标这类间接症状。要求直接点名属性名笔误。
func TestCheckDocAttrNameCaseTypo(t *testing.T) {
	xml := `<X>
  <Param name="A" type="D" />
  <Param name="B" type="D" />
  <Option index="1">
    <Value paramname="A">1</Value>
    <Value paramName="B">2</Value>
  </Option>
</X>`
	issues := check(t, xml)
	if len(issues) != 1 {
		t.Fatalf("只应报属性名笔误一处(配对不报警): %v", issues)
	}
	is := issues[0]
	if is.Kind != "attr-name" || is.Severity != Error {
		t.Fatalf("属性名笔误必须判为 error(attr-name): %v", is)
	}
	for _, want := range []string{"paramname", "paramName"} {
		if !strings.Contains(is.Detail, want) {
			t.Fatalf("描述未点名 %s: %v", want, is)
		}
	}
	if !HasError(issues) {
		t.Fatalf("属性名笔误必须判为 error")
	}
}

// TestCheckDocAttrNameVariants 覆盖 Param 侧 name 的大小写笔误，以及两种属性名同时出现的写法。
func TestCheckDocAttrNameVariants(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		kind string
	}{
		{
			name: "Param Name 大写",
			xml: `<X>
  <Param Name="A" type="D" />
  <Option index="1"><Value paramName="A">1</Value></Option>
</X>`,
			kind: "attr-name",
		},
		{
			name: "Value ParamName 首字母大写",
			xml: `<X>
  <Param name="A" type="D" />
  <Option index="1"><Value ParamName="A">1</Value></Option>
</X>`,
			kind: "attr-name",
		},
		{
			name: "Value 缺少 paramName",
			xml: `<X>
  <Param name="A" type="D" />
  <Option index="1"><Value>A</Value></Option>
</X>`,
			kind: "attr-missing",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := check(t, c.xml)
			if len(issues) == 0 {
				t.Fatalf("应检出属性名缺陷")
			}
			if issues[0].Kind != c.kind || issues[0].Severity != Error {
				t.Fatalf("首条应为 error:%s，实际 %v", c.kind, issues)
			}
			if !HasError(issues) {
				t.Fatalf("属性名缺陷必须判为 error")
			}
		})
	}
}

// TestCheckDocAttrNameOk 确认属性名写法正确时不受影响（精确匹配优先于大小写兜底）。
func TestCheckDocAttrNameOk(t *testing.T) {
	xml := `<X>
  <Param name="A" type="D" />
  <Option index="1"><Value paramName="A">1</Value></Option>
</X>`
	if issues := check(t, xml); len(issues) != 0 {
		t.Fatalf("属性名正确不应有问题: %v", issues)
	}
}

func TestCheckDirMissingSetup(t *testing.T) {
	issues, err := CheckDir(t.TempDir())
	if err != nil || len(issues) != 0 {
		t.Fatalf("无 Setup 目录应通过: %v %v", issues, err)
	}
}
