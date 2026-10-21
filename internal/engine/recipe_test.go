package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"addex/internal/feature"
)

// recipeDocCRLF 是真实 RecipeNamespace.xml 的最小夹具：CRLF 换行、每层 2 空格、自闭合标签
// 斜杠前带空格。Ch1/Ch2 有 <RecipeExecutor>，GaugeOnly 没有(用于告警路径)。
const recipeDocCRLF = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\r\n" +
	"<RecipeNamespace name=\"Local\" readOnlyLvl=\"2\">\r\n" +
	"  <RecipeClass name=\"Ch1\">\r\n" +
	"    <RecipeExecutor type=\"default\">\r\n" +
	"      <Param name=\"Step Name\" dataObject=\"\" type=\"S\" maxLength=\"256\" default=\"\" />\r\n" +
	"      <Param name=\"Time\" dataObject=\"\" type=\"D\" min=\"0\" max=\"3600\" units=\"s\" accuracy=\"0.1\" default=\"0\" />\r\n" +
	"    </RecipeExecutor>\r\n" +
	"  </RecipeClass>\r\n" +
	"  <RecipeClass name=\"Ch2\">\r\n" +
	"    <RecipeExecutor type=\"default\">\r\n" +
	"      <Param name=\"Step Name\" dataObject=\"\" type=\"S\" maxLength=\"100\" default=\"\" />\r\n" +
	"    </RecipeExecutor>\r\n" +
	"  </RecipeClass>\r\n" +
	"  <RecipeClass name=\"GaugeOnly\">\r\n" +
	"    <Gauge type=\"default\" />\r\n" +
	"  </RecipeClass>\r\n" +
	"</RecipeNamespace>\r\n"

// recipeNewParamLine 是测试中反复断言的新 Param 渲染行(6 空格缩进 = RecipeExecutor 4 + 2)。
const recipeNewParamLine = `      <Param name="New Param" dataObject="" type="D" min="0" max="100" units="%" accuracy="0.1" default="50" />`

// recipeConfig 在 t.TempDir() 下构造最小 config/：Engine.New 需要 Control/IO master，
// add-recipe 缺省目标为 config/Recipe/RecipeNamespace.xml。
func recipeConfig(t *testing.T) (work, nsPath string) {
	t.Helper()
	work = t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")
	nsPath = filepath.Join(work, "config", "Recipe", "RecipeNamespace.xml")
	writeFile(t, nsPath, recipeDocCRLF)
	return work, nsPath
}

// recipeDiffLines 提取日志里以 key 开头的语义 diff 片段(plan/apply 对比用)。
func recipeDiffLines(logs []string, key string) []string {
	var out []string
	for _, l := range logs {
		if i := strings.Index(l, key); i >= 0 {
			out = append(out, l[i:])
		}
	}
	return out
}

// recipeClassBlock 截取输出中某个 <RecipeClass name=...> 的整段文本。
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

