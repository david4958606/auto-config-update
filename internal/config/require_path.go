package config

import (
	"strings"

	"addex/internal/xmldoc"
)

// ResolveRequirePath 在具体标签路径之外，允许尚未替换的 {Class} 段按 class 查找。
// 标签段只匹配结构的一层；class 段可跨包装节点。返回首条完整命中的实际逻辑路径。
// 不使用 anchor.Resolve：require 的裸名段必须保持纯标签语义。
func ResolveRequirePath(idx Indexes, logical string) (string, *xmldoc.Node, string) {
	parts := splitPath(logical)
	if len(parts) < 2 {
		return "", nil, logical
	}
	isClass := func(seg string) bool {
		return len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && !strings.ContainsAny(seg[1:len(seg)-1], "{}$")
	}
	hasClass := false
	for _, seg := range parts[1:] {
		hasClass = hasClass || isClass(seg)
		// 未绑定的 ${X} 不得被解释成 class，也不得匹配字面标签。
		if strings.ContainsAny(seg, "{}") && !isClass(seg) {
			return "", nil, logical
		}
	}
	if !hasClass {
		path, node := ResolveLogical(idx, logical)
		return path, node, logical
	}

	type candidate struct {
		node *xmldoc.Node
		path string
	}
	valid := func(n *xmldoc.Node) bool { return n != nil && !n.Removed && !n.IsEntity }
	matches := func(n *xmldoc.Node, seg string) bool {
		if !valid(n) {
			return false
		}
		if isClass(seg) {
			return n.Class() == seg[1:len(seg)-1]
		}
		return n.Tag == seg
	}
	// 收集所有候选后再继续剩余路径，避免第一个同类实例缺少 leaf 时漏掉后续实例。
	var children func(candidate, string) []candidate
	children = func(parent candidate, seg string) []candidate {
		var out []candidate
		for _, n := range parent.node.Children {
			if !valid(n) {
				continue
			}
			c := candidate{node: n, path: parent.path + "/" + n.Tag}
			if matches(n, seg) {
				out = append(out, c)
			}
			if isClass(seg) {
				out = append(out, children(c, seg)...)
			}
		}
		return out
	}
	for _, master := range []bool{false, true} {
		for _, sec := range idx[parts[0]] {
			if sec.Master != master || !valid(sec.Root) {
				continue
			}
			var frontier []candidate
			if master {
				// 首段只匹配顶层对象，不能向其他腔室内部递归找同名 class。
				for _, n := range sec.Root.Children {
					if matches(n, parts[1]) {
						frontier = append(frontier, candidate{n, "/" + parts[0] + "/" + n.Tag})
					}
				}
			} else if matches(sec.Root, parts[1]) {
				frontier = append(frontier, candidate{sec.Root, "/" + parts[0] + "/" + sec.Root.Tag})
			}
			for _, seg := range parts[2:] {
				var next []candidate
				for _, c := range frontier {
					next = append(next, children(c, seg)...)
				}
				frontier = next
				if len(frontier) == 0 {
					break
				}
			}
			if len(frontier) > 0 {
				return sec.Path, frontier[0].node, frontier[0].path
			}
		}
	}
	return "", nil, logical
}
