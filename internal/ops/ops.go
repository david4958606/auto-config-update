// Package ops —— 幂等补丁原语。
//
// 每个原语返回 (changed, message, edit)：changed=false 表示已达目标(no-op)、edit=nil。
// 上层据此产出"完整而诚实"的语义 diff —— 每个声明的动作都出一行。
// 原语在内存树上挂合成节点(供跨步可见 + 落盘渲染)，并产出字节编辑记录。
package ops

import (
	"fmt"
	"strings"

	"addex/internal/xmldoc"
)

// Result 是一个原语的执行结果。
//
// Edit 为单个字节编辑；Edits 用于一次动作产出多处编辑(如 set-attr 多命中、uncomment 去标记)。
// 二者至多一个非空。
type Result struct {
	Changed bool
	Message string
	Edit    *xmldoc.Edit // no-op 时为 nil
	Edits   []xmldoc.Edit
}

// AddNode 确保 parent 下存在 <tag class=cls ...>；按 tag 判重(幂等)。
// attrs 为有序键值对，值已由上层做过占位符替换。
func AddNode(parent *xmldoc.Node, tag, cls string, attrs []xmldoc.Attr, before *xmldoc.Node) Result {
	if xmldoc.FindChild(parent, tag) != nil {
		return Result{Changed: false, Message: fmt.Sprintf("对象 <%s> 已存在", tag), Edit: nil}
	}
	el := xmldoc.NewElement(tag)
	el.Attrs = append([]xmldoc.Attr{{Name: "class", Value: cls}}, attrs...)
	edit := &xmldoc.Edit{Kind: xmldoc.Insert, Parent: parent, Child: el}
	if before != nil {
		xmldoc.InsertBefore(parent, el, before)
		edit.Before = before
	} else {
		xmldoc.AppendChild(parent, el)
	}
	return Result{Changed: true, Message: fmt.Sprintf("新增对象 <%s class=%s>", tag, cls), Edit: edit}
}

// AddEntityRef 确保 parent 内存在实体引用 &name;；已存在即幂等。
func AddEntityRef(parent *xmldoc.Node, name string) Result {
	if xmldoc.HasEntity(parent, name) {
		return Result{Changed: false, Message: fmt.Sprintf("实体引用 &%s; 已存在", name), Edit: nil}
	}
	ent := xmldoc.NewEntity(name)
	xmldoc.AppendChild(parent, ent)
	return Result{Changed: true, Message: fmt.Sprintf("新增实体引用 &%s;", name), Edit: &xmldoc.Edit{Kind: xmldoc.Insert, Parent: parent, Child: ent}}
}

// AddMethod 确保 anchor 下存在 <name type="method" ...extra>value</name>。
// hasValue=false 为标志型方法(按名判重、序列化为自闭合)；true 为带值方法(按名+值判重)。
// extra 为额外属性(如 comment=...)，追加在 type="method" 之后；不参与判重。
// before 非 nil 时把新方法插到该既有兄弟节点之前(见 where.before-method)；nil 则追加末尾。
func AddMethod(anchor *xmldoc.Node, name string, value string, hasValue bool, extra []xmldoc.Attr, before *xmldoc.Node) Result {
	if !hasValue {
		if xmldoc.FindChild(anchor, name) != nil {
			return Result{Changed: false, Message: fmt.Sprintf("方法 %s() 已存在", name), Edit: nil}
		}
	} else {
		for _, el := range xmldoc.FindChildren(anchor, name) {
			if el.Text == value {
				return Result{Changed: false, Message: fmt.Sprintf("方法 %s(%s) 已存在", name, value), Edit: nil}
			}
		}
	}
	el := xmldoc.NewElement(name)
	el.Attrs = append([]xmldoc.Attr{{Name: "type", Value: "method"}}, extra...)
	if hasValue {
		el.Text = value
	}
	edit := &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: el}
	if before != nil {
		xmldoc.InsertBefore(anchor, el, before)
		edit.Before = before
	} else {
		xmldoc.AppendChild(anchor, el)
	}
	msg := fmt.Sprintf("新增方法 %s()", name)
	if hasValue {
		msg = fmt.Sprintf("新增方法 %s(%s)", name, value)
	}
	return Result{Changed: true, Message: msg, Edit: edit}
}