// TestAddRecipePrimitiveImplicitFile 端到端覆盖 add-recipe 隐式文件：省略 file: 时自动作用于
// config/Recipe/RecipeNamespace.xml；plan 不落盘且语义 diff 与 apply 一致；新 Param 落在
// RecipeExecutor 的 Param 序列末尾、沿用 CRLF 与 2 空格缩进、其余字节逐字不变；二次 apply 幂等。
func TestAddRecipePrimitiveImplicitFile(t *testing.T) {
	work, nsPath := recipeConfig(t)
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-implicit
version: 1
steps:
  - name: 隐式 RecipeNamespace 追加
    add-recipe:
      - recipe-class: Ch1
        param: New Param
        dataObject: ""
        type: D
        min: 0
        max: 100
        units: "%"
        accuracy: 0.1
        default: 50
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}

	// plan：只输出语义 diff，不落盘。
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var planLogs []string
	if err := eng.ApplyFeature(feat, nil, false, func(s string) { planLogs = append(planLogs, s) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(nsPath); string(got) != recipeDocCRLF {
		t.Fatalf("plan 模式不应写盘:\n%q", got)
	}

	// apply：追加到 Ch1 的 RecipeExecutor 末尾。
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var applyLogs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { applyLogs = append(applyLogs, s) }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(nsPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	if !strings.Contains(out, "\r\n"+recipeNewParamLine+"\r\n") {
		t.Fatalf("新行应保留 CRLF 与 6 空格缩进:\n%q", out)
	}
	// 其余字节逐字不变：仅在 Ch1 的 </RecipeExecutor> 前多出一行。
	want := strings.Replace(recipeDocCRLF, "    </RecipeExecutor>", recipeNewParamLine+"\r\n    </RecipeExecutor>", 1)
	if out != want {
		t.Fatalf("插入改动了目标区间之外的字节:\n got %q\nwant %q", out, want)
	}
	// 位置：Ch1 的既有 Param 之后、闭合标签之前。
	iTime := strings.Index(out, `name="Time"`)
	iNew := strings.Index(out, `name="New Param"`)
	iExecEnd := strings.Index(out, "    </RecipeExecutor>")
	if !(0 <= iTime && iTime < iNew && iNew < iExecEnd) {
		t.Fatalf("新 Param 未追加到 RecipeExecutor 的 Param 序列末尾:\n%s", out)
	}

	// plan 与 apply 的语义 diff 文案一致。
	planDiffs := recipeDiffLines(planLogs, "新增 Recipe Param")
	applyDiffs := recipeDiffLines(applyLogs, "新增 Recipe Param")
	if !reflect.DeepEqual(planDiffs, applyDiffs) {
		t.Fatalf("plan 与 apply 语义 diff 不一致:\nplan  %v\napply %v", planDiffs, applyDiffs)
	}
	if len(planDiffs) != 1 || !strings.Contains(planDiffs[0], "新增 Recipe Param New Param（RecipeClass Ch1）") {
		t.Fatalf("语义 diff 文案不符: %v", planDiffs)
	}

	// 二次 apply：逐字节幂等，且报告已存在(no-op)。
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var secondLogs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { secondLogs = append(secondLogs, s) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(nsPath); string(got) != out {
		t.Fatalf("二次 apply 不幂等:\n%s", got)
	}
	if logged := strings.Join(secondLogs, "\n"); !strings.Contains(logged, "- Recipe Param New Param 已存在（RecipeClass Ch1）") {
		t.Fatalf("二次 apply 应报告 no-op，实得:\n%s", logged)
	}
}

// TestAddRecipePrimitiveExplicitFileGlob 端到端覆盖显式 file: + recipe-class glob：
// Ch* 同时给 Ch1/Ch2 追加、GaugeOnly 不受影响；二次 apply 幂等。
func TestAddRecipePrimitiveExplicitFileGlob(t *testing.T) {
	work, nsPath := recipeConfig(t)
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-explicit
version: 1
steps:
  - name: 显式文件 glob 追加
    file: Recipe/RecipeNamespace.xml
    add-recipe:
      - recipe-class: "Ch*"
        param: Extra Param
        dataObject: ""
        type: I
        min: 0
        max: 2
        descriptorList: "Close:0,Open:1"
        units: ""
        default: Close
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(nsPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	wantLine := `      <Param name="Extra Param" dataObject="" type="I" min="0" max="2" descriptorList="Close:0,Open:1" units="" default="Close" />`
	if !strings.Contains(out, "\r\n"+wantLine+"\r\n") {
		t.Fatalf("显式 file 的渲染不符:\n%q", out)
	}
	if got := strings.Count(out, `name="Extra Param"`); got != 2 {
		t.Fatalf("Ch* 应给 Ch1/Ch2 各加 1 条，实得 %d:\n%s", got, out)
	}
	for _, cls := range []string{"Ch1", "Ch2"} {
		if got := strings.Count(recipeClassBlock(t, out, cls), `name="Extra Param"`); got != 1 {
			t.Fatalf("RecipeClass %s 应恰好 1 条，实得 %d:\n%s", cls, got, out)
		}
	}
	if strings.Contains(recipeClassBlock(t, out, "GaugeOnly"), `name="Extra Param"`) {
		t.Fatalf("GaugeOnly 不应被 Ch* 命中:\n%s", out)
	}
	if logged := strings.Join(logs, "\n"); !strings.Contains(logged, "新增 Recipe Param Extra Param（RecipeClass Ch1、Ch2）") {
		t.Fatalf("语义 diff 应列出两个类:\n%s", logged)
	}

	// 二次 apply：幂等。
	before := out
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(nsPath); string(after) != before {
		t.Fatalf("二次 apply 不幂等:\n%s", after)
	}
}

// TestAddRecipeDuplicateParamNotInsertedTwice 覆盖重复 param 名：同一步内声明两次、后续步骤
// 再声明一次，最终只插入一条；二次 apply 逐字节不变。
func TestAddRecipeDuplicateParamNotInsertedTwice(t *testing.T) {
	work, nsPath := recipeConfig(t)
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-dup
version: 1
steps:
  - name: 同一步重复声明
    add-recipe:
      - { recipe-class: Ch1, param: Dup Param, dataObject: "", type: I, min: 0, max: 1, default: 0 }
      - { recipe-class: Ch1, param: Dup Param, dataObject: "", type: I, min: 0, max: 1, default: 0 }
  - name: 下一步重复声明
    add-recipe:
      - { recipe-class: Ch1, param: Dup Param, dataObject: "", type: I, min: 0, max: 1, default: 0 }
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(nsPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	if got := strings.Count(out, `name="Dup Param"`); got != 1 {
		t.Fatalf("重复 param 名只应插入 1 条，实得 %d:\n%s", got, out)
	}
	if logged := strings.Join(logs, "\n"); !strings.Contains(logged, "已存在") {
		t.Fatalf("重复声明应报告已存在(no-op):\n%s", logged)
	}

	// 二次 apply 逐字节幂等。
	before := out
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(nsPath); string(after) != before {
		t.Fatalf("二次 apply 不幂等:\n%s", after)
	}
}

// TestAddRecipeUnmatchedClassWarnsWithoutError 覆盖未匹配 RecipeClass：apply 不返回 error、
// 日志含 !、文件逐字节不变。
func TestAddRecipeUnmatchedClassWarnsWithoutError(t *testing.T) {
	work, nsPath := recipeConfig(t)
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-miss
version: 1
steps:
  - name: 未匹配 RecipeClass
    add-recipe:
      - { recipe-class: NoSuchClass, param: X, dataObject: "", type: I, min: 0, max: 1, default: 0 }
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatalf("未匹配 RecipeClass 不应返回 error: %v", err)
	}
	if got, _ := os.ReadFile(nsPath); string(got) != recipeDocCRLF {
		t.Fatalf("未匹配时文件不应改动:\n%q", got)
	}
	logged := strings.Join(logs, "\n")
	if !strings.Contains(logged, `! 未找到 RecipeClass "NoSuchClass"`) {
		t.Fatalf("日志应含 ! 告警:\n%s", logged)
	}
}

// TestAddRecipeClassWithoutExecutorWarns 覆盖 RecipeClass 存在但缺 <RecipeExecutor>：
// apply 不返回 error、日志含 ! 告警、文件不变。
func TestAddRecipeClassWithoutExecutorWarns(t *testing.T) {
	work, nsPath := recipeConfig(t)
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-no-exec
version: 1
steps:
  - name: 缺 RecipeExecutor
    add-recipe:
      - { recipe-class: GaugeOnly, param: X, dataObject: "", type: I, min: 0, max: 1, default: 0 }
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatalf("缺 RecipeExecutor 不应返回 error: %v", err)
	}
	if got, _ := os.ReadFile(nsPath); string(got) != recipeDocCRLF {
		t.Fatalf("缺 RecipeExecutor 时文件不应改动:\n%q", got)
	}
	logged := strings.Join(logs, "\n")
	if !strings.Contains(logged, "! RecipeClass GaugeOnly 缺少 <RecipeExecutor>") {
		t.Fatalf("日志应含 ! 告警:\n%s", logged)
	}
}

// TestAddRecipeBeforeLocatesAndEscapes 端到端覆盖 before 定位与属性转义：
// 新 Param 落在 Step Name 与 Time 之间；dataObject/units 里的 & < " 转义成实体。
func TestAddRecipeBeforeLocatesAndEscapes(t *testing.T) {
	work, nsPath := recipeConfig(t)
	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-before
version: 1
steps:
  - name: before 定位与转义
    add-recipe:
      - recipe-class: Ch1
        param: Mid Param
        dataObject: "/x&y"
        type: S
        maxLength: 10
        units: 'a&b<c"d'
        before: Time
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(nsPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	wantLine := `      <Param name="Mid Param" dataObject="/x&amp;y" type="S" maxLength="10" units="a&amp;b&lt;c&quot;d" />`
	if !strings.Contains(out, "\r\n"+wantLine+"\r\n") {
		t.Fatalf("before 定位/属性转义渲染不符:\n%q", out)
	}
	iStep := strings.Index(out, `name="Step Name"`)
	iMid := strings.Index(out, `name="Mid Param"`)
	iTime := strings.Index(out, `name="Time"`)
	if !(0 <= iStep && iStep < iMid && iMid < iTime) {
		t.Fatalf("Mid Param 应落在 Step Name 之后、Time 之前:\n%s", out)
	}
}

// recipePerChamberFixture 在 t.TempDir() 下构造"Ch1/Ch2 两个腔室片段 + 带两个
// RecipeClass 的 RecipeNamespace"的最小 config/，并返回一个取值含占位符的 feature。
func recipePerChamberFixture(t *testing.T) (work, nsPath string, feat *feature.Feature) {
	t.Helper()
	work = t.TempDir()
	cfg := filepath.Join(work, "config")
	writeFile(t, filepath.Join(cfg, "Control", "Control_config.xml"),
		"<!DOCTYPE Control [<!ENTITY Control_Ch1 SYSTEM \"Control_Ch1\">"+
			"<!ENTITY Control_Ch2 SYSTEM \"Control_Ch2\">]>"+
			"<Control>\n&Control_Ch1;\n&Control_Ch2;\n</Control>\n")
	writeFile(t, filepath.Join(cfg, "Control", "Control_Ch1"), "<Ch1 class=\"ITO\">\n  <Heater/>\n</Ch1>\n")
	writeFile(t, filepath.Join(cfg, "Control", "Control_Ch2"), "<Ch2 class=\"ITO\">\n  <Heater/>\n</Ch2>\n")
	writeFile(t, filepath.Join(cfg, "IO_config.xml"), "<IO></IO>")
	nsPath = filepath.Join(cfg, "Recipe", "RecipeNamespace.xml")
	writeFile(t, nsPath, recipeDocCRLF)

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: recipe-per-chamber
version: 1
steps:
  - name: 按腔室给 Recipe 加参数
    add-recipe:
      - recipe-class: "${Chamber}"
        param: "P_${Chamber}"
        dataObject: "/SETUP/${Chamber}/P"
        type: D
        default: 0
`)
	var err error
	feat, err = feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	return work, nsPath, feat
}

// TestAddRecipeClassPlaceholderExpandsPerChamber 覆盖 add-recipe 取值含 ${Chamber} 时按腔室展开：
// 逐腔室用 {Chamber} 绑定解析 recipe-class/param/属性值，只改该腔室对应的 RecipeClass；
// `--chamber` 限定时只处理选中腔室；二次 apply 幂等。
func TestAddRecipeClassPlaceholderExpandsPerChamber(t *testing.T) {
	t.Run("all-chambers", func(t *testing.T) {
		work, nsPath, feat := recipePerChamberFixture(t)
		eng, err := New(filepath.Join(work, "config"))
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(nsPath)
		if err != nil {
			t.Fatal(err)
		}
		out := string(raw)
		if got := strings.Count(recipeClassBlock(t, out, "Ch1"), `name="P_Ch1"`); got != 1 {
			t.Fatalf("Ch1 应新增 1 条 P_Ch1，实得 %d:\n%s", got, out)
		}
		if got := strings.Count(recipeClassBlock(t, out, "Ch2"), `name="P_Ch2"`); got != 1 {
			t.Fatalf("Ch2 应新增 1 条 P_Ch2，实得 %d:\n%s", got, out)
		}
		// 占位符必须按腔室各自解析，不能串类。
		if strings.Contains(recipeClassBlock(t, out, "Ch1"), `name="P_Ch2"`) ||
			strings.Contains(recipeClassBlock(t, out, "Ch2"), `name="P_Ch1"`) {
			t.Fatalf("占位符解析串类:\n%s", out)
		}
		if strings.Contains(recipeClassBlock(t, out, "GaugeOnly"), "P_") {
			t.Fatalf("未命中腔室不应被改:\n%s", out)
		}
		wantLine := `      <Param name="P_Ch1" dataObject="/SETUP/Ch1/P" type="D" default="0" />`
		if !strings.Contains(out, "\r\n"+wantLine+"\r\n") {
			t.Fatalf("按腔室解析的渲染不符(CRLF/6 空格/ />):\n%q", out)
		}

		// 二次 apply：逐字节幂等。
		eng, err = New(filepath.Join(work, "config"))
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
			t.Fatal(err)
		}
		if after, _ := os.ReadFile(nsPath); string(after) != out {
			t.Fatalf("二次 apply 不幂等:\n%s", after)
		}
	})

	t.Run("chamber-filter", func(t *testing.T) {
		work, nsPath, feat := recipePerChamberFixture(t)
		eng, err := New(filepath.Join(work, "config"))
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.ApplyFeature(feat, []string{"Ch1"}, true, func(string) {}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(nsPath)
		if err != nil {
			t.Fatal(err)
		}
		out := string(raw)
		if !strings.Contains(recipeClassBlock(t, out, "Ch1"), `name="P_Ch1"`) {
			t.Fatalf("选中 Ch1 应新增 P_Ch1:\n%s", out)
		}
		if strings.Contains(recipeClassBlock(t, out, "Ch2"), "P_") {
			t.Fatalf("未选中 Ch2 不应被改:\n%s", out)
		}
	})
}
