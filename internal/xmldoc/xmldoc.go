// Package xmldoc 解析设备配置片段为一棵【带精确字节区间】的轻量可变节点树。
//
// 这是 Go 版的地基(计划 §2.1/§2.2)。相比 Python 用 lxml + sourceline(按行)，
// 这里走"路线 B"：自写极简分词器，直接产出带字节偏移的节点，天然做到：
//   - 实体容忍：&Simulated_Ch1; 这类外部实体作为不透明 Entity 节点，无需 DTD、不报错。
//   - 外科式写盘的根基：每个原节点带 [Start,End) 全区间 + 闭合标签起点 CloseStart。
//   - 跨步可变树：可挂合成节点(Synthetic)、标记删除(Removed)，落盘时只翻译成字节编辑。
//
// 假设(设备 XML 均满足)：结构良好、实体写作 &x;、属性值内不含裸 '<' 或 '>'。
// CDATA 段(<![CDATA[...]]>)按整段忽略处理——配置里存在用它包内容当注释的写法。
package xmldoc

import (
	"bytes"
	"fmt"
	"strings"
)

// Attr 是一个属性，保留书写顺序。
//
// ValueStart/ValueEnd 记录该属性【值内容】在源文件里的字节区间(不含引号)；合成属性为 -1。
// 供 set-attr 做原位替换；值里含转义实体时 Value 是解析后的文本(本工具的目标值均为普通文本)。
type Attr struct {
	Name       string
	Value      string
	ValueStart int
	ValueEnd   int
}

// Node 是树里的一个节点：可能是元素、也可能是实体引用(IsEntity)。
//
// 原节点带真实字节区间；本次新增的合成节点 Start/End/CloseStart 均为 -1。
type Node struct {
	Tag      string
	Attrs    []Attr
	Children []*Node
	Parent   *Node

	// Text 是【首个子元素之前】的字符数据(等价 lxml 的 .text)。
	// 对方法叶子节点(<setPgValve ...>值</setPgValve>)即其值，供幂等判重。
	Text string

	// InnerText 是该元素内部**全部**字符数据的顺序拼接(含实体引用之后的文本)。
	// 配置里大量出现 "A &amp;&amp; B" 这类混合内容，Text 只留首段；InnerText 用于比较/判重。
	InnerText string

	IsEntity bool   // true 表示这是 &EntName; 这类实体引用节点
	EntName  string // 实体名(去掉 & 与 ;)

	// 注释 / 空行合成节点(仅由 add-comment / add-blank 产出，解析器不建这类节点)。
	IsComment  bool   // true → 注释节点：Raw 存完整 <!--...-->，落盘时原样输出
	IsBlank    bool   // true → 空行节点：渲染为空行(无缩进)
	Raw        string // IsComment 时的完整注释原文(含 <!-- -->)
	BlankCount int    // IsBlank 时的空行行数(>=1)

	Synthetic bool // 本次新增(无字节区间)
	Removed   bool // 标记删除(落盘时删掉其字节区间)
	SelfClose bool // 原文是自闭合 <tag .../>

	// PairedEmpty 为 true 时，空文本节点渲染成成对标签 <tag></tag> 而非自闭合 <tag/>。
	// 仅作用于合成节点(如 add-io 的空 <Unit>/<Min>/<Max>)，与 IG 片段既有写法保持一致。
	PairedEmpty bool

	Start      int // 元素/实体在源文件的起始字节(指向 '<' 或 '&')；合成节点 = -1
	End        int // 结束字节(其后第一个字节，半开区间)；合成节点 = -1
	OpenEnd    int // 开标签 '>' 之后第一个字节；实体/合成 = -1
	CloseStart int // '</tag>' 中 '<' 的字节偏移；自闭合/实体/合成 = -1
}

// Document 是一次解析的结果：源字节 + 顶层元素(片段可含多个，如 IO_Motor 有 Ch1/Ch4)。
type Document struct {
	Src   []byte
	Roots []*Node
}

// Attr 返回属性值；不存在返回 ""。
func (n *Node) Attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value
		}
	}
	return ""
}

