package ops

import (
	"strings"
	"testing"

	"addex/internal/splice"
	"addex/internal/xmldoc"
)

// recipeFixture 是一份多 RecipeClass 的 RecipeNamespace 夹具(LF、每层 2 空格)：
// Ch1/Ch2/Ch3 各有一个 <RecipeExecutor>(Ch1 带三个 Param)，GaugeOnly 没有 RecipeExecutor，
// LA 另有一个(用于验证 glob 的匹配边界与"仅给缺失类添加")。
const recipeFixture = `<?xml version="1.0" encoding="UTF-8"?>
<RecipeNamespace name="Local" readOnlyLvl="2">
  <RecipeClass name="Ch1">
    <RecipeExecutor type="default">
      <Param name="Step Name" dataObject="" type="S" maxLength="256" default="" />
      <Param name="Time" dataObject="" type="D" min="0" max="3600" units="s" accuracy="0.1" default="0" />
      <Param name="Pressure" dataObject="" type="D" min="0" max="800" units="Torr" accuracy="0.00001" default="0" />
    </RecipeExecutor>
  </RecipeClass>
  <RecipeClass name="Ch2">
    <RecipeExecutor type="default">
      <Param name="Step Name" dataObject="" type="S" maxLength="100" default="" />
    </RecipeExecutor>
  </RecipeClass>
  <RecipeClass name="Ch3">
    <RecipeExecutor type="default">
      <Param name="Step Name" dataObject="" type="S" maxLength="100" default="" />
    </RecipeExecutor>
  </RecipeClass>
  <RecipeClass name="GaugeOnly">
    <Gauge type="default" />
  </RecipeClass>
  <RecipeClass name="LA">
    <RecipeExecutor type="default">
      <Param name="Step Name" dataObject="" type="S" maxLength="100" default="" />
    </RecipeExecutor>
  </RecipeClass>
</RecipeNamespace>
`

// recipeAttrs 返回一条按规范顺序书写的 <Param> 属性表(AddRecipeParam 只按传入顺序渲染，
// 属性排序由 feature.AddRecipeSpec.ParamAttrs 负责，本包单测只验证渲染与插入语义)。
func recipeAttrs(name string) []xmldoc.Attr {
	return []xmldoc.Attr{
		{Name: "name", Value: name},
		{Name: "dataObject", Value: ""},
		{Name: "type", Value: "D"},
		{Name: "min", Value: "0"},
		{Name: "max", Value: "100"},
		{Name: "units", Value: "%"},
		{Name: "accuracy", Value: "0.1"},
		{Name: "default", Value: "50"},
	}
}

// recipeLineOf 返回 out 里首个包含 key 的整行(去掉行尾 CR/LF)，供断言" />"渲染格式。
func recipeLineOf(t *testing.T, out, key string) string {
	t.Helper()
	i := strings.Index(out, key)
	if i < 0 {
		t.Fatalf("输出中找不到 %q:\n%s", key, out)
	}
	start := strings.LastIndex(out[:i], "\n") + 1
	end := len(out)
	if j := strings.Index(out[i:], "\n"); j >= 0 {
		end = i + j
	}
	return strings.TrimRight(out[start:end], "\r")
}

// recipeClassBlock 截取输出中某个 <RecipeClass name=...> 的整段文本(含自身标签，不含结尾换行)。
func recipeClassBlock(t *testing.T, out, class string) string {
	t.Helper()
	start := strings.Index(out, `<RecipeClass name="`+class+`">`)
	if start < 0 {
		t.Fatalf("输出中找不到 RecipeClass %s:\n%s", class, out)
	}
	rel := strings.Index(out[start:], "</RecipeClass>")
	if rel < 0 {
		t.Fatalf("RecipeClass %s 缺少闭合标签:\n%s", class, out[start:])
	}
	return out[start : start+rel]
}

