// Package splice —— 外科式字节拼接写盘。
//
// 不重排整棵树，只在插入点/删除点改动原文，其余字节逐字保留 → 真正 clean diff。
// 相比 Python 的按行 splice，这里用 xmldoc 给出的精确字节偏移：
//   - 插入：新子节点渲染成文本，插到其父节点闭合标签(CloseStart)之前；
//     父本身是合成节点则跳过(其文本已含在祖先渲染里)；父是片段文件伪根
//     (VirtualRoot)时，改为在文件里新增一个顶层元素(追加到最后一个顶层元素之后)。
//   - 删除：删掉原节点所在的整行(含缩进与行尾换行)，与 Python 按行删除一致。
package splice

import (
	"sort"
	"strings"

	"addex/internal/xmldoc"
)

type patch struct {
	start, end int    // 替换区间 [start,end)
	text       string // 替换为(插入时 end==start)
}

// Apply 把编辑应用到 src，返回新字节。edits 的插入按加入顺序合并到同一父节点。
func Apply(src []byte, edits []xmldoc.Edit) []byte {
	// 按"父节点闭合标签偏移"聚合插入，保序拼接每个父的新子节点块。
	insertText := map[int]string{}
	var insertOrder []int
	// 自闭合父没有 CloseStart；只展开原开标签末尾的 />，避免重写属性，
	// 也让同批属性值 Replace 的原始偏移仍然有效。
	selfClosing := map[int]*xmldoc.Node{}
	nl := detectNewline(src)
	var patches []patch

	for _, e := range edits {
		switch e.Kind {
		case xmldoc.Insert, xmldoc.InsertRaw:
			if e.Parent.Synthetic && !e.Parent.VirtualRoot {
				continue // 父是合成节点，文本已在祖先渲染里
			}
			if e.First && !e.Parent.VirtualRoot && !e.Parent.SelfClose {
				// 首位置：插到父开标签 '>' 之后，成为第一个子节点(先于既有子节点与 &实体;)。
				// 新节点独占一行并沿用既有首个子节点的缩进；这里刻意不补行尾换行——原文紧跟的
				// 换行/空行保持不动，通常正好成为新节点与后继兄弟之间的分隔。
				var body string
				if e.Kind == xmldoc.Insert {
					indent, unit := insertionIndent(src, e.Parent, e.Before)
					body = xmldoc.RenderIndented(e.Child, indent, unit)
				} else {
					body = strings.TrimRight(e.Text, "\r\n")
				}
				at := e.Parent.OpenEnd
				if at < 0 || at > len(src) {
					continue
				}
				text := nl + matchNewline(body, nl)
				if at < len(src) && src[at] != '\n' && src[at] != '\r' {
					// 开标签后紧跟内容(如内联空元素)：补一个换行，避免新节点与后文挤在同一行。
					text += nl
				}
				if _, ok := insertText[at]; !ok {
					insertOrder = append(insertOrder, at)
				}
				insertText[at] += text
				continue
			}
			var block string
			if e.Kind == xmldoc.Insert {
				indent, unit := insertionIndent(src, e.Parent, e.Before)
				if e.Parent.VirtualRoot {
					indent, unit = docRootIndent(src, e.Parent, e.Before)
				}
				block = matchNewline(xmldoc.RenderIndented(e.Child, indent, unit)+"\n", nl)
			} else {
				block = matchNewline(strings.TrimRight(e.Text, "\r\n"), nl) + nl
			}
			if e.Parent.VirtualRoot {
				// 伪根没有闭合标签：新顶层元素插到 before 所在行之前；否则追加到最后一个
				// 顶层元素所在行之后(前置一个换行，与既有顶层元素之间保留空行分隔)。
				at, prefix := docRootInsertPos(src, e.Parent, e.Before, nl)
				if _, ok := insertText[at]; !ok {
					insertOrder = append(insertOrder, at)
				}
				insertText[at] += prefix + block
				continue
			}
			if e.Parent.SelfClose {
				// End/OpenEnd 都在 '>' 之后；自闭合父无原文子节点可作为 before。
				at := e.Parent.OpenEnd - 2
				if _, ok := insertText[at]; !ok {
					insertOrder = append(insertOrder, at)
				}
				selfClosing[at] = e.Parent
				insertText[at] += block
				continue
			}
			// 缺省插到父闭合标签前(追加)；指定 before 且其为原节点时，改插到该兄弟节点所在行之前。
			// 追加时若父末尾是"永远在最后"的实体引用(如 &Simulated_ChN;)，则插到该实体之前，
			// 与内存树 AppendChild 的书写惯例一致。
			anchorOff := e.Parent.CloseStart
			if e.Before != nil && e.Before.Start >= 0 {
				anchorOff = e.Before.Start
			} else if ent := trailingEntity(e.Parent); ent != nil {
				anchorOff = ent.Start
			}
			at := insertOffset(src, anchorOff)
			if _, ok := insertText[at]; !ok {
				insertOrder = append(insertOrder, at)
			}
			insertText[at] += block
		case xmldoc.Delete:
			for _, t := range e.Targets {
				s, end := lineSpan(src, t.Start, t.End)
				patches = append(patches, patch{start: s, end: end})
			}
		case xmldoc.SetText:
			for _, t := range e.Targets {
				patches = append(patches, setTextPatch(src, t, e.Text))
			}
		case xmldoc.Wrap:
			if lo, hi, ok := wrapSpan(e.Targets); ok {
				patches = append(patches, patch{start: lo, end: lo, text: e.Open})
				patches = append(patches, patch{start: hi, end: hi, text: e.Close})
			}
		case xmldoc.Replace:
			patches = append(patches, patch{start: e.Start, end: e.End, text: e.Text})
		}
	}
	for _, at := range insertOrder {
		if parent := selfClosing[at]; parent != nil {
			indent := strings.Repeat("    ", xmldoc.RealDepth(parent))
			// 独占一行时，闭标签复用原父节点的缩进(包括 tab)。
			if lineStart := insertOffset(src, parent.Start); lineStart < parent.Start {
				indent = string(src[lineStart:parent.Start])
			}
			patches = append(patches, patch{start: at, end: parent.OpenEnd,
				text: ">" + nl + insertText[at] + indent + "</" + parent.Tag + ">"})
			continue
		}
		patches = append(patches, patch{start: at, end: at, text: insertText[at]})
	}

	// 自底向上应用，保持偏移稳定。
	// 同一偏移处可能既有"删除/替换"(end>start) 又有"插入"(end==start)——例如把新节点插到
	// 一个即将被删除的兄弟之前。此时必须**先删后插**：若先插，随后按原偏移执行的删除会把
	// 刚插入的文本一并吃掉。故同一 start 时 end 大者先应用。
	sort.SliceStable(patches, func(i, j int) bool {
		if patches[i].start != patches[j].start {
			return patches[i].start > patches[j].start
		}
		return patches[i].end > patches[j].end
	})
	out := src
	for _, p := range patches {
		next := make([]byte, 0, len(out)-(p.end-p.start)+len(p.text))
		next = append(next, out[:p.start]...)
		next = append(next, p.text...)
		next = append(next, out[p.end:]...)
		out = next
	}
	return out
}

