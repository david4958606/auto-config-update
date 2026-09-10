// Package xmlcmp —— 配置片段的"语义比对"。
//
// 升级校验用：把 apply 后的产物与目标配置比较，忽略**语义无关**的书写差异：
//   - 空白/缩进/换行/空行；
//   - 属性书写顺序、自闭合 <x/> 与成对空标签 <x></x>；
//   - 被 <!-- --> 注释掉的内容与被 <![CDATA[ ]]> 包住的内容(解析器本就跳过 → 视为不存在)。
//
// 仍然严格比较：元素层级与标签、属性集合与取值、叶子文本、实体引用(&Name;)、
// 以及每个父节点下的子节点**顺序**。
//
// 差异分两级：Kind="order" 只表示同一批子节点顺序不同(通常语义无关)；其余均为实质差异。
package xmlcmp

import (
	"fmt"
	"sort"
	"strings"

	"addex/internal/xmldoc"
)

// Diff 是一处差异。
type Diff struct {
	Path   string // 逻辑路径，如 /Ch1Setup/Param[3]
	Kind   string // missing | extra | text | attr | tag | order
	Detail string
}

func (d Diff) String() string {
	return fmt.Sprintf("%s: [%s] %s", d.Path, d.Kind, d.Detail)
}

// Compare 比较 actual(升级产物)与 expected(目标)的语义差异。
func Compare(actual, expected []byte) ([]Diff, error) {
	da, err := xmldoc.Parse(actual)
	if err != nil {
		return nil, fmt.Errorf("解析升级产物: %w", err)
	}
	de, err := xmldoc.Parse(expected)
	if err != nil {
		return nil, fmt.Errorf("解析目标配置: %w", err)
	}
	var diffs []Diff
	compareNodeList("", da.Roots, de.Roots, &diffs)
	return diffs, nil
}

// CompareNodes 供已持有解析树时复用。
func CompareNodes(actual, expected *xmldoc.Document) []Diff {
	var diffs []Diff
	compareNodeList("", actual.Roots, expected.Roots, &diffs)
	return diffs
}

func compareNodeList(path string, a, e []*xmldoc.Node, diffs *[]Diff) {
	// 顶层可能有多个同名节点(如 IO_Motor 的 <Ch1>/<Ch4>)，按序比对。
	na, ne := significant(a), significant(e)
	n := len(na)
	if len(ne) < n {
		n = len(ne)
	}
	for i := 0; i < n; i++ {
		compareNode(childPath(path, na[i], i), na[i], ne[i], diffs)
	}
	for i := n; i < len(na); i++ {
		*diffs = append(*diffs, Diff{childPath(path, na[i], i), "extra", "升级产物多出 <" + na[i].Tag + ">"})
	}
	for i := n; i < len(ne); i++ {
		*diffs = append(*diffs, Diff{childPath(path, ne[i], i), "missing", "升级产物缺少 <" + ne[i].Tag + ">"})
	}
}

func compareNode(path string, a, e *xmldoc.Node, diffs *[]Diff) {
	if a.IsEntity || e.IsEntity {
		if a.IsEntity != e.IsEntity {
			*diffs = append(*diffs, Diff{path, "tag", label(a) + " ≠ " + label(e)})
			return
		}
		if a.EntName != e.EntName {
			*diffs = append(*diffs, Diff{path, "text", fmt.Sprintf("实体 &%s; ≠ &%s;", a.EntName, e.EntName)})
		}
		return
	}
	if a.Tag != e.Tag {
		*diffs = append(*diffs, Diff{path, "tag", "<" + a.Tag + "> ≠ <" + e.Tag + ">"})
		return
	}
	// 属性：按名比较(忽略书写顺序)。
	for _, kv := range attrDiff(a, e) {
		*diffs = append(*diffs, Diff{path, "attr", kv})
	}
	// 文本：只在"无子节点"的叶子上比较(父节点的 Text 只是缩进空白)。
	if len(significant(a.Children)) == 0 && len(significant(e.Children)) == 0 {
		if strings.TrimSpace(a.InnerText) != strings.TrimSpace(e.InnerText) {
			*diffs = append(*diffs, Diff{path, "text", fmt.Sprintf("%q ≠ %q", a.InnerText, e.InnerText)})
		}
		return
	}
	ca, ce := significant(a.Children), significant(e.Children)
	compareChildren(path, ca, ce, diffs)
}

// compareChildren 比较两个子节点序列：先按序(完全一致则逐节点递归)，否则按
// "身份键(tag + 属性集)"对位后再递归，从而把差异精确到具体子节点。
func compareChildren(path string, ca, ce []*xmldoc.Node, diffs *[]Diff) {
	if sameSequence(ca, ce) {
		for i := range ca {
			compareNode(childPath(path, ca[i], i), ca[i], ce[i], diffs)
		}
		return
	}
	used := make([]bool, len(ce))
	matched := make([]int, len(ca))
	for i := range matched {
		matched[i] = -1
	}
	for i, a := range ca {
		for j, e := range ce {
			if used[j] || identity(a) != identity(e) {
				continue
			}
			used[j] = true
			matched[i] = j
			compareNode(childPath(path, a, i), a, e, diffs)
			break
		}
		if matched[i] < 0 {
			*diffs = append(*diffs, Diff{path + "/" + a.Tag, "extra", "升级产物多出 <" + snippet(a) + ">"})
		}
	}
	for j, e := range ce {
		if !used[j] {
			*diffs = append(*diffs, Diff{path + "/" + e.Tag, "missing", "升级产物缺少 <" + snippet(e) + ">"})
		}
	}
	// 全部对位成功但位置不同 → 只记顺序差异。
	if len(ca) == len(ce) {
		same := true
		for i := range ca {
			if matched[i] != i {
				same = false
				break
			}
		}
		if !same {
			*diffs = append(*diffs, Diff{path, "order", "子节点集合相同但顺序不同"})
		}
	}
}