// TestAddRecipeParamAppendsToRecipeExecutor 覆盖按 name 精确命中单类：新 Param 落在该
// <RecipeExecutor> 的 Param 序列末尾、渲染成 `<Param ... />`、其余字节逐字不变、二次调用幂等。
func TestAddRecipeParamAppendsToRecipeExecutor(t *testing.T) {
	doc, root, _ := parse(t, recipeFixture)
	r := AddRecipeParam(root, "Ch1", "New Param", recipeAttrs("New Param"), "", "")
	if !r.Changed {
		t.Fatalf("期望新增 Param: %s", r.Message)
	}
	if r.Edit == nil || len(r.Edits) != 0 {
		t.Fatalf("单类新增应只给出 Edit(edit=%v, edits=%d)", r.Edit, len(r.Edits))
	}
	if !strings.Contains(r.Message, "新增 Recipe Param New Param") || !strings.Contains(r.Message, "Ch1") {
		t.Fatalf("消息口径不符: %s", r.Message)
	}

	out := applyAll(doc, r)
	wantLine := `      <Param name="New Param" dataObject="" type="D" min="0" max="100" units="%" accuracy="0.1" default="50" />`
	if line := recipeLineOf(t, out, `name="New Param"`); line != wantLine {
		t.Fatalf("新 Param 渲染不符:\n got %q\nwant %q", line, wantLine)
	}
	if !strings.HasSuffix(wantLine, " />") {
		t.Fatal("夹具断言前提有误：期望行应以 ' />' 结尾")
	}

	// 位置：Ch1 的既有 Param 之后、RecipeExecutor 闭合之前。
	iTime := strings.Index(out, `name="Time"`)
	iNew := strings.Index(out, `name="New Param"`)
	iExecEnd := strings.Index(out, "    </RecipeExecutor>")
	if !(0 <= iTime && iTime < iNew && iNew < iExecEnd) {
		t.Fatalf("新 Param 未追加到 RecipeExecutor 的 Param 序列末尾:\n%s", out)
	}

	// 其余字节逐字不变：仅多出这一行。
	want := strings.Replace(recipeFixture, "    </RecipeExecutor>", wantLine+"\n    </RecipeExecutor>", 1)
	if out != want {
		t.Fatalf("插入改动了目标区间之外的字节:\n got %q\nwant %q", out, want)
	}

	// 二次调用：同名 Param 已存在 → no-op。
	_, root2, _ := parse(t, out)
	again := AddRecipeParam(root2, "Ch1", "New Param", recipeAttrs("New Param"), "", "")
	if again.Changed || again.Edit != nil || len(again.Edits) != 0 {
		t.Fatalf("二次 add-recipe 不应有改动: %s", again.Message)
	}
	if !strings.Contains(again.Message, "已存在") || !strings.Contains(again.Message, "Ch1") {
		t.Fatalf("二次调用的消息口径不符: %s", again.Message)
	}
}

// TestAddRecipeParamGlobAddsOnlyMissingClasses 覆盖 glob：一次命中多个类时产出多条编辑、
// 只给缺失的类添加，二次调用全部已存在则 no-op。
func TestAddRecipeParamGlobAddsOnlyMissingClasses(t *testing.T) {
	// 全部缺失：Ch* 命中 Ch1/Ch2/Ch3，各一条编辑(GaugeOnly/LA 不匹配)。
	doc, root, _ := parse(t, recipeFixture)
	r := AddRecipeParam(root, "Ch*", "Extra Param", recipeAttrs("Extra Param"), "", "")
	if !r.Changed || r.Edit != nil {
		t.Fatalf("多类命中应返回 Edits 而非 Edit: changed=%v edit=%v", r.Changed, r.Edit)
	}
	if len(r.Edits) != 3 {
		t.Fatalf("Ch* 应命中 3 个类并各产出一条编辑，实得 %d", len(r.Edits))
	}
	if !strings.Contains(r.Message, "Ch1、Ch2、Ch3") {
		t.Fatalf("消息应列出全部命中类: %s", r.Message)
	}
	out := applyAll(doc, r)
	if got := strings.Count(out, `name="Extra Param"`); got != 3 {
		t.Fatalf("应插入 3 条 Param，实得 %d:\n%s", got, out)
	}
	for _, cls := range []string{"Ch1", "Ch2", "Ch3"} {
		if got := strings.Count(recipeClassBlock(t, out, cls), `name="Extra Param"`); got != 1 {
			t.Fatalf("RecipeClass %s 应恰好插入 1 条，实得 %d:\n%s", cls, got, out)
		}
	}
	if strings.Contains(recipeClassBlock(t, out, "GaugeOnly"), `name="Extra Param"`) {
		t.Fatalf("GaugeOnly 不应被 Ch* 命中:\n%s", out)
	}
	if strings.Contains(recipeClassBlock(t, out, "LA"), `name="Extra Param"`) {
		t.Fatalf("LA 不应被 Ch* 命中:\n%s", out)
	}

	// 二次调用：三个类都已有 → no-op。
	_, root2, _ := parse(t, out)
	again := AddRecipeParam(root2, "Ch*", "Extra Param", recipeAttrs("Extra Param"), "", "")
	if again.Changed {
		t.Fatalf("二次 glob add-recipe 不应有改动: %s", again.Message)
	}

	// 部分已存在：Ch1 先有 Shared Param，glob 只给 Ch2/Ch3 加，消息同时报告已存在的类。
	doc3, root3, _ := parse(t, recipeFixture)
	first := applyAll(doc3, AddRecipeParam(root3, "Ch1", "Shared Param", recipeAttrs("Shared Param"), "", ""))
	doc4, root4, _ := parse(t, first)
	r4 := AddRecipeParam(root4, "Ch*", "Shared Param", recipeAttrs("Shared Param"), "", "")
	if !r4.Changed || len(r4.Edits) != 2 {
		t.Fatalf("应只给缺失的 Ch2/Ch3 各一条编辑，实得 changed=%v edits=%d: %s",
			r4.Changed, len(r4.Edits), r4.Message)
	}
	if !strings.Contains(r4.Message, "Ch2、Ch3") || !strings.Contains(r4.Message, "已存在：Ch1") {
		t.Fatalf("消息应区分新增类与已存在类: %s", r4.Message)
	}
	out4 := applyAll(doc4, r4)
	if got := strings.Count(out4, `name="Shared Param"`); got != 3 {
		t.Fatalf("最终应共有 3 条 Shared Param，实得 %d:\n%s", got, out4)
	}
}