// AddIO 确保 anchor 下存在 IO 点位 <name attrs...>，内含 children 声明的各子元素
// (如 <Bd>/<Ch>/<DescriptorList>/<Unit>，按传入顺序)；按 name 判重(幂等)。
// children 复用 xmldoc.Attr：Name=子标签、Value=子元素文本(空文本渲染为自闭合)。
// entity 非空时在子节点末尾追加实体引用 &entity;(由 include-entity 解析得来)。
func AddIO(anchor *xmldoc.Node, name string, attrs, children []xmldoc.Attr, entity string, before *xmldoc.Node) Result {
	if xmldoc.FindChild(anchor, name) != nil {
		return Result{Changed: false, Message: fmt.Sprintf("IO 点位 <%s> 已存在", name), Edit: nil}
	}
	el := xmldoc.NewElement(name)
	el.Attrs = attrs
	appendChildrenAndEntity(el, children, entity)
	edit := &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: el}
	if before != nil {
		xmldoc.InsertBefore(anchor, el, before)
		edit.Before = before
	} else {
		xmldoc.AppendChild(anchor, el)
	}
	return Result{Changed: true, Message: fmt.Sprintf("新增 IO 点位 <%s>", name), Edit: edit}
}

// AddData 确保 anchor 下存在数据点位 <name type="data" attrs...>，内含 children 声明的各子元素
// (如 <Bd>/<Ch>/<Min>/<Max>/<Accuracy>/<DescriptorList>/<Unit>，按传入顺序)；按 name 判重(幂等)。
// 与 AddIO 的差别：首属性固定为 type="data"。entity 语义同 AddIO(于点位内部末尾追加 &entity;)。
// children 复用 xmldoc.Attr：Name=子标签、Value=子元素文本(空文本渲染为成对空标签)。
func AddData(anchor *xmldoc.Node, name string, attrs, children []xmldoc.Attr, entity string, before *xmldoc.Node) Result {
	if xmldoc.FindChild(anchor, name) != nil {
		return Result{Changed: false, Message: fmt.Sprintf("数据点位 <%s> 已存在", name), Edit: nil}
	}
	el := xmldoc.NewElement(name)
	el.Attrs = append([]xmldoc.Attr{{Name: "type", Value: "data"}}, attrs...)
	appendChildrenAndEntity(el, children, entity)
	edit := &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: el}
	if before != nil {
		xmldoc.InsertBefore(anchor, el, before)
		edit.Before = before
	} else {
		xmldoc.AppendChild(anchor, el)
	}
	return Result{Changed: true, Message: fmt.Sprintf("新增数据点位 <%s>", name), Edit: edit}
}

// appendChildrenAndEntity 把 children 逐个作为子元素挂到 el 下(空文本渲染成对空标签)，
// 最后 entity 非空时追加实体引用 &entity;。add-io / add-data 共用。
func appendChildrenAndEntity(el *xmldoc.Node, children []xmldoc.Attr, entity string) {
	for _, c := range children {
		sub := xmldoc.NewElement(c.Name)
		sub.Text = c.Value
		sub.PairedEmpty = true // 空值子元素渲染成 <Unit></Unit> 而非 <Unit/>(与 IG 片段既有写法一致)
		xmldoc.AppendChild(el, sub)
	}
	if entity != "" {
		xmldoc.AppendChild(el, xmldoc.NewEntity(entity))
	}
}

// AddRawFragment 在 anchor 下插入一段"已带缩进"的 XML 原文(内联片段)，逐字落盘，
// 不经过渲染器——这样才能保留 &amp;&amp; 这类混合内容。frag 用于判重(结构一致即 no-op)。
func AddRawFragment(anchor, frag *xmldoc.Node, raw string, before *xmldoc.Node) Result {
	for _, c := range anchor.Children {
		if c.Removed {
			continue
		}
		if xmldoc.Subs(c, frag) {
			return Result{Changed: false, Message: fmt.Sprintf("片段 <%s> 已存在", frag.Tag), Edit: nil}
		}
	}
	return Result{Changed: true, Message: fmt.Sprintf("新增片段 <%s>", frag.Tag),
		Edit: &xmldoc.Edit{Kind: xmldoc.InsertRaw, Parent: anchor, Before: before, Text: raw}}
}

// AddComment 确保 anchor 下存在一行注释 raw(完整 <!--...-->，原样输出)。
// 判重：注释/空行不进解析树(解析器跳过)，故按 anchor 的【原始字节区间 inner】整串包含判重——
// 二次 apply 时 inner 已含该注释即 no-op。追加到 anchor 末尾(由上层控制相对顺序)。
func AddComment(anchor *xmldoc.Node, raw, inner string) Result {
	if strings.Contains(inner, raw) {
		return Result{Changed: false, Message: fmt.Sprintf("注释 %s 已存在", raw), Edit: nil}
	}
	c := xmldoc.NewComment(raw)
	xmldoc.AppendChild(anchor, c)
	return Result{Changed: true, Message: fmt.Sprintf("新增注释 %s", raw), Edit: &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: c}}
}