// identity 是元素的对位键：标签 + 属性集(忽略子节点与文本)，用于把差异定位到具体子节点。
func identity(n *xmldoc.Node) string {
	if n.IsEntity {
		return "&" + n.EntName + ";"
	}
	var b strings.Builder
	b.WriteString("<" + n.Tag)
	names := make([]string, 0, len(n.Attrs))
	vals := map[string]string{}
	for _, a := range n.Attrs {
		names = append(names, a.Name)
		vals[a.Name] = normAttr(a.Name, a.Value)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString(" " + name + "=" + vals[name])
	}
	// 叶子节点把文本纳入对位键：同一父节点下常有大量同名方法(如 addChecker)，
	// 只靠属性无法区分，会把差异错配成连环 text 差异。
	if len(significant(n.Children)) == 0 {
		b.WriteString(">" + strings.TrimSpace(n.InnerText))
	}
	return b.String()
}

// significant 过滤掉解析器可能保留但语义无关的节点(空行/注释)。
func significant(ns []*xmldoc.Node) []*xmldoc.Node {
	out := make([]*xmldoc.Node, 0, len(ns))
	for _, n := range ns {
		if n.Removed || n.IsComment || n.IsBlank {
			continue
		}
		out = append(out, n)
	}
	return out
}

func sameSequence(a, b []*xmldoc.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if canonical(a[i]) != canonical(b[i]) {
			return false
		}
	}
	return true
}

// canonical 把一个子树序列化成可比较的规范串(忽略缩进/属性序/自闭合风格)。
func canonical(n *xmldoc.Node) string {
	var b strings.Builder
	canonicalInto(&b, n)
	return b.String()
}

func canonicalInto(b *strings.Builder, n *xmldoc.Node) {
	if n.IsEntity {
		b.WriteString("&" + n.EntName + ";")
		return
	}
	b.WriteString("<" + n.Tag)
	names := make([]string, 0, len(n.Attrs))
	vals := map[string]string{}
	for _, a := range n.Attrs {
		names = append(names, a.Name)
		vals[a.Name] = normAttr(a.Name, a.Value)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString(" " + name + "=" + vals[name])
	}
	kids := significant(n.Children)
	if len(kids) == 0 {
		b.WriteString(">")
		b.WriteString(strings.TrimSpace(n.InnerText))
		b.WriteString("</" + n.Tag + ">")
		return
	}
	b.WriteString(">")
	for _, c := range kids {
		canonicalInto(b, c)
	}
	b.WriteString("</" + n.Tag + ">")
}

// commentAttrs 是"仅注释性"的属性：其空白差异不影响运行语义，比对时归一化。
var commentAttrs = map[string]bool{"comment": true, "comments": true}

func normAttr(name, v string) string {
	if commentAttrs[name] {
		return strings.Join(strings.Fields(v), " ")
	}
	return v
}

// attrDiff 比较属性集合，返回人类可读差异。
func attrDiff(a, e *xmldoc.Node) []string {
	am := map[string]string{}
	for _, x := range a.Attrs {
		am[x.Name] = x.Value
	}
	em := map[string]string{}
	for _, x := range e.Attrs {
		em[x.Name] = x.Value
	}
	var out []string
	for k, v := range em {
		av, ok := am[k]
		if !ok {
			out = append(out, fmt.Sprintf("缺少属性 %s=%q", k, v))
		} else if normAttr(k, av) != normAttr(k, v) {
			out = append(out, fmt.Sprintf("属性 %s=%q ≠ %q", k, av, v))
		}
	}
	for k, v := range am {
		if _, ok := em[k]; !ok {
			out = append(out, fmt.Sprintf("多出属性 %s=%q", k, v))
		}
	}
	sort.Strings(out)
	return out
}

// label 给实体/元素一个人可读标签。
func label(n *xmldoc.Node) string {
	if n.IsEntity {
		return "&" + n.EntName + ";"
	}
	return "<" + n.Tag + ">"
}

// snippet 返回元素的开标签(截断)用于差异描述。
func snippet(n *xmldoc.Node) string {
	var b strings.Builder
	b.WriteString(n.Tag)
	for _, a := range n.Attrs {
		b.WriteString(fmt.Sprintf(" %s=%q", a.Name, a.Value))
		if b.Len() > 120 {
			break
		}
	}
	s := b.String()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func childPath(parent string, n *xmldoc.Node, idx int) string {
	tag := n.Tag
	if n.IsEntity {
		tag = "&" + n.EntName + ";"
	}
	return fmt.Sprintf("%s/%s[%d]", parent, tag, idx)
}
