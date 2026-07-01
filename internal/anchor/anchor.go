// Package anchor —— 靠 class 路径定位节点，支持"同类多实例"fan-out。
//
//   - anchor 是一条 class 路径(如 CVD/PhyChuck)，逐层 descendant 匹配。
//   - leaf 命中同一 class 的多个实例则【全部返回】，由上层各执行一次。
//   - 每个匹配带一份 tags：{class名: 该实例的标签名}，供 {占位符} 逐实例替换。
//   - where(可选)：leaf 命中多个时按标签精确筛(glob)。
package anchor

import (
	"path"

	"addex/internal/xmldoc"
)

// Where 是 leaf 谓词。
type Where struct {
	TagGlob string            // 标签名 glob(如 "Ped*")
	Attr    map[string]string // 属性全等
	HasGlob bool              // 是否设置了 tag-glob
}

// Match 是一个命中：锚点节点 + 路径上各层的标签名。
type Match struct {
	Node *xmldoc.Node
	Tags map[string]string
}

// findByClass 返回 node 子树里 class==cls 的元素。includeSelf 决定是否含 node 本身。
func findByClass(node *xmldoc.Node, cls string, includeSelf bool) []*xmldoc.Node {
	var out []*xmldoc.Node
	var walk func(n *xmldoc.Node, self bool)
	walk = func(n *xmldoc.Node, self bool) {
		if n.Removed {
			return
		}
		if self && !n.IsEntity && n.Class() == cls {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c, true)
		}
	}
	walk(node, includeSelf)
	return out
}

// Resolve 返回全部匹配。leaf 多实例 → 多条；无匹配 → 空。
func Resolve(root *xmldoc.Node, classPath []string, where *Where) []Match {
	if len(classPath) == 0 {
		return nil
	}
	// 第一层：从根(含自身)找该 class。
	var frontier []Match
	for _, n := range findByClass(root, classPath[0], true) {
		frontier = append(frontier, Match{Node: n, Tags: map[string]string{classPath[0]: n.Tag}})
	}
	// 其余层：在上一层节点的子孙里继续找(逐层可各自 fan-out)。
	for _, cls := range classPath[1:] {
		var next []Match
		for _, m := range frontier {
			for _, child := range findByClass(m.Node, cls, false) {
				t := make(map[string]string, len(m.Tags)+1)
				for k, v := range m.Tags {
					t[k] = v
				}
				t[cls] = child.Tag
				next = append(next, Match{Node: child, Tags: t})
			}
		}
		frontier = next
	}
	// where 只筛 leaf。
	var out []Match
	for _, m := range frontier {
		if matchWhere(m.Node, where) {
			out = append(out, m)
		}
	}
	return out
}

func matchWhere(n *xmldoc.Node, w *Where) bool {
	if w == nil {
		return true
	}
	if w.HasGlob {
		if ok, _ := path.Match(w.TagGlob, n.Tag); !ok {
			return false
		}
	}
	for k, v := range w.Attr {
		if n.Attr(k) != v {
			return false
		}
	}
	return true
}
