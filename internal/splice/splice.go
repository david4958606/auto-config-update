// Package splice —— 外科式字节拼接写盘。
//
// 不重排整棵树，只在插入点/删除点改动原文，其余字节逐字保留 → 真正 clean diff。
// 相比 Python 的按行 splice，这里用 xmldoc 给出的精确字节偏移：
//   - 插入：新子节点渲染成文本，插到其父节点闭合标签(CloseStart)之前；
//     父本身是合成节点则跳过(其文本已含在祖先渲染里)。
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
	var patches []patch

	for _, e := range edits {
		switch e.Kind {
		case xmldoc.Insert:
			if e.Parent.Synthetic {
				continue // 父是合成节点，文本已在祖先渲染里
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
			block := xmldoc.Render(e.Child, xmldoc.RealDepth(e.Parent)+1) + "\n"
			block = matchNewline(block, detectNewline(src))
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
		case xmldoc.InsertRaw:
			if e.Parent.Synthetic {
				continue
			}
			anchorOff := e.Parent.CloseStart
			if e.Before != nil && e.Before.Start >= 0 {
				anchorOff = e.Before.Start
			} else if ent := trailingEntity(e.Parent); ent != nil {
				anchorOff = ent.Start
			}
			at := insertOffset(src, anchorOff)
			block := matchNewline(strings.TrimRight(e.Text, "\r\n"), detectNewline(src)) + detectNewline(src)
			if _, ok := insertText[at]; !ok {
				insertOrder = append(insertOrder, at)
			}
			insertText[at] += block
		case xmldoc.Replace:
			patches = append(patches, patch{start: e.Start, end: e.End, text: e.Text})
		}
	}
	for _, at := range insertOrder {
		patches = append(patches, patch{start: at, end: at, text: insertText[at]})
	}

	// 自底向上应用，保持偏移稳定。
	sort.Slice(patches, func(i, j int) bool { return patches[i].start > patches[j].start })
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
	if nl == "\n" {
		return block
	}
	return strings.ReplaceAll(strings.ReplaceAll(block, "\r\n", "\n"), "\n", nl)
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
