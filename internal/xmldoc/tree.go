package xmldoc

import "strings"

// EditKind 区分插入、删除与原地改写。
type EditKind int

const (
	Insert    EditKind = iota // 把 Child 插到 Parent 的新子位置
	Delete                    // 删除 Targets 各原节点所在整行
	SetText                   // 把 Target 元素的文本改成 Text(保留开/闭标签原文)
	Wrap                      // 在 Targets 区间前后插入 Open/Close 标记(注释掉 / CDATA 化)
	Replace                   // 把原文 [Start,End) 直接替换成 Text(放开注释等文本级编辑)
	InsertRaw                 // 把 Text 原样插到 Parent 的 Before 之前(用于内联 XML 片段)
)

// Edit 由 ops 产出、splice 消费(计划核心结构)。
//
//   - Insert：把合成节点 Child 渲染后插到 Parent 闭合标签之前(Parent 为合成节点则跳过，
//     其文本已含在祖先渲染里)。
//   - Delete：删掉 Targets 各原节点的原文行区间(一次 remove-method 可命中多个，算一处编辑)。
//   - SetText：把 Target 元素的 inner 文本替换为 Text(缩进与标签原文不动)。
//   - Wrap：在 Targets 覆盖的字节区间 [min(Start), max(End)) 前后插 Open/Close。
//   - Replace：把原文 [Start,End) 替换为 Text。
//   - InsertRaw：把 Text(已带缩进的整段 XML 原文)插到 Parent 的 Before 之前；
//     不经过渲染器，故 &amp;&amp; 这类混合内容逐字保留。Text 已含缩进，不带行尾换行。
type Edit struct {
	Kind    EditKind
	Parent  *Node   // Insert 用
	Child   *Node   // Insert 用
	Before  *Node   // Insert 用(可选)：插到该既有兄弟节点之前；nil = 父闭合标签之前(追加)
	Targets []*Node // Delete / SetText / Wrap 用
	Open    string  // Wrap 用：区间前插入的文本
	Close   string  // Wrap 用：区间后插入的文本
	Text    string  // SetText / Replace 用
	Start   int     // Replace 用
	End     int     // Replace 用
}

// RealDepth 返回节点在真实文件里的缩进深度(腔室根 = 0)。
func RealDepth(n *Node) int {
	d := 0
	for p := n.Parent; p != nil; p = p.Parent {
		d++
	}
	return d
}

// FindChild 返回首个标签为 tag 的直接子元素(跳过已删除)，无则 nil。
func FindChild(parent *Node, tag string) *Node {
	for _, c := range parent.Children {
		if !c.Removed && !c.IsEntity && c.Tag == tag {
			return c
		}
	}
	return nil
}