// TestAddRecipeParamBeforeAfter 覆盖 before/after 定位：按既有 Param 名插到其前/后；
// before 优先于 after；基准不存在时退化追加末尾并在消息里以 ! 告警；
// 多类 glob 时按类分别告警。
func TestAddRecipeParamBeforeAfter(t *testing.T) {
	attrs := recipeAttrs("New Param")

	// before: Time —— 落在 Step Name 与 Time 之间。
	doc, root, _ := parse(t, recipeFixture)
	out := applyAll(doc, AddRecipeParam(root, "Ch1", "New Param", attrs, "Time", ""))
	iStep := strings.Index(out, `name="Step Name"`)
	iNew := strings.Index(out, `name="New Param"`)
	iTime := strings.Index(out, `name="Time"`)
	if !(0 <= iStep && iStep < iNew && iNew < iTime) {
		t.Fatalf("before Time 的 Param 应落在 Step Name 之后、Time 之前:\n%s", out)
	}

	// after: Step Name —— 同样落在 Step Name 与 Time 之间。
	doc2, root2, _ := parse(t, recipeFixture)
	r2 := AddRecipeParam(root2, "Ch1", "New Param", attrs, "", "Step Name")
	out2 := applyAll(doc2, r2)
	iStep2 := strings.Index(out2, `name="Step Name"`)
	iNew2 := strings.Index(out2, `name="New Param"`)
	iTime2 := strings.Index(out2, `name="Time"`)
	if !(0 <= iStep2 && iStep2 < iNew2 && iNew2 < iTime2) {
		t.Fatalf("after Step Name 的 Param 应落在 Step Name 之后、Time 之前:\n%s", out2)
	}

	// before 优先于 after：同时给 before:Time / after:Pressure 时按 before 定位。
	doc3, root3, _ := parse(t, recipeFixture)
	out3 := applyAll(doc3, AddRecipeParam(root3, "Ch1", "New Param", attrs, "Time", "Pressure"))
	iNew3 := strings.Index(out3, `name="New Param"`)
	if !(0 <= iNew3 && iNew3 < strings.Index(out3, `name="Time"`)) {
		t.Fatalf("before 应优先于 after(新 Param 落在 Time 之前):\n%s", out3)
	}

	// 基准不存在：退化追加到末尾，并以 ! 告警。
	doc4, root4, _ := parse(t, recipeFixture)
	r4 := AddRecipeParam(root4, "Ch1", "New Param", attrs, "NoSuch", "")
	if !r4.Changed {
		t.Fatalf("基准缺失时仍应追加: %s", r4.Message)
	}
	if !strings.Contains(r4.Message, "!") || !strings.Contains(r4.Message, "NoSuch") ||
		!strings.Contains(r4.Message, "未找到定位参数") {
		t.Fatalf("定位基准缺失应告警: %s", r4.Message)
	}
	out4 := applyAll(doc4, r4)
	iPressure := strings.Index(out4, `name="Pressure"`)
	iNew4 := strings.Index(out4, `name="New Param"`)
	iExecEnd4 := strings.Index(out4, "    </RecipeExecutor>")
	if !(0 <= iPressure && iPressure < iNew4 && iNew4 < iExecEnd4) {
		t.Fatalf("基准缺失时 Param 应追加到序列末尾:\n%s", out4)
	}

	// 多类 glob 时按类告警：Ch1 有 Pressure，Ch2/Ch3 没有。
	doc5, root5, _ := parse(t, recipeFixture)
	r5 := AddRecipeParam(root5, "Ch*", "New Param", attrs, "Pressure", "")
	if len(r5.Edits) != 3 {
		t.Fatalf("应命中 3 个类，实得 %d: %s", len(r5.Edits), r5.Message)
	}
	if !strings.Contains(r5.Message, "! RecipeClass Ch2、Ch3 未找到定位参数 Pressure") {
		t.Fatalf("应按类报告定位缺失: %s", r5.Message)
	}
	out5 := applyAll(doc5, r5)
	if iNew5, iPressure5 := strings.Index(out5, `name="New Param"`), strings.Index(out5, `name="Pressure"`); !(iNew5 < iPressure5) {
		t.Fatalf("Ch1 的 before Pressure 应生效(新 Param 在 Pressure 之前):\n%s", out5)
	}
	if got := strings.Count(out5, `name="New Param"`); got != 3 {
		t.Fatalf("应给 3 个类各追加 1 条，实得 %d:\n%s", got, out5)
	}
}

