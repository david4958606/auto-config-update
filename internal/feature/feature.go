// Package feature —— 功能 YAML 加载 + 变量绑定(require / bind)。
//
// 变量作用域：
//   - {类名} 由 anchor 逐实例自动绑定(在 engine 完成)。
//   - add-node 建对象后把 {标签} 绑成 "./标签"，供后续步骤跨步引用(engine 完成)。
//   - require: 守卫+绑定。exist —— 逻辑路径必须解析得到，否则跳过；命中则绑定为该路径。
//   - bind:    纯绑定，不做存在性要求。
package feature

import (
	"fmt"
	"os"
	"strings"

	"addex/internal/config"
	"addex/internal/xmldoc"
	"gopkg.in/yaml.v3"
)

// Feature 是一个功能定义。
type Feature struct {
	ID          string    `yaml:"id"`
	Description string    `yaml:"description"`
	Version     yaml.Node `yaml:"version"` // 保留原样标量(1 / "1.0" 都照打)
	Steps       []Step    `yaml:"steps"`
}

// Ver 返回 version 的原始文本(用于打印 v{version})。
func (f *Feature) Ver() string { return f.Version.Value }

// Step 是一个有序步骤。
type Step struct {
	Name         string        `yaml:"name"`
	Anchor       string        `yaml:"anchor"`
	Where        *WhereSpec    `yaml:"where"`
	Require      []yaml.Node   `yaml:"require"` // 单键 map 列表
	Bind         []yaml.Node   `yaml:"bind"`    // 单键 map 列表
	AddNode      []AddNodeSpec `yaml:"add-node"`
	AddMethod    []MethodSpec  `yaml:"add-method"`
	RemoveMethod []MethodSpec  `yaml:"remove-method"`
}

// WhereSpec 是 leaf 谓词。
type WhereSpec struct {
	TagGlob string            `yaml:"tag-glob"`
	Attr    map[string]string `yaml:"attr"`
}

// AddNodeSpec 描述一个 add-node 动作。
type AddNodeSpec struct {
	Tag           string    `yaml:"tag"`
	Class         string    `yaml:"class"`
	Attrs         yaml.Node `yaml:"attrs"` // 保留书写顺序
	IncludeEntity string    `yaml:"include-entity"`
}

// AttrPairs 按书写顺序返回 attrs 的键值对。
func (a *AddNodeSpec) AttrPairs() []xmldoc.Attr {
	return mapPairs(&a.Attrs)
}

// MethodSpec 描述一个 add-method / remove-method 动作。
type MethodSpec struct {
	Name  string  `yaml:"name"`
	Value *string `yaml:"value"` // nil = 无值(标志型方法)
}

// Load 读取并解析 feature YAML。
func Load(path string) (*Feature, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Feature
	if err := yaml.Unmarshal(src, &f); err != nil {
		return nil, fmt.Errorf("解析 feature %s: %w", path, err)
	}
	return &f, nil
}

// Skip 表示 require 不满足，携带人类可读原因。
type Skip struct{ Reason string }

func (s *Skip) Error() string { return s.Reason }

// Note 是 require 命中记录，供语义 diff 显示"命中于哪个文件"。
type Note struct {
	Var     string
	Logical string
	Path    string
}

// ResolveBindings 处理一个 step 的 require/bind，返回 (新 tags, notes)。
// require 不满足 → 返回 *Skip。
func ResolveBindings(step Step, tags map[string]string, idx config.Indexes) (map[string]string, []Note, error) {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	var notes []Note
	for _, item := range step.Require {
		kind, spec := firstEntry(&item)
		if kind != "exist" {
			return nil, nil, &Skip{Reason: fmt.Sprintf("未知 require 类型: %s", kind)}
		}
		v, tmplNode := firstEntry(spec)
		logical := Format(tmplNode.Value, out)
		fpath, node := config.ResolveLogical(idx, logical)
		if node == nil {
			return nil, nil, &Skip{Reason: fmt.Sprintf("require.exist 不满足 —— 找不到 %s", logical)}
		}
		out[v] = logical
		notes = append(notes, Note{Var: v, Logical: logical, Path: fpath})
	}
	for _, item := range step.Bind {
		v, tmplNode := firstEntry(&item)
		out[v] = Format(tmplNode.Value, out)
	}
	return out, notes, nil
}

// Format 把模板里的 {key} 替换为 tags[key](等价 Python str.format(**tags))。
func Format(tmpl string, tags map[string]string) string {
	if !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	pairs := make([]string, 0, len(tags)*2)
	for k, v := range tags {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// firstEntry 取一个 mapping yaml.Node 的第一个键与其值节点。
// 键返回字符串；值节点用于再取(require.exist 的内层 {var: tmpl})或直接取标量。
func firstEntry(n *yaml.Node) (string, *yaml.Node) {
	if n.Kind != yaml.MappingNode || len(n.Content) < 2 {
		return "", nil
	}
	return n.Content[0].Value, n.Content[1]
}

// mapPairs 按顺序返回 mapping yaml.Node 的键值(标量)对。
func mapPairs(n *yaml.Node) []xmldoc.Attr {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]xmldoc.Attr, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, xmldoc.Attr{Name: n.Content[i].Value, Value: n.Content[i+1].Value})
	}
	return out
}