// FindChildren 返回全部标签为 tag 的直接子元素(跳过已删除)。
func FindChildren(parent *Node, tag string) []*Node {
	var out []*Node
	for _, c := range parent.Children {
		if !c.Removed && !c.IsEntity && c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// HasEntity 报告 parent 下是否已有名为 name 的实体引用(跳过已删除)。
func HasEntity(parent *Node, name string) bool {
	for _, c := range parent.Children {
		if !c.Removed && c.IsEntity && c.EntName == name {
			return true
		}
	}
	return false
}

// AppendChild 把 child 挂到 parent 下，维持"实体引用永远在最后一位"的书写惯例：
// 若当前末子是实体引用，则把 child 插到它【前面】(与 Python _append_indented 一致)。
func AppendChild(parent, child *Node) {
	child.Parent = parent
	kids := parent.Children
	if n := len(kids); n > 0 && kids[n-1].IsEntity {
		parent.Children = append(kids[:n-1:n-1], child, kids[n-1])
		return
	}
	parent.Children = append(parent.Children, child)
}

// InsertBefore 把 child 插到 parent.Children 中 ref 之前；ref 不在其中则退化为 AppendChild。
func InsertBefore(parent, child, ref *Node) {
	child.Parent = parent
	for i, c := range parent.Children {
		if c == ref {
			tail := append([]*Node{child}, parent.Children[i:]...)
			parent.Children = append(parent.Children[:i:i], tail...)
			return
		}
	}
	AppendChild(parent, child)
}

// MarkSynthetic 把一棵从"片段字符串"解析出来的子树整体标记为合成节点：
// 清掉相对片段自身的字节区间，保留结构/文本/实体；成对空标签保持成对写法。
// 供 add-xml(内联片段) 使用——区间若留着会被 splice 误认为原件而按错误偏移落盘。
func MarkSynthetic(n *Node) {
	n.Synthetic = true
	n.Start, n.End, n.OpenEnd, n.CloseStart = -1, -1, -1, -1
	n.Removed = false
	if !n.IsEntity && !n.IsComment && !n.IsBlank && !n.SelfClose && n.Text == "" && len(n.Children) == 0 {
		n.PairedEmpty = true
	}
	for _, c := range n.Children {
		MarkSynthetic(c)
	}
}

// Subs 报告两棵子树是否结构/属性/文本/实体完全一致(属性按集合比较，忽略书写顺序)。
func Subs(a, b *Node) bool {
	if a.IsEntity || b.IsEntity {
		return a.IsEntity && b.IsEntity && a.EntName == b.EntName
	}
	if a.Tag != b.Tag || len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for _, x := range a.Attrs {
		found := false
		for _, y := range b.Attrs {
			if x.Name == y.Name && x.Value == y.Value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if strings.TrimSpace(a.InnerText) != strings.TrimSpace(b.InnerText) {
		return false
	}
	ac, bc := liveChildren(a), liveChildren(b)
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		if !Subs(ac[i], bc[i]) {
			return false
		}
	}
	return true
}

func liveChildren(n *Node) []*Node {
	out := make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		if c.Removed {
			continue
		}
		out = append(out, c)
	}
	return out
}

// NewElement 造一个合成元素节点。
func NewElement(tag string) *Node {
	return &Node{Tag: tag, Synthetic: true, Start: -1, End: -1, OpenEnd: -1, CloseStart: -1}
}

// NewEntity 造一个合成实体引用节点。
func NewEntity(name string) *Node {
	return &Node{IsEntity: true, EntName: name, Synthetic: true, Start: -1, End: -1, OpenEnd: -1, CloseStart: -1}
}

// NewComment 造一个合成注释节点；raw 为完整注释原文(含 <!-- -->)，落盘时原样输出。
func NewComment(raw string) *Node {
	return &Node{IsComment: true, Raw: raw, Synthetic: true, Start: -1, End: -1, OpenEnd: -1, CloseStart: -1}
}

// NewBlank 造一个合成空行节点，渲染成 n 个空行(n<1 按 1 计)。
func NewBlank(n int) *Node {
	if n < 1 {
		n = 1
	}
	return &Node{IsBlank: true, BlankCount: n, Synthetic: true, Start: -1, End: -1, OpenEnd: -1, CloseStart: -1}
}

// hasElementChildren 报告是否有(未删除的)元素或实体子节点。
func (n *Node) hasElementChildren() bool {
	for _, c := range n.Children {
		if !c.Removed {
			return true
		}
	}
	return false
}

// Render 把一个(通常是合成的)节点渲染成缩进文本，首行缩进 depth 层(4 空格/层)，
// 结尾【不带】换行。三种形态：
//
//	实体            → &Name;
//	有子节点        → <tag attrs>\n  子...\n</tag>
//	仅文本          → <tag attrs>text</tag>
//	空              → <tag attrs/>(PairedEmpty=true 时改为 <tag attrs></tag>)
func Render(n *Node, depth int) string {
	// 空行：不带缩进；BlankCount 行空行 = BlankCount-1 个换行(splice 落盘时再补一个)。
	if n.IsBlank {
		return strings.Repeat("\n", n.BlankCount-1)
	}
	indent := strings.Repeat("    ", depth)
	// 注释：原样输出(首行按深度缩进)。
	if n.IsComment {
		return indent + n.Raw
	}
	if n.IsEntity {
		return indent + "&" + n.EntName + ";"
	}
	open := "<" + n.Tag + renderAttrs(n.Attrs)
	if n.hasElementChildren() {
		var b strings.Builder
		b.WriteString(indent + open + ">")
		for _, c := range n.Children {
			if c.Removed {
				continue
			}
			b.WriteString("\n" + Render(c, depth+1))
		}
		b.WriteString("\n" + indent + "</" + n.Tag + ">")
		return b.String()
	}
	if n.Text != "" {
		return indent + open + ">" + escapeText(n.Text) + "</" + n.Tag + ">"
	}
	if n.PairedEmpty {
		return indent + open + "></" + n.Tag + ">"
	}
	return indent + open + "/>"
}

func renderAttrs(attrs []Attr) string {
	var b strings.Builder
	for _, a := range attrs {
		b.WriteString(" " + a.Name + `="` + escapeAttr(a.Value) + `"`)
	}
	return b.String()
}

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }

// EscapeText / EscapeAttr 供其它包(set-text、set-attr 的字节替换)复用同一套转义规则。
func EscapeText(s string) string { return escapeText(s) }
func EscapeAttr(s string) string { return escapeAttr(s) }
