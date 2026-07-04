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
	AddIO        []AddIOSpec   `yaml:"add-io"`
	AddData      []AddDataSpec `yaml:"add-data"`
}

// WhereSpec 是 leaf 谓词 + 可选的插入定位。
//   - tag-glob / attr：筛选命中哪个 leaf 实例。
//   - before-method：定位指令(不参与筛选)——本步的 add-method 插到该既有方法之前，而非追加末尾。
type WhereSpec struct {
	TagGlob      string            `yaml:"tag-glob"`
	Attr         map[string]string `yaml:"attr"`
	BeforeMethod *MethodPos        `yaml:"before-method"`
}

// MethodPos 以方法名指定一个既有方法作为插入定位点。
type MethodPos struct {
	Name string `yaml:"name"`
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

// AddIOSpec 描述一个 add-io 动作：在 anchor(如 <IG>)下建一个 IO 点位。
// 点位形如 <name attrs...><Bd>..</Bd><Ch>..</Ch><DescriptorList>..</DescriptorList>[<Unit>..</Unit>][&Simulated_ChN;]</name>。
type AddIOSpec struct {
	Name           string     `yaml:"name"`
	Attrs          yaml.Node  `yaml:"attrs"`          // 保留书写顺序；含 simulated 时同时追加实体引用
	Bd             yaml.Node  `yaml:"Bd"`             // "auto"=自适应推断，否则取字面值
	Ch             yaml.Node  `yaml:"Ch"`             // 字面值(整数标量，取 .Value)
	Min            yaml.Node  `yaml:"Min"`            // Kind==0=未配不加；否则 <Min>值</Min>(空串→<Min></Min>)
	Max            yaml.Node  `yaml:"Max"`            // Kind==0=未配不加；否则 <Max>值</Max>(空串→<Max></Max>)
	Accuracy       yaml.Node  `yaml:"Accuracy"`       // Kind==0=未配不加；否则 <Accuracy>值</Accuracy>(NULL/空串→成对空标签)
	DescriptorList []DescItem `yaml:"DescriptorList"` // 渲染成 <DescriptorList>name:value,...</DescriptorList>
	Unit           *string    `yaml:"Unit"`           // nil=未配不加；否则 <Unit>值</Unit>(空串→<Unit/>)
}

// AddDataSpec 描述一个 add-data 动作：在 anchor(如 <Heater>)下建一个数据点位。
// 与 add-io 的差别：首属性固定为 type="data"，且不追加模拟量实体引用；
// Bd/Ch/Min/Max/Accuracy 各字段 Kind==0(未配)时跳过，NULL 或空串→成对空标签(如 <Bd></Bd>)。
type AddDataSpec struct {
	Name     string    `yaml:"name"`
	Attrs    yaml.Node `yaml:"attrs"`
	Bd       yaml.Node `yaml:"Bd"`
	Ch       yaml.Node `yaml:"Ch"`
	Min      yaml.Node `yaml:"Min"`
	Max      yaml.Node `yaml:"Max"`
	Accuracy yaml.Node `yaml:"Accuracy"`
}

// AttrPairs 按书写顺序返回数据点位属性的键值对(不含 type="data")。
func (a *AddDataSpec) AttrPairs() []xmldoc.Attr {
	return mapPairs(&a.Attrs)
}

// ScalarText 返回标量节点用于渲染的文本：未配(Kind==0)或 NULL(!!null)→空串，否则取原始文本。
func ScalarText(n *yaml.Node) string {
	if n.Kind == 0 || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// DescItem 是 DescriptorList 的一项(name:value)。
type DescItem struct {
	Name  string    `yaml:"name"`
	Value yaml.Node `yaml:"value"`
}

// AttrPairs 按书写顺序返回点位属性的键值对。
func (a *AddIOSpec) AttrPairs() []xmldoc.Attr {
	return mapPairs(&a.Attrs)
}

// HasAttr 报告点位是否声明了某属性(如 simulated)。
func (a *AddIOSpec) HasAttr(name string) bool {
	for _, p := range a.AttrPairs() {
		if p.Name == name {
			return true
		}
	}
	return false
}

// Descriptors 把 DescriptorList 拼成 "OFF:0,ON:1" 形式(name:value，逗号连接)；空列表返回 ""。
func (a *AddIOSpec) Descriptors() string {
	parts := make([]string, 0, len(a.DescriptorList))
	for _, d := range a.DescriptorList {
		parts = append(parts, d.Name+":"+d.Value.Value)
	}
	return strings.Join(parts, ",")
}

// MethodSpec 描述一个 add-method / remove-method 动作。
type MethodSpec struct {
	Name  string    `yaml:"name"`
	Value *string   `yaml:"value"` // nil = 无值(标志型方法)
	Attrs yaml.Node `yaml:"attrs"` // 额外属性(如 comment=...)，保留书写顺序；type="method" 由 ops 置于最前
}

// AttrPairs 按书写顺序返回方法额外属性的键值对(不含 type="method")。
func (m *MethodSpec) AttrPairs() []xmldoc.Attr {
	return mapPairs(&m.Attrs)
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

// Format 把模板里的 ${key} 或 {key} 替换为 tags[key]。
// ${key}(节点名语义)与 {key} 同解为标签名——二者最终都取该实例的标签，
// 但 ${key} 允许写在 alias/logical 路径里而不残留 '$'(NewReplacer 在 '$' 处优先吃掉 ${key})。
func Format(tmpl string, tags map[string]string) string {
	if !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	pairs := make([]string, 0, len(tags)*4)
	for k, v := range tags {
		pairs = append(pairs, "${"+k+"}", v, "{"+k+"}", v)
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