// HasAttr 报告是否存在名为 name 的属性(与取值为空串区分开)。
func (n *Node) HasAttr(name string) bool {
	for _, a := range n.Attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// Class 是 class 属性的快捷取值。
func (n *Node) Class() string { return n.Attr("class") }

// Raw 返回该原节点在源文件里的原始字节(合成节点返回 "")。
func (d *Document) Raw(n *Node) string {
	if n.Start < 0 || n.End < 0 {
		return ""
	}
	return string(d.Src[n.Start:n.End])
}

// Parse 把片段字节解析成 Document。
func Parse(src []byte) (*Document, error) {
	p := &parser{src: src}
	doc := &Node{} // 虚拟容器，收集顶层元素
	stack := []*Node{doc}
	top := func() *Node { return stack[len(stack)-1] }

	for p.pos < len(src) {
		c := src[p.pos]
		switch {
		case c == '<':
			switch {
			case p.has("<!--"):
				if err := p.skipUntil("-->"); err != nil {
					return nil, err
				}
			case p.has("<![CDATA["):
				// CDATA 段整体跳过：配置里有用 <![CDATA[...]]> 包住内容当注释的写法。
				// 按 "]]>" 精确结束，避免内容里的 '>' 或 ']>' 造成误截断。
				if err := p.skipUntil("]]>"); err != nil {
					return nil, err
				}
			case p.has("<?"):
				if err := p.skipUntil("?>"); err != nil {
					return nil, err
				}
			case p.has("<!"):
				if err := p.skipDirective(); err != nil {
					return nil, err
				}
			case p.has("</"):
				name, end, err := p.readEndTag()
				if err != nil {
					return nil, err
				}
				parent := top()
				if parent == doc {
					return nil, fmt.Errorf("字节 %d: 多余的结束标签 </%s>", p.pos, name)
				}
				if parent.Tag != name {
					return nil, fmt.Errorf("字节 %d: 结束标签 </%s> 与 <%s> 不匹配", p.pos, name, parent.Tag)
				}
				parent.CloseStart = p.pos
				parent.End = end
				stack = stack[:len(stack)-1]
				p.pos = end
			default:
				node, err := p.readStartTag()
				if err != nil {
					return nil, err
				}
				node.Parent = top()
				top().Children = append(top().Children, node)
				if !node.SelfClose {
					stack = append(stack, node)
				}
			}
		case c == '&':
			node, err := p.readEntity()
			if err != nil {
				return nil, err
			}
			node.Parent = top()
			top().Children = append(top().Children, node)
		default:
			// CharData：不建节点(缩进由落盘时按深度算)，但把【首子元素之前】的
			// 文本记到当前元素的 Text 上(等价 lxml .text)，供方法节点幂等判重。
			start := p.pos
			p.skipText()
			if t := top(); t != doc {
				chunk := string(src[start:p.pos])
				if len(t.Children) == 0 {
					t.Text += chunk
				}
				t.InnerText += chunk
			}
		}
	}

	if len(stack) != 1 {
		return nil, fmt.Errorf("到文件尾仍有未闭合标签 <%s>", top().Tag)
	}
	for _, r := range doc.Children {
		r.Parent = nil
	}
	return &Document{Src: src, Roots: doc.Children}, nil
}

type parser struct {
	src []byte
	pos int
}

func (p *parser) has(s string) bool {
	return bytes.HasPrefix(p.src[p.pos:], []byte(s))
}

// skipUntil 把 pos 推进到 delim 之后。
func (p *parser) skipUntil(delim string) error {
	i := bytes.Index(p.src[p.pos:], []byte(delim))
	if i < 0 {
		return fmt.Errorf("字节 %d: 找不到结束符 %q", p.pos, delim)
	}
	p.pos += i + len(delim)
	return nil
}

// skipDirective 跳过 <!DOCTYPE ...> 等指令；含内部子集 [...] 时跳到 "]>"。
func (p *parser) skipDirective() error {
	// 片段本身通常没有指令；稳妥处理 DOCTYPE 的内部子集。
	rest := p.src[p.pos:]
	if lb := bytes.IndexByte(rest, '['); lb >= 0 {
		if gt := bytes.IndexByte(rest, '>'); gt >= 0 && gt < lb {
			return p.skipUntil(">")
		}
		return p.skipUntil("]>")
	}
	return p.skipUntil(">")
}

// skipText 推进过一段 CharData。
func (p *parser) skipText() {
	for p.pos < len(p.src) && p.src[p.pos] != '<' && p.src[p.pos] != '&' {
		p.pos++
	}
}

// readEntity 读 &name; 返回实体节点(Start 指向 '&'，End 指向 ';' 之后)。
func (p *parser) readEntity() (*Node, error) {
	start := p.pos
	i := bytes.IndexByte(p.src[p.pos:], ';')
	if i < 0 {
		return nil, fmt.Errorf("字节 %d: 实体引用缺少 ';'", start)
	}
	name := string(p.src[p.pos+1 : p.pos+i])
	end := p.pos + i + 1
	p.pos = end
	return &Node{
		IsEntity: true, EntName: name,
		Start: start, End: end, OpenEnd: -1, CloseStart: -1,
	}, nil
}

// readEndTag 读 </name> 返回 (name, endOffset)。调用时 pos 指向 '<'。
func (p *parser) readEndTag() (string, int, error) {
	start := p.pos
	i := bytes.IndexByte(p.src[p.pos:], '>')
	if i < 0 {
		return "", 0, fmt.Errorf("字节 %d: 结束标签缺少 '>'", start)
	}
	name := strings.TrimSpace(string(p.src[p.pos+2 : p.pos+i]))
	return name, p.pos + i + 1, nil
}

// readStartTag 读 <tag attr="v" ...> 或 <tag .../>。调用时 pos 指向 '<'，返回后 pos 指向 '>' 之后。
func (p *parser) readStartTag() (*Node, error) {
	start := p.pos
	p.pos++ // 跳过 '<'
	tag := p.readName()
	if tag == "" {
		return nil, fmt.Errorf("字节 %d: 起始标签缺少名字", start)
	}
	node := &Node{Tag: tag, Start: start, CloseStart: -1}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("字节 %d: 标签 <%s 未闭合", start, tag)
		}
		switch p.src[p.pos] {
		case '>':
			p.pos++
			node.End = p.pos // 非自闭合：End 暂设到 '>' 后，遇 </tag> 再更新
			node.OpenEnd = p.pos
			return node, nil
		case '/':
			if p.has("/>") {
				p.pos += 2
				node.SelfClose = true
				node.End = p.pos
				node.OpenEnd = p.pos
				return node, nil
			}
			return nil, fmt.Errorf("字节 %d: 标签 <%s 里出现意外的 '/'", p.pos, tag)
		default:
			name := p.readName()
			if name == "" {
				return nil, fmt.Errorf("字节 %d: 标签 <%s 属性名非法", p.pos, tag)
			}
			p.skipSpace()
			val := ""
			vstart, vend := -1, -1
			if p.pos < len(p.src) && p.src[p.pos] == '=' {
				p.pos++
				p.skipSpace()
				vs, ve, err := p.readQuotedSpan()
				if err != nil {
					return nil, err
				}
				val, vstart, vend = string(p.src[vs:ve]), vs, ve
			}
			node.Attrs = append(node.Attrs, Attr{Name: name, Value: val, ValueStart: vstart, ValueEnd: vend})
		}
	}
}

// readName 读一个 XML 名字(标签名/属性名)。
func (p *parser) readName() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '>' || c == '/' || c == '=' {
			break
		}
		p.pos++
	}
	return string(p.src[start:p.pos])
}

// readQuotedSpan 读带引号的属性值，返回引号内内容的字节区间 [start,end)。
func (p *parser) readQuotedSpan() (int, int, error) {
	if p.pos >= len(p.src) || (p.src[p.pos] != '"' && p.src[p.pos] != '\'') {
		return 0, 0, fmt.Errorf("字节 %d: 属性值缺少引号", p.pos)
	}
	q := p.src[p.pos]
	p.pos++
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != q {
		p.pos++
	}
	if p.pos >= len(p.src) {
		return 0, 0, fmt.Errorf("字节 %d: 属性值引号未闭合", start)
	}
	end := p.pos
	p.pos++ // 跳过右引号
	return start, end, nil
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		p.pos++
	}
}
