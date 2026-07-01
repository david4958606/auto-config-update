// Package config —— 配置的解析/寻址层(Python model.py 的对应)。
//
// master(Control_config.xml / IO_config.xml)用 XML 外部实体把一批"无扩展名片段"
// 拼成逻辑 <Control>/<IO> 树。本层职责：
//   - 从 master 的 <!ENTITY> 声明拿到片段清单与实体声明(真相源，不靠 glob)。
//   - 构造只读逻辑索引，解析 /IO/... 与 /Control/... 逻辑路径到真实节点(带来源文件)。
//   - 按 glob 从"实际声明的实体名"里解析真名(Simulated_Ch1 / SimulatedFlag_Ch1)。
//
// 片段可含多个顶层元素(如 IO_Motor 同时有 <Ch1> 和 <Ch4>)，且内部可再引用共享实体
// (如 Control_Ch1 里 36 处 &Simulated_Ch1;)——xmldoc 已按实体容忍解析。
package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"addex/internal/xmldoc"
)

// Frag 是 master 正文按序引用的一个片段。
type Frag struct {
	Name string // 实体名(如 Control_Ch1)
	Path string // 片段文件绝对/相对路径
}

var entityDecl = regexp.MustCompile(`<!ENTITY\s+(\S+)\s+SYSTEM\s+"([^"]*)"\s*>`)

// entityURLs 返回 master DTD 里声明的 name->system_url。
func entityURLs(masterPath string) (map[string]string, []string, error) {
	src, err := os.ReadFile(masterPath)
	if err != nil {
		return nil, nil, err
	}
	urls := map[string]string{}
	var order []string
	for _, m := range entityDecl.FindAllStringSubmatch(string(src), -1) {
		urls[m[1]] = m[2]
		order = append(order, m[1])
	}
	return urls, order, nil
}

// DeclaredEntities 返回 master 里声明的全部实体名(声明顺序)。
func DeclaredEntities(masterPath string) ([]string, error) {
	_, order, err := entityURLs(masterPath)
	return order, err
}

// FragmentFiles 返回 master【正文】按顺序引用的片段 [(entity_name, path)]。
// 只算正文里 &X; 用到的实体；仅被片段内部引用的共享实体(如 Simulated_Ch1)不在此列。
func FragmentFiles(masterPath string) ([]Frag, error) {
	urls, _, err := entityURLs(masterPath)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(masterPath)
	if err != nil {
		return nil, err
	}
	doc, err := xmldoc.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("解析 master %s: %w", masterPath, err)
	}
	base := filepath.Dir(masterPath)
	var out []Frag
	var walk func(nodes []*xmldoc.Node)
	walk = func(nodes []*xmldoc.Node) {
		for _, n := range nodes {
			if n.IsEntity {
				if url, ok := urls[n.EntName]; ok {
					out = append(out, Frag{Name: n.EntName, Path: filepath.Clean(filepath.Join(base, url))})
				}
				continue
			}
			walk(n.Children)
		}
	}
	walk(doc.Roots)
	return out, nil
}

// Section 是逻辑索引里的一段：来源文件 + 顶层元素。
type Section struct {
	Path string
	Root *xmldoc.Node
}

// Indexes 是只读逻辑索引：键 = 逻辑根名(IO/Control)。
type Indexes map[string][]Section

// loadSections 按 master 片段清单加载所有片段，展开成 [(path, section)]。
func loadSections(masterPath string) ([]Section, error) {
	frags, err := FragmentFiles(masterPath)
	if err != nil {
		return nil, err
	}
	var out []Section
	for _, f := range frags {
		src, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, err
		}
		doc, err := xmldoc.Parse(src)
		if err != nil {
			return nil, fmt.Errorf("解析片段 %s: %w", f.Path, err)
		}
		for _, r := range doc.Roots {
			out = append(out, Section{Path: f.Path, Root: r})
		}
	}
	return out, nil
}

// BuildIndexes 构造 {IO:..., Control:...} 只读逻辑索引。
func BuildIndexes(ioMaster, controlMaster string) (Indexes, error) {
	io, err := loadSections(ioMaster)
	if err != nil {
		return nil, err
	}
	ctrl, err := loadSections(controlMaster)
	if err != nil {
		return nil, err
	}
	return Indexes{"IO": io, "Control": ctrl}, nil
}

// ResolveLogical 把 '/IO/Ch4/Ped/CurPosDI' 解析成 (来源文件, 节点)。
// 按首段选索引，其余段在"腔室标签匹配"的片段里逐个 find；命中即返回，否则 ("", nil)。
func ResolveLogical(idx Indexes, logical string) (string, *xmldoc.Node) {
	parts := splitPath(logical)
	if len(parts) < 2 {
		return "", nil
	}
	rootName, chamber, rest := parts[0], parts[1], parts[2:]
	for _, sec := range idx[rootName] {
		if sec.Root.Tag != chamber {
			continue
		}
		node := sec.Root
		ok := true
		for _, seg := range rest {
			node = xmldoc.FindChild(node, seg)
			if node == nil {
				ok = false
				break
			}
		}
		if ok && node != nil {
			return sec.Path, node
		}
	}
	return "", nil
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ResolveEntityName 按 glob 从声明的实体名里解析真名。
// 无匹配 → ""；多匹配 → 优先该腔室已用到的，否则取最短名(与 Python 一致)。
func ResolveEntityName(declared []string, pattern string, chamberRoot *xmldoc.Node) string {
	var matches []string
	for _, name := range declared {
		if ok, _ := path.Match(pattern, name); ok {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	if len(matches) > 1 && chamberRoot != nil {
		used := usedEntities(chamberRoot)
		var preferred []string
		for _, m := range matches {
			if used[m] {
				preferred = append(preferred, m)
			}
		}
		if len(preferred) > 0 {
			return shortest(preferred)
		}
	}
	return shortest(matches)
}

// usedEntities 收集腔室子树里用到的实体名。
func usedEntities(root *xmldoc.Node) map[string]bool {
	used := map[string]bool{}
	var walk func(n *xmldoc.Node)
	walk = func(n *xmldoc.Node) {
		if n.IsEntity {
			used[n.EntName] = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return used
}

// shortest 返回最短名(等长取先出现者，稳定)。
func shortest(names []string) string {
	cp := append([]string(nil), names...)
	sort.SliceStable(cp, func(i, j int) bool { return len(cp[i]) < len(cp[j]) })
	return cp[0]
}