// TestAddRecipeParamWarnsWhenNoClassMatches 覆盖无匹配 RecipeClass：Changed=false、
// 消息以 ! 告警、源字节不变。
func TestAddRecipeParamWarnsWhenNoClassMatches(t *testing.T) {
	doc, root, _ := parse(t, recipeFixture)
	r := AddRecipeParam(root, "No*", "X", recipeAttrs("X"), "", "")
	if r.Changed || r.Edit != nil || len(r.Edits) != 0 {
		t.Fatalf("无匹配类不应有改动: changed=%v edit=%v edits=%d", r.Changed, r.Edit, len(r.Edits))
	}
	if !strings.Contains(r.Message, "!") || !strings.Contains(r.Message, "未找到 RecipeClass") ||
		!strings.Contains(r.Message, `"No*"`) {
		t.Fatalf("无匹配类应告警: %s", r.Message)
	}
	if out := applyAll(doc, r); out != recipeFixture {
		t.Fatalf("无匹配类不应改动任何字节:\n%s", out)
	}
}

// TestAddRecipeParamWarnsWhenClassHasNoExecutor 覆盖 RecipeClass 缺 <RecipeExecutor>：
// 单类命中时 Changed=false 且告警；与其它类一起 glob 命中时，只给有 executor 的类添加，
// 并在同一条消息里以 ! 告警缺 executor 的类。
func TestAddRecipeParamWarnsWhenClassHasNoExecutor(t *testing.T) {
	doc, root, _ := parse(t, recipeFixture)
	r := AddRecipeParam(root, "GaugeOnly", "X", recipeAttrs("X"), "", "")
	if r.Changed {
		t.Fatalf("缺 RecipeExecutor 不应有改动: %s", r.Message)
	}
	if !strings.Contains(r.Message, "!") || !strings.Contains(r.Message, "GaugeOnly") ||
		!strings.Contains(r.Message, "缺少 <RecipeExecutor>") {
		t.Fatalf("缺 RecipeExecutor 应告警: %s", r.Message)
	}
	if out := applyAll(doc, r); out != recipeFixture {
		t.Fatalf("缺 RecipeExecutor 不应改动任何字节:\n%s", out)
	}

	// glob 命中含缺 executor 的类：其余类正常新增，消息里同时告警。
	doc2, root2, _ := parse(t, recipeFixture)
	r2 := AddRecipeParam(root2, "*", "Extra Param", recipeAttrs("Extra Param"), "", "")
	if !r2.Changed || len(r2.Edits) != 4 {
		t.Fatalf("应给 Ch1/Ch2/Ch3/LA 各一条编辑(共 4 条)，实得 changed=%v edits=%d: %s",
			r2.Changed, len(r2.Edits), r2.Message)
	}
	if !strings.Contains(r2.Message, "! RecipeClass GaugeOnly 缺少 <RecipeExecutor>") {
		t.Fatalf("消息应告警缺 RecipeExecutor 的类: %s", r2.Message)
	}
	out2 := applyAll(doc2, r2)
	if strings.Contains(recipeClassBlock(t, out2, "GaugeOnly"), `name="Extra Param"`) {
		t.Fatalf("缺 RecipeExecutor 的类不应被插入 Param:\n%s", out2)
	}
	if got := strings.Count(recipeClassBlock(t, out2, "LA"), `name="Extra Param"`); got != 1 {
		t.Fatalf("LA 应恰好插入 1 条，实得 %d:\n%s", got, out2)
	}
}

