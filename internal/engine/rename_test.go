package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// injectBeforeClose 把 snippet 插到 fragment 的根闭合标签之前(供夹具注入探针节点)。
func injectBeforeClose(t *testing.T, path, closeTag, snippet string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(src), closeTag, snippet+closeTag, 1)
	if out == string(src) {
		t.Fatalf("夹具注入失败：%s 里未找到 %s", path, closeTag)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRenameNodePrimitive 端到端覆盖 rename-node：与需求里的例子逐字对照——
//
//	<Edge class="FuncEdge" type="instance">                      <Edge class="FuncEdge" type="instance">
//	    <setValve type="method">111</setValve>          →            <setGasInValve type="method">111</setGasInValve>
//	</Edge>                                                       </Edge>
//
// 即 outer 节点不动(标签与属性原样保留)，只把子方法改名；同时校验同一步里 rename 后的
// 新名字可被后续动作选中、二次 apply 幂等。
func TestRenameNodePrimitive(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))
	target := filepath.Join(work, "config", "Control", "Control_ChC")
	injectBeforeClose(t, target, "</ChC>",
		"    <Edge class=\"FuncEdge\" type=\"instance\">\n"+
			"        <setValve type=\"method\">111</setValve>\n"+
			"    </Edge>\n"+
			"    <Edge2>\n"+
			"        <setValve2 type=\"method\">1</setValve2>\n"+
			"    </Edge2>\n")

	featPath := filepath.Join(work, "rename.yaml")
	writeFile(t, featPath, `id: rename-demo
version: 1
steps:
  - name: setValve 方法改名
    anchor: Edge
    rename-node:
      - { tag: setValve, to: setGasInValve }

  - name: 先改名再按新名字改文本
    anchor: Edge2
    rename-node:
      - { tag: setValve2, to: setGasInValve2 }
    set-text:
      - { tag: setGasInValve2, value: "222" }
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
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)

	want := "<Edge class=\"FuncEdge\" type=\"instance\">\n" +
		"        <setGasInValve type=\"method\">111</setGasInValve>\n" +
		"    </Edge>"
	if !strings.Contains(out, want) {
		t.Fatalf("rename-node 产物与目标不符:\n%s", excerpt(out, "Edge"))
	}
	if strings.Contains(out, "<setValve ") || strings.Contains(out, "</setValve>") {
		t.Fatalf("旧方法名未清干净:\n%s", excerpt(out, "setValve"))
	}
	// 同一步里 rename 先执行 → 后续 set-text 能按新名字命中。
	if !strings.Contains(out, `<setGasInValve2 type="method">222</setGasInValve2>`) {
		t.Fatalf("rename 后 set-text 未按新名字命中:\n%s", excerpt(out, "setValve2"))
	}
	if !strings.Contains(strings.Join(logs, "\n"), "+ 重命名 <setValve> → <setGasInValve>") {
		t.Fatalf("日志未报告改名:\n%s", strings.Join(logs, "\n"))
	}

	// 二次 apply：逐字节幂等 + 报告 no-op。
	before := out
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var again []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { again = append(again, s) }); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(target); string(after) != before {
		t.Fatalf("二次 apply 不幂等:\n%s", excerpt(string(after), "Edge"))
	}
	if logged := strings.Join(again, "\n"); !strings.Contains(logged, "- 未找到 <setValve>，无需改名") {
		t.Fatalf("二次 apply 应报告 no-op(旧标签已不存在):\n%s", logged)
	}
}

// TestRenameNodeAnchorSelf 校验 anchor 自身改名：`anchor: Edge` + 空选择器 → 标签改名。
func TestRenameNodeAnchorSelf(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))
	target := filepath.Join(work, "config", "Control", "Control_ChC")
	injectBeforeClose(t, target, "</ChC>", "    <Edge>\n        <seed type=\"method\">1</seed>\n    </Edge>\n")

	featPath := filepath.Join(work, "rename.yaml")
	writeFile(t, featPath, `id: rename-self
version: 1
steps:
  - name: Edge 直接改名
    anchor: Edge
    rename-node:
      - { to: FuncEdge }
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
	raw, _ := os.ReadFile(target)
	out := string(raw)
	if !strings.Contains(out, "<FuncEdge>") || !strings.Contains(out, "</FuncEdge>") {
		t.Fatalf("anchor 未改名:\n%s", excerpt(out, "FuncEdge"))
	}
	if strings.Contains(out, "<Edge>") || strings.Contains(out, "</Edge>") {
		t.Fatalf("旧标签名未清干净:\n%s", excerpt(out, "Edge"))
	}
}
