// Package ops —— 幂等补丁原语。
//
// 每个原语返回 (changed, message, edit)：changed=false 表示已达目标(no-op)、edit=nil。
// 上层据此产出"完整而诚实"的语义 diff —— 每个声明的动作都出一行。
// 原语在内存树上挂合成节点(供跨步可见 + 落盘渲染)，并产出字节编辑记录。
package ops

import (
	"fmt"

	"addex/internal/xmldoc"
)

// Result 是一个原语的执行结果。
type Result struct {
	Changed bool
	Message string
	Edit    *xmldoc.Edit // no-op 时为 nil
}

// AddNode 确保 parent 下存在 <tag class=cls ...>；按 tag 判重(幂等)。
// attrs 为有序键值对，值已由上层做过占位符替换。
func AddNode(parent *xmldoc.Node, tag, cls string, attrs []xmldoc.Attr) Result {
	if xmldoc.FindChild(parent, tag) != nil {
		return Result{false, fmt.Sprintf("对象 <%s> 已存在", tag), nil}
	}
	el := xmldoc.NewElement(tag)
	el.Attrs = append([]xmldoc.Attr{{Name: "class", Value: cls}}, attrs...)
	xmldoc.AppendChild(parent, el)
	return Result{true, fmt.Sprintf("新增对象 <%s class=%s>", tag, cls),
		&xmldoc.Edit{Kind: xmldoc.Insert, Parent: parent, Child: el}}
}

// AddEntityRef 确保 parent 内存在实体引用 &name;；已存在即幂等。
func AddEntityRef(parent *xmldoc.Node, name string) Result {
	if xmldoc.HasEntity(parent, name) {
		return Result{false, fmt.Sprintf("实体引用 &%s; 已存在", name), nil}
	}
	ent := xmldoc.NewEntity(name)
	xmldoc.AppendChild(parent, ent)
	return Result{true, fmt.Sprintf("新增实体引用 &%s;", name),
		&xmldoc.Edit{Kind: xmldoc.Insert, Parent: parent, Child: ent}}
}

// AddMethod 确保 anchor 下存在 <name type="method">value</name>。
// hasValue=false 为标志型方法(按名判重、序列化为自闭合)；true 为带值方法(按名+值判重)。
func AddMethod(anchor *xmldoc.Node, name string, value string, hasValue bool) Result {
	if !hasValue {
		if xmldoc.FindChild(anchor, name) != nil {
			return Result{false, fmt.Sprintf("方法 %s() 已存在", name), nil}
		}
	} else {
		for _, el := range xmldoc.FindChildren(anchor, name) {
			if el.Text == value {
				return Result{false, fmt.Sprintf("方法 %s(%s) 已存在", name, value), nil}
			}
		}
	}
	el := xmldoc.NewElement(name)
	el.Attrs = []xmldoc.Attr{{Name: "type", Value: "method"}}
	if hasValue {
		el.Text = value
	}
	xmldoc.AppendChild(anchor, el)
	msg := fmt.Sprintf("新增方法 %s()", name)
	if hasValue {
		msg = fmt.Sprintf("新增方法 %s(%s)", name, value)
	}
	return Result{true, msg, &xmldoc.Edit{Kind: xmldoc.Insert, Parent: anchor, Child: el}}
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
		return Result{true, fmt.Sprintf("删除方法 %s(%s)", name, value),
			&xmldoc.Edit{Kind: xmldoc.Delete, Targets: hits}}
	}
	return Result{false, fmt.Sprintf("方法 %s(%s) 不存在，无需删除", name, value), nil}
}