// TestAddRecipeParamRejectsEmptyClassOrParam 覆盖必填字段缺失：给出 ! 告警、Changed=false。
func TestAddRecipeParamRejectsEmptyClassOrParam(t *testing.T) {
	_, root, _ := parse(t, recipeFixture)
	if r := AddRecipeParam(root, "", "X", nil, "", ""); r.Changed ||
		!strings.Contains(r.Message, "! add-recipe 缺少 recipe-class") {
		t.Fatalf("空 recipe-class 应告警: changed=%v msg=%s", r.Changed, r.Message)
	}
	if r := AddRecipeParam(root, "Ch1", "", nil, "", ""); r.Changed ||
		!strings.Contains(r.Message, "! add-recipe 缺少 param 名") {
		t.Fatalf("空 param 名应告警: changed=%v msg=%s", r.Changed, r.Message)
	}
}

// TestAddRecipeParamRendersAttrsOrderAndEscapes 覆盖属性按传入顺序渲染、空值属性保留、
// 特殊字符(& < ")转义成实体，且自闭合标签斜杠前有空格。
func TestAddRecipeParamRendersAttrsOrderAndEscapes(t *testing.T) {
	doc, root, _ := parse(t, recipeFixture)
	attrs := []xmldoc.Attr{
		{Name: "name", Value: "Special"},
		{Name: "dataObject", Value: ""},
		{Name: "type", Value: "S"},
		{Name: "maxLength", Value: "10"},
		{Name: "descriptorList", Value: ""},
		{Name: "default", Value: `a&b<c"d`},
		{Name: "custom", Value: "x"},
	}
	r := AddRecipeParam(root, "Ch1", "Special", attrs, "", "")
	if !r.Changed {
		t.Fatalf("期望新增: %s", r.Message)
	}
	out := applyAll(doc, r)
	wantLine := `      <Param name="Special" dataObject="" type="S" maxLength="10" descriptorList="" ` +
		`default="a&amp;b&lt;c&quot;d" custom="x" />`
	if line := recipeLineOf(t, out, `name="Special"`); line != wantLine {
		t.Fatalf("属性顺序/空值/转义/空格渲染不符:\n got %q\nwant %q", line, wantLine)
	}
}

// TestAddRecipeParamUsesFirstExecutorOnly 覆盖一个 RecipeClass 下有多个 <RecipeExecutor> 时
// 只取首个(与实现约定一致)。
func TestAddRecipeParamUsesFirstExecutorOnly(t *testing.T) {
	const multi = `<RecipeNamespace name="Local">
  <RecipeClass name="Ch1">
    <RecipeExecutor type="default">
      <Param name="A" dataObject="" type="I" default="0" />
    </RecipeExecutor>
    <RecipeExecutor type="other">
      <Param name="B" dataObject="" type="I" default="0" />
    </RecipeExecutor>
  </RecipeClass>
</RecipeNamespace>
`
	doc, root, _ := parse(t, multi)
	r := AddRecipeParam(root, "Ch1", "New Param", recipeAttrs("New Param"), "", "")
	if !r.Changed {
		t.Fatalf("期望新增: %s", r.Message)
	}
	out := applyAll(doc, r)
	if got := strings.Count(out, `name="New Param"`); got != 1 {
		t.Fatalf("应只插入首个 RecipeExecutor，实得 %d 条:\n%s", got, out)
	}
	iDefault := strings.Index(out, `<RecipeExecutor type="default">`)
	iNew := strings.Index(out, `name="New Param"`)
	iOther := strings.Index(out, `<RecipeExecutor type="other">`)
	if !(0 <= iDefault && iDefault < iNew && iNew < iOther) {
		t.Fatalf("新 Param 应落在首个 RecipeExecutor 内:\n%s", out)
	}
}