// AddBlank 确保 anchor 下有一处空行分隔(n 行)。判重同 AddComment：inner 已含空行(两换行间仅空白)
// 即 no-op——避免二次 apply 反复堆空行。
func AddBlank(anchor *xmldoc.Node, n int, inner string) Result {
	if n < 1 {
		return Result{Changed: false, Message: "空行数 <1，忽略", Edit: nil}
	}
	if hasBlankLine(inner) {
		return Result{Changed: false, Message: "空行已存在", Edit: nil}
	}
	b := xmldoc.NewBlank(n)
	xmldoc.AppendChild(anchor, b)
	msg := "新增空行"
	if n > 1 {
		msg = fmt.Sprintf("新增 %d 行空行", n)
	}
	return Result{Changed: true, Message: msg, Edit: &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: b}}
}

// hasBlankLine 报告 s 中是否存在空行：某个换行之后(只隔空白)紧跟另一个换行。
func hasBlankLine(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
			j++
		}
		if j < len(s) && s[j] == '\n' {
			return true
		}
	}
	return false
}

// RemoveMethod 删除 anchor 下匹配 (名字+值) 的方法调用；一个都没有 → no-op。
// 命中多个记为一处删除编辑(与 Python 一致，一次删净)。
func RemoveMethod(anchor *xmldoc.Node, name, value string) Result {
	var hits []*xmldoc.Node
	for _, el := range xmldoc.FindChildren(anchor, name) {
		if el.Text == value {
			el.Removed = true
			hits = append(hits, el)
		}
	}
	if len(hits) > 0 {
		return Result{Changed: true, Message: fmt.Sprintf("删除方法 %s(%s)", name, value), Edit: &xmldoc.Edit{Kind: xmldoc.Delete, Targets: hits}}
	}
	return Result{Changed: false, Message: fmt.Sprintf("方法 %s(%s) 不存在，无需删除", name, value), Edit: nil}
}

// ───────────────────────── 通用选择器 ─────────────────────────

// Sel 是一个元素选择器：tag 相同(空=不限) + attrs 全等 + Value 文本相等(非 nil 时) +
// Has 指定的直接子元素必须存在(用于区分同名但内部不同的节点)。
type Sel struct {
	Tag   string
	Attrs map[string]string
	Value *string
	Has   *Sel
}