// lineIndent 只读取独占一行的原节点缩进，不把内联内容当作缩进。
func lineIndent(src []byte, n *xmldoc.Node) (string, bool) {
	if n == nil || n.Synthetic || n.Start < 0 || n.Start > len(src) {
		return "", false
	}
	at := insertOffset(src, n.Start)
	if at == n.Start && n.Start > 0 && src[n.Start-1] != '\n' {
		return "", false
	}
	return string(src[at:n.Start]), true
}

// insertionIndent 优先沿用插入点兄弟或既有子节点的缩进；空父节点沿祖先推断
// 每层缩进。无可用原文格式(如全内联文档)时保持原有的四空格回退。
func insertionIndent(src []byte, parent, before *xmldoc.Node) (string, string) {
	base, baseOK := lineIndent(src, parent)
	childIndent, childOK := lineIndent(src, before)
	if !childOK {
		for _, c := range parent.Children {
			if c.Removed {
				continue
			}
			if childIndent, childOK = lineIndent(src, c); childOK {
				break
			}
		}
	}
	unit := "    "
	if baseOK && childOK && strings.HasPrefix(childIndent, base) && len(childIndent) > len(base) {
		unit = childIndent[len(base):]
	} else {
		for n := parent; n != nil && n.Parent != nil; n = n.Parent {
			own, ownOK := lineIndent(src, n)
			outer, outerOK := lineIndent(src, n.Parent)
			if ownOK && outerOK && strings.HasPrefix(own, outer) && len(own) > len(outer) {
				unit = own[len(outer):]
				break
			}
		}
	}
	if childOK {
		return childIndent, unit
	}
	if baseOK {
		return base + unit, unit
	}
	return strings.Repeat(unit, xmldoc.RealDepth(parent)+1), unit
}

// docRootIndent 给出向"多根片段伪根"追加顶层元素时的缩进与每层缩进单位：
// 顶层元素通常顶格(缩进空串)，单位缩进沿用既有顶层元素与其首个子元素的差值。
func docRootIndent(src []byte, parent, before *xmldoc.Node) (string, string) {
	indent := ""
	if s, ok := lineIndent(src, before); ok {
		indent = s
	} else {
		for _, c := range parent.Children {
			if c.Removed || c.Synthetic {
				continue
			}
			if s, ok := lineIndent(src, c); ok {
				indent = s
				break
			}
		}
	}
	unit := "    "
	for _, c := range parent.Children {
		if c.Removed || c.Synthetic {
			continue
		}
		ci, ciOK := lineIndent(src, c)
		for _, gc := range c.Children {
			if gc.Removed || gc.Synthetic {
				continue
			}
			gi, giOK := lineIndent(src, gc)
			if ciOK && giOK && strings.HasPrefix(gi, ci) && len(gi) > len(ci) {
				unit = gi[len(ci):]
			}
			break
		}
		break
	}
	return indent, unit
}