// recipeCRLF 是 CRLF + 每层 2 空格的夹具(与真实 RecipeNamespace.xml 一致)。
const recipeCRLF = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\r\n" +
	"<RecipeNamespace name=\"Local\" readOnlyLvl=\"2\">\r\n" +
	"  <RecipeClass name=\"Ch1\">\r\n" +
	"    <RecipeExecutor type=\"default\">\r\n" +
	"      <Param name=\"Step Name\" dataObject=\"\" type=\"S\" maxLength=\"256\" default=\"\" />\r\n" +
	"    </RecipeExecutor>\r\n" +
	"  </RecipeClass>\r\n" +
	"</RecipeNamespace>\r\n"

// TestAddRecipeParamPreservesCRLFAndTwoSpaceIndent 覆盖真实文件格式：新行沿用 CRLF 与
// 每层 2 空格缩进(新 Param 前 6 空格)，其余字节逐字不变。
func TestAddRecipeParamPreservesCRLFAndTwoSpaceIndent(t *testing.T) {
	doc, root, _ := parse(t, recipeCRLF)
	r := AddRecipeParam(root, "Ch1", "Time", recipeAttrs("Time"), "", "")
	if !r.Changed {
		t.Fatalf("期望新增: %s", r.Message)
	}
	out := applyAll(doc, r)
	wantLine := `      <Param name="Time" dataObject="" type="D" min="0" max="100" units="%" accuracy="0.1" default="50" />`
	if !strings.Contains(out, "\r\n"+wantLine+"\r\n") {
		t.Fatalf("新行应保留 CRLF 与 6 空格缩进:\n%q", out)
	}
	want := strings.Replace(recipeCRLF, "    </RecipeExecutor>", wantLine+"\r\n    </RecipeExecutor>", 1)
	if out != want {
		t.Fatalf("CRLF 文件的其它字节发生了变化:\n got %q\nwant %q", out, want)
	}
}

// TestAddRecipeParamBeforeStepNewFallsBackToEnd 覆盖 before 指向同一步内先前新增的合成 Param：
// 合成节点尚无落盘位置，应退化为追加末尾并告警，且内存树顺序与落盘顺序一致(不会把新节点
// 排到磁盘上并不存在的位置)。
func TestAddRecipeParamBeforeStepNewFallsBackToEnd(t *testing.T) {
	doc, root, _ := parse(t, recipeFixture)
	r1 := AddRecipeParam(root, "Ch1", "First New", recipeAttrs("First New"), "", "")
	if !r1.Changed || r1.Edit == nil {
		t.Fatalf("第一条应新增: %s", r1.Message)
	}
	// 同一棵内存树(同一步内)：First New 是刚挂上的合成节点，before 指向它应退化。
	r2 := AddRecipeParam(root, "Ch1", "Second New", recipeAttrs("Second New"), "First New", "")
	if !r2.Changed {
		t.Fatalf("期望新增: %s", r2.Message)
	}
	if !strings.Contains(r2.Message, "!") || !strings.Contains(r2.Message, "为本步新增") ||
		!strings.Contains(r2.Message, "First New") {
		t.Fatalf("合成基准应告警说明退化: %s", r2.Message)
	}
	out := string(splice.Apply(doc.Src, []xmldoc.Edit{*r1.Edit, *r2.Edit}))
	iFirst := strings.Index(out, `name="First New"`)
	iSecond := strings.Index(out, `name="Second New"`)
	iEnd := strings.Index(out, "    </RecipeExecutor>")
	if !(0 <= iFirst && iFirst < iSecond && iSecond < iEnd) {
		t.Fatalf("合成基准应退化为追加末尾(First New 在前、Second New 在后):\n%s", out)
	}
}