// Match 报告 n 是否满足选择器。
func (s Sel) Match(n *xmldoc.Node) bool {
	if n == nil || n.Removed || n.IsEntity || n.IsComment || n.IsBlank {
		return false
	}
	if s.Tag != "" && n.Tag != s.Tag {
		return false
	}
	for k, v := range s.Attrs {
		// 必须"存在该属性且取值相等"：attr: {alias: ""} 不应命中"根本没有 alias"的节点，
		// 否则 add-xml 新增的节点会被 remove-node 的旧选择器反复误删(破坏幂等)。
		if !n.HasAttr(k) || n.Attr(k) != v {
			return false
		}
	}
	if s.Value != nil && n.InnerText != *s.Value {
		return false
	}
	if s.Has != nil {
		found := false
		for _, c := range n.Children {
			if s.Has.Match(c) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// SelectNodes 在 anchor 的**整棵子树**里按 s 选；child 非空时再下沉一层到匹配 child 的孙子。
// 返回命中的目标节点(有 child 时返回 child 节点本身，否则返回 tag 节点)。
// 用于 set-text/set-attr/remove-node/wrap 这类"找到已存在节点"的原地改写。
func SelectNodes(anchor *xmldoc.Node, s Sel, child string) []*xmldoc.Node {
	var out []*xmldoc.Node
	descend := func(el *xmldoc.Node) {
		if child == "" {
			out = append(out, el)
			return
		}
		for _, c := range el.Children {
			if (Sel{Tag: child}).Match(c) {
				out = append(out, c)
			}
		}
	}
	var walk func(n *xmldoc.Node)
	walk = func(n *xmldoc.Node) {
		for _, c := range n.Children {
			if c.Removed || c.IsEntity {
				continue
			}
			if s.Match(c) {
				descend(c)
			}
			walk(c)
		}
	}
	walk(anchor)
	return out
}

// selectChildren 只在 anchor 的**直接子元素**里按 s 选(用于 before/after 插入定位)。
func selectChildren(anchor *xmldoc.Node, s Sel, child string) []*xmldoc.Node {
	var out []*xmldoc.Node
	for _, el := range anchor.Children {
		if !s.Match(el) {
			continue
		}
		if child == "" {
			out = append(out, el)
			continue
		}
		for _, c := range el.Children {
			if (Sel{Tag: child}).Match(c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// attrIndex 返回 n 上名为 name 的属性下标；不存在返回 -1。
func attrIndex(n *xmldoc.Node, name string) int {
	for i, a := range n.Attrs {
		if a.Name == name {
			return i
		}
	}
	return -1
}

// SetText 把 anchor 下按 s 选中的元素(可选下沉到 child)文本改成 text。已等于目标值 → no-op。
func SetText(anchor *xmldoc.Node, s Sel, child, old string, hasOld bool, text string) Result {
	hits := SelectNodes(anchor, s, child)
	var todo []*xmldoc.Node
	for _, n := range hits {
		if hasOld && n.InnerText != old {
			continue
		}
		if n.InnerText == text {
			continue
		}
		todo = append(todo, n)
	}
	if len(todo) == 0 {
		if len(hits) == 0 {
			return Result{Changed: false, Message: fmt.Sprintf("未找到 <%s>，无需改文本", selDesc(s, child)), Edit: nil}
		}
		return Result{Changed: false, Message: fmt.Sprintf("<%s> 文本已是目标值，无需修改", selDesc(s, child)), Edit: nil}
	}
	for _, n := range todo {
		n.Text = text
		n.InnerText = text
	}
	return Result{Changed: true, Message: fmt.Sprintf("改写 <%s> 文本 → %s", selDesc(s, child), text), Edit: &xmldoc.Edit{Kind: xmldoc.SetText, Targets: todo, Text: text}}
}

// SetAttr 把 anchor 下按 s 选中的元素(可选下沉到 child)的 name 属性设为 value。
// hasOld 为真时只命中"当前该属性值 == old"的元素；已是目标值 → no-op。
func SetAttr(anchor *xmldoc.Node, s Sel, child, old string, hasOld bool, name, value string) Result {
	hits := SelectNodes(anchor, s, child)
	var edits []xmldoc.Edit
	changed := 0
	for _, n := range hits {
		cur := n.Attr(name)
		if hasOld && cur != old {
			continue
		}
		if attrIndex(n, name) >= 0 && cur == value {
			continue
		}
		changed++
		if e, ok := setAttrEdit(n, name, value); ok {
			edits = append(edits, e)
		}
	}
	if changed == 0 {
		if len(hits) == 0 {
			return Result{Changed: false, Message: fmt.Sprintf("未找到 <%s>，无需改属性", selDesc(s, child)), Edit: nil}
		}
		return Result{Changed: false, Message: fmt.Sprintf("<%s> 属性 %s 已是目标值，无需修改", selDesc(s, child), name), Edit: nil}
	}
	res := Result{Changed: true, Message: fmt.Sprintf("改写 <%s> 属性 %s → %s", selDesc(s, child), name, value)}
	if len(edits) == 1 {
		res.Edit = &edits[0]
	} else if len(edits) > 1 {
		res.Edits = edits
	}
	return res
}

// setAttrEdit 把节点 n 的属性 name 设为 value，返回字节编辑。
// 合成节点无字节区间 → 只改内存并返回 ok=false；已有该属性且有值区间 → 原位替换值；
// 否则在开标签 '>' (自闭合在 '/>' )之前插入 ` name="value"`。两种情形都会同步内存树。
func setAttrEdit(n *xmldoc.Node, name, value string) (xmldoc.Edit, bool) {
	if n.Synthetic {
		setAttrInMemory(n, name, value)
		return xmldoc.Edit{}, false
	}
	if i := attrIndex(n, name); i >= 0 && n.Attrs[i].ValueStart >= 0 {
		edit := xmldoc.Edit{Kind: xmldoc.Replace,
			Start: n.Attrs[i].ValueStart, End: n.Attrs[i].ValueEnd, Text: xmldoc.EscapeAttr(value)}
		setAttrInMemory(n, name, value)
		return edit, true
	}
	at := n.OpenEnd - 1 // '>' 之前
	if n.SelfClose {
		at = n.OpenEnd - 2 // '/>' 之前
	}
	edit := xmldoc.Edit{Kind: xmldoc.Replace, Start: at, End: at,
		Text: fmt.Sprintf(` %s="%s"`, name, xmldoc.EscapeAttr(value))}
	setAttrInMemory(n, name, value)
	return edit, true
}

func setAttrInMemory(n *xmldoc.Node, name, value string) {
	if i := attrIndex(n, name); i >= 0 {
		n.Attrs[i].Value = value
		return
	}
	n.Attrs = append(n.Attrs, xmldoc.Attr{Name: name, Value: value})
}

// RemoveNode 删除 anchor 下按 s 选中的元素(可选下沉到 child)，含整棵子树。无命中 → no-op。
func RemoveNode(anchor *xmldoc.Node, s Sel, child string) Result {
	hits := SelectNodes(anchor, s, child)
	if len(hits) == 0 {
		return Result{Changed: false, Message: fmt.Sprintf("未找到 <%s>，无需删除", selDesc(s, child)), Edit: nil}
	}
	for _, n := range hits {
		n.Removed = true
	}
	return Result{Changed: true, Message: fmt.Sprintf("删除 <%s>", selDesc(s, child)), Edit: &xmldoc.Edit{Kind: xmldoc.Delete, Targets: hits}}
}

// RenameNode 把 anchor 下按 s 选中的元素(可选下沉到 child)改名并/或增改属性：
//   - to 非空 → 同步改写开标签与闭标签里的标签名(自闭合只改开标签)，属性/子节点/文本原样保留；
//   - attrs  → 逐个在所选元素上设置(已有则原位替换值，没有则在开标签 '>' 前插入)，保留书写顺序。
//
// 选择器字段全空(tag/child/attr/value/has 都没给)时，作用对象是 **anchor 自身**；
// 给了任一选择器则在 anchor 的整棵子树里选(可命中多个，全部处理)。
// 判重：标签已是 to 且 attrs 都已是目标值 → no-op(幂等)。
func RenameNode(anchor *xmldoc.Node, s Sel, child, to string, attrs []xmldoc.Attr) Result {
	desc := selDesc(s, child)
	targets, self := selectForRename(anchor, s, child)
	if self {
		desc = "anchor:" + anchor.Tag
	}
	if len(targets) == 0 {
		return Result{Changed: false, Message: fmt.Sprintf("未找到 <%s>，无需改名", desc), Edit: nil}
	}
	var edits []xmldoc.Edit
	renamed, attrChanged := 0, 0
	for _, n := range targets {
		if to != "" && n.Tag != to {
			renamed++
			if !n.Synthetic {
				// 开标签名：'<' 之后 len(旧标签) 个字节。
				edits = append(edits, xmldoc.Edit{Kind: xmldoc.Replace,
					Start: n.Start + 1, End: n.Start + 1 + len(n.Tag), Text: to})
				// 闭标签名：'</' 之后同样长度(自闭合没有闭标签)。
				if !n.SelfClose && n.CloseStart >= 0 {
					edits = append(edits, xmldoc.Edit{Kind: xmldoc.Replace,
						Start: n.CloseStart + 2, End: n.CloseStart + 2 + len(n.Tag), Text: to})
				}
			}
			n.Tag = to
		}
		// 属性：已达目标值的跳过，其余一次性合并处理(见 setAttrsOnNode)。
		var pending []xmldoc.Attr
		for _, a := range attrs {
			if n.HasAttr(a.Name) && n.Attr(a.Name) == a.Value {
				continue
			}
			pending = append(pending, a)
		}
		if len(pending) > 0 {
			attrEdits, c := setAttrsOnNode(n, pending)
			edits = append(edits, attrEdits...)
			attrChanged += c
		}
	}
	if renamed == 0 && attrChanged == 0 {
		return Result{Changed: false, Message: fmt.Sprintf("<%s> 标签/属性已是目标值，无需改名", desc), Edit: nil}
	}
	var msg string
	switch {
	case renamed > 0 && attrChanged > 0:
		msg = fmt.Sprintf("重命名 <%s> → <%s> 并设置 %d 个属性", desc, to, attrChanged)
	case renamed > 0:
		msg = fmt.Sprintf("重命名 <%s> → <%s>", desc, to)
	default:
		msg = fmt.Sprintf("设置 <%s> 的 %d 个属性", desc, attrChanged)
	}
	res := Result{Changed: true, Message: msg}
	res.Edits = edits
	return res
}

// selectForRename 返回 rename-node 的作用节点与"是否作用在 anchor 自身"。
// 选择器字段全空 → 作用于 anchor 自身；否则常规子树选择。
func selectForRename(anchor *xmldoc.Node, s Sel, child string) ([]*xmldoc.Node, bool) {
	if s.Tag == "" && child == "" && len(s.Attrs) == 0 && s.Value == nil && s.Has == nil {
		return []*xmldoc.Node{anchor}, true
	}
	return SelectNodes(anchor, s, child), false
}

// setAttrsOnNode 按顺序在节点 n 上设置 attrs，返回字节编辑与改动个数。
// 已有属性原位替换其值；缺失的属性**合并成一处插入**(按书写顺序)，避免同一偏移的多次
// 零长插入在 splice 里被倒序。合成节点无字节区间 → 只改内存，返回的编辑为空。
func setAttrsOnNode(n *xmldoc.Node, attrs []xmldoc.Attr) ([]xmldoc.Edit, int) {
	var edits []xmldoc.Edit
	var insert strings.Builder
	changed := 0
	for _, a := range attrs {
		if n.HasAttr(a.Name) && n.Attr(a.Name) == a.Value {
			continue
		}
		changed++
		if i := attrIndex(n, a.Name); i >= 0 && n.Attrs[i].ValueStart >= 0 {
			edits = append(edits, xmldoc.Edit{Kind: xmldoc.Replace,
				Start: n.Attrs[i].ValueStart, End: n.Attrs[i].ValueEnd, Text: xmldoc.EscapeAttr(a.Value)})
			setAttrInMemory(n, a.Name, a.Value)
			continue
		}
		insert.WriteString(fmt.Sprintf(` %s="%s"`, a.Name, xmldoc.EscapeAttr(a.Value)))
		setAttrInMemory(n, a.Name, a.Value)
	}
	if insert.Len() > 0 && !n.Synthetic {
		at := n.OpenEnd - 1 // '>' 之前
		if n.SelfClose {
			at = n.OpenEnd - 2 // '/>' 之前
		}
		// 插入点(start 最大)排最前：splice 按 start 倒序应用，先插属性再原位替换值，
		// 后者用的仍是原始偏移，互不影响。
		edits = append([]xmldoc.Edit{{Kind: xmldoc.Replace, Start: at, End: at, Text: insert.String()}}, edits...)
	}
	return edits, changed
}

// WrapRange 用 open/close 把 anchor 下若干选择器命中的节点区间包裹起来(注释掉 / CDATA 化)。
// 每个选择器可命中多个节点；实际区间 = 全部命中节点的 [min(Start), max(End))。
// alreadyWrapped 为真(紧邻处已有 open/close)或命中为空 → no-op。
func WrapRange(anchor *xmldoc.Node, sels []Sel, child string, open, close string, src []byte) Result {
	var hits []*xmldoc.Node
	for _, s := range sels {
		hits = append(hits, SelectNodes(anchor, s, child)...)
	}
	if len(hits) == 0 {
		return Result{Changed: false, Message: fmt.Sprintf("未找到待包裹的节点(open=%s)", open), Edit: nil}
	}
	lo, hi := -1, -1
	for _, n := range hits {
		if n.Start < 0 || n.End < 0 {
			continue
		}
		if lo < 0 || n.Start < lo {
			lo = n.Start
		}
		if n.End > hi {
			hi = n.End
		}
	}
	if lo < 0 {
		return Result{Changed: false, Message: "待包裹的节点均为合成节点，忽略", Edit: nil}
	}
	if alreadyWrapped(src, lo, hi, open, close) {
		return Result{Changed: false, Message: fmt.Sprintf("区间已被 %s … %s 包裹", open, close), Edit: nil}
	}
	return Result{Changed: true, Message: fmt.Sprintf("包裹 %d 个节点 %s…%s", len(hits), open, close), Edit: &xmldoc.Edit{Kind: xmldoc.Wrap, Targets: hits, Open: open, Close: close}}
}

// alreadyWrapped 报告 [lo,hi) 紧邻处是否已有 open/close 标记(允许标记内空白差异)。
func alreadyWrapped(src []byte, lo, hi int, open, close string) bool {
	if lo > len(src) || hi > len(src) {
		return false
	}
	pre := strings.TrimRight(string(src[:lo]), " \t")
	post := strings.TrimLeft(string(src[hi:]), " \t")
	return strings.HasSuffix(pre, strings.TrimSpace(open)) && strings.HasPrefix(post, strings.TrimSpace(close))
}

// Uncomment 放开被注释包住的片段：在 src 中定位 find，向前找最近的 open、向后找最近的 close，
// 去掉这对外层标记；drop=true 时连注释内容一起删除。找不到包裹 → no-op。
func Uncomment(src []byte, find, open, close string, drop bool) Result {
	pos := strings.Index(string(src), find)
	if pos < 0 {
		return Result{Changed: false, Message: fmt.Sprintf("原文中未找到 %q，无需放开注释", find), Edit: nil}
	}
	lo := strings.LastIndex(string(src[:pos]), open)
	if lo < 0 {
		return Result{Changed: false, Message: fmt.Sprintf("%q 未被 %s 包裹", find, open), Edit: nil}
	}
	// 只接受"从 lo 处 open 到其后第一个 close"这一段确实包住 find 的情形。
	rel := strings.Index(string(src[lo+len(open):]), close)
	if rel < 0 {
		return Result{Changed: false, Message: fmt.Sprintf("%s 之后找不到 %s", open, close), Edit: nil}
	}
	hi := lo + len(open) + rel // close 起点
	if !(lo < pos && pos < hi) {
		return Result{Changed: false, Message: fmt.Sprintf("%q 不在最近的注释块内", find), Edit: nil}
	}
	// 去掉标记时，一并吃掉紧邻标记内侧的空白，避免留下空行。
	_, _ = lo, hi
	if drop {
		end := hi + len(close)
		return Result{Changed: true, Message: fmt.Sprintf("删除注释块(%d 字节)", end-lo), Edit: &xmldoc.Edit{Kind: xmldoc.Replace, Start: lo, End: end, Text: ""}}
	}
	edits := []xmldoc.Edit{
		{Kind: xmldoc.Replace, Start: hi, End: hi + len(close), Text: ""},
		{Kind: xmldoc.Replace, Start: lo, End: lo + len(open), Text: ""},
	}
	res := Result{Changed: true, Message: "放开注释块"}
	res.Edits = edits
	return res
}

// AddElement 在 anchor 下确保存在 <tag attrs...>text</tag>(或自闭合)；按
// tag + attrs + text 判重(幂等)。selfClose=true 渲染为 <tag .../>；否则 text 非空渲染成对标签，
// 空文本按 pairedEmpty 决定 <tag></tag> 还是 <tag/>。before 非 nil 时插到该既有兄弟之前。
func AddElement(anchor *xmldoc.Node, tag string, attrs []xmldoc.Attr, text string, selfClose, pairedEmpty bool, before *xmldoc.Node) Result {
	for _, el := range anchor.Children {
		if el.Removed || el.IsEntity || el.Tag != tag || el.Text != text {
			continue
		}
		if attrsEqual(el.Attrs, attrs) {
			return Result{Changed: false, Message: fmt.Sprintf("元素 <%s> 已存在", tag), Edit: nil}
		}
	}
	el := xmldoc.NewElement(tag)
	el.Attrs = attrs
	el.Text = text
	el.PairedEmpty = pairedEmpty
	edit := &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: el}
	if before != nil {
		xmldoc.InsertBefore(anchor, el, before)
		edit.Before = before
	} else {
		xmldoc.AppendChild(anchor, el)
	}
	return Result{Changed: true, Message: fmt.Sprintf("新增元素 <%s>", tag), Edit: edit}
}

// AddSetupPair 在 Setup 文档的 anchor(缺省=文件根元素)下成对追加一个参数声明与取值：
//
//	<Param name=... attrs.../>                        追加到 <Param> 序列末尾(首个 <Option> 之前)
//	<Option>…<Value paramName=...>value</Value></Option>  追加到该 <Option> 末尾
//
// 两侧各自按 name / paramName 判重：已存在的一侧不动，只补缺失的一侧，故二次执行幂等。
// 找不到 <Option> 时只追加 <Param>，并在消息里以 ! 告警。
func AddSetupPair(anchor *xmldoc.Node, name string, paramAttrs []xmldoc.Attr, value string) Result {
	var edits []xmldoc.Edit
	var added []string

	if !hasNamedChild(anchor, "Param", "name", name) {
		el := xmldoc.NewElement("Param")
		el.Attrs = paramAttrs
		edit := xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: el}
		if before := setupParamInsertBefore(anchor); before != nil {
			xmldoc.InsertBefore(anchor, el, before)
			edit.Before = before
		} else {
			xmldoc.AppendChild(anchor, el)
		}
		edits = append(edits, edit)
		added = append(added, "Param")
	}

	option := xmldoc.FindChild(anchor, "Option")
	switch {
	case option == nil:
		if len(added) == 0 {
			return Result{Changed: false, Message: fmt.Sprintf("Setup 参数 %s 已存在", name)}
		}
		return Result{Changed: true, Edits: edits,
			Message: fmt.Sprintf("新增 Setup 参数 %s（! 未找到 <Option>，未加取值）", name)}
	case !hasNamedChild(option, "Value", "paramName", name):
		el := xmldoc.NewElement("Value")
		el.Attrs = []xmldoc.Attr{{Name: "paramName", Value: name}}
		el.Text = value
		el.PairedEmpty = true // 空取值渲染成 <Value paramName="X"></Value>(与 Setup 既有写法一致)
		xmldoc.AppendChild(option, el)
		edits = append(edits, xmldoc.Edit{Kind: xmldoc.Insert, Parent: option, Child: el})
		added = append(added, "Value")
	}

	if len(added) == 0 {
		return Result{Changed: false, Message: fmt.Sprintf("Setup 参数 %s 及其取值已存在", name)}
	}
	res := Result{Changed: true, Message: fmt.Sprintf("新增 Setup 参数 %s（%s）", name, strings.Join(added, "+"))}
	if len(edits) == 1 {
		res.Edit = &edits[0]
	} else {
		res.Edits = edits
	}
	return res
}

// hasNamedChild 报告 parent 下是否存在 <tag key="value"> 的直接子元素(跳过已删除/实体)。
func hasNamedChild(parent *xmldoc.Node, tag, key, value string) bool {
	for _, c := range parent.Children {
		if c.Removed || c.IsEntity || c.Tag != tag {
			continue
		}
		if c.HasAttr(key) && c.Attr(key) == value {
			return true
		}
	}
	return false
}

// setupParamInsertBefore 返回"新增 <Param> 应插到其前"的兄弟节点，使新声明落在 <Param>
// 序列末尾：取最后一个 <Param> 之后的下一个原节点；没有 <Param> 时取首个 <Option>；
// 都没有则 nil(追加到父末尾)。合成节点无字节区间，不作定位点。
func setupParamInsertBefore(anchor *xmldoc.Node) *xmldoc.Node {
	last := -1
	for i, c := range anchor.Children {
		if !c.Removed && !c.IsEntity && c.Tag == "Param" {
			last = i
		}
	}
	if last >= 0 {
		for j := last + 1; j < len(anchor.Children); j++ {
			if c := anchor.Children[j]; !c.Removed && !c.Synthetic {
				return c
			}
		}
		return nil
	}
	return xmldoc.FindChild(anchor, "Option")
}

func attrsEqual(a, b []xmldoc.Attr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

// selDesc 生成选择器的人类可读描述。
func selDesc(s Sel, child string) string {
	d := s.Tag
	if child != "" {
		d += "/" + child
	}
	return d
}

// InsertAfterNode 返回 anchor 下首个满足选择器的节点之后的下一个兄弟(用作 Insert.Before)；
// 无匹配或无后继 → nil(退化为追加末尾)。
func InsertAfterNode(anchor *xmldoc.Node, s Sel) *xmldoc.Node {
	hits := selectChildren(anchor, s, "")
	if len(hits) == 0 {
		return nil
	}
	target := hits[0]
	for i, c := range anchor.Children {
		if c != target {
			continue
		}
		for j := i + 1; j < len(anchor.Children); j++ {
			// 跳过已删除与本次合成节点：合成节点无字节区间，不能作为落盘定位点；
			// 同一锚点连续插入时都落在同一个原始后继之前，按声明顺序拼接。
			if anchor.Children[j].Removed || anchor.Children[j].Synthetic {
				continue
			}
			return anchor.Children[j]
		}
		return nil
	}
	return nil
}

// InsertBeforeNode 供原语共用：把 before 解析成 anchor 下首个满足选择器的子节点。
func InsertBeforeNode(anchor *xmldoc.Node, s *Sel, child string) *xmldoc.Node {
	if s == nil {
		return nil
	}
	hits := selectChildren(anchor, *s, child)
	if len(hits) == 0 {
		return nil
	}
	// 插到该节点的"外层节点"之前：有 child 时用其父元素。
	if child != "" {
		return hits[0].Parent
	}
	return hits[0]
}