// docRootInsertPos 返回新增顶层元素在片段文件里的写入偏移与需前置的文本：
// before 是原顶层元素时插到其所在行行首；否则追加到最后一个顶层元素所在行之后，
// 并前置一个换行，让新旧顶层元素之间保留一个空行(与片段既有写法一致)。
func docRootInsertPos(src []byte, parent, before *xmldoc.Node, nl string) (int, string) {
	if before != nil && !before.Synthetic && before.Start >= 0 {
		return insertOffset(src, before.Start), ""
	}
	var last *xmldoc.Node
	for _, c := range parent.Children {
		if c.Removed || c.Synthetic || c.Start < 0 || c.End < 0 {
			continue
		}
		last = c
	}
	if last == nil {
		at := len(src)
		if at > 0 && src[at-1] != '\n' {
			return at, nl
		}
		return at, ""
	}
	_, end := lineSpan(src, last.Start, last.End)
	return end, nl
}

// detectNewline 返回文件主导换行符(有 CRLF 就是 "\r\n"，否则 "\n")。
// 配置片段多为 CRLF；插入的新行须沿用，避免同文件混用换行。
func detectNewline(src []byte) string {
	if strings.Contains(string(src), "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// matchNewline 把 block 的换行统一成 nl(先归一到 \n 再展开，避免重复 \r)。
func matchNewline(block, nl string) string {
	block = strings.ReplaceAll(block, "\r\n", "\n")
	if nl == "\n" {
		return block
	}
	return strings.ReplaceAll(block, "\n", nl)
}

// setTextPatch 生成"把 t 的文本改成 text"的字节替换：
//   - 成对标签：只替换开标签之后 ~ 闭标签之前的 inner 区间(标签/属性/缩进原文不动)。
//   - 自闭合标签：整段重渲染成 <tag attrs>text</tag>。
func setTextPatch(src []byte, t *xmldoc.Node, text string) patch {
	if t.CloseStart >= 0 && t.OpenEnd >= 0 {
		return patch{start: t.OpenEnd, end: t.CloseStart, text: xmldoc.EscapeText(text)}
	}
	open := strings.TrimRight(string(src[t.Start:t.End]), " \t\r\n")
	open = strings.TrimSuffix(open, "/")
	return patch{start: t.Start, end: t.End, text: open + ">" + xmldoc.EscapeText(text) + "</" + t.Tag + ">"}
}

// wrapSpan 返回一组节点覆盖的最小字节区间 [min(Start), max(End))。
func wrapSpan(targets []*xmldoc.Node) (int, int, bool) {
	lo, hi := -1, -1
	for _, t := range targets {
		if t == nil || t.Start < 0 || t.End < 0 {
			continue
		}
		if lo < 0 || t.Start < lo {
			lo = t.Start
		}
		if t.End > hi {
			hi = t.End
		}
	}
	return lo, hi, lo >= 0 && hi > lo
}

// trailingEntity 返回父节点末尾连续"永远在最后"的原文实体引用(Start>=0)中最靠前的一个；
// 无尾部实体则返回 nil。合成插入的新子节点被 AppendChild 放在实体之前，故实体仍在末位；
// 从末尾向前扫，遇到首个非实体子节点即停，取到的最靠前实体即新节点应插入的位置。
func trailingEntity(parent *xmldoc.Node) *xmldoc.Node {
	var first *xmldoc.Node
	for i := len(parent.Children) - 1; i >= 0; i-- {
		c := parent.Children[i]
		if c.Removed {
			continue
		}
		if c.IsEntity && c.Start >= 0 {
			first = c
			continue
		}
		break
	}
	return first
}

// insertOffset 给出定位点 anchorOff(父闭合标签起点，或某兄弟节点起点)之前的插入点。
// 若 anchorOff 独占一行(其前只有缩进空白)，则把插入点上移到该行行首，让原缩进留给该行、
// 新子节点各自成行且缩进正确；否则(与内容同行，如内联元素)退回到 anchorOff 原位插入。
func insertOffset(src []byte, anchorOff int) int {
	ls := 0
	if i := strings.LastIndexByte(string(src[:anchorOff]), '\n'); i >= 0 {
		ls = i + 1
	}
	for _, b := range src[ls:anchorOff] {
		if b != ' ' && b != '\t' {
			return anchorOff // 同行有内容 → 不上移
		}
	}
	return ls
}

// lineSpan 把 [start,end) 扩展到"整行"：左到上一个换行之后，右到覆盖该节点的行尾换行之后。
func lineSpan(src []byte, start, end int) (int, int) {
	s := start
	if i := strings.LastIndexByte(string(src[:start]), '\n'); i >= 0 {
		s = i + 1
	} else {
		s = 0
	}
	e := end
	if i := strings.IndexByte(string(src[end:]), '\n'); i >= 0 {
		e = end + i + 1
	} else {
		e = len(src)
	}
	return s, e
}
