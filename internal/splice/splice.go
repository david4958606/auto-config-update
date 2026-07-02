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
			anchorOff := e.Parent.CloseStart
			if e.Before != nil && e.Before.Start >= 0 {
				anchorOff = e.Before.Start
			}
			at := insertOffset(src, anchorOff)
			block := xmldoc.Render(e.Child, xmldoc.RealDepth(e.Parent)+1) + "\n"
			if _, ok := insertText[at]; !ok {
				insertOrder = append(insertOrder, at)
			}
			insertText[at] += block
		case xmldoc.Delete:
			for _, t := range e.Targets {
				s, end := lineSpan(src, t.Start, t.End)
				patches = append(patches, patch{start: s, end: end})
			}
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
