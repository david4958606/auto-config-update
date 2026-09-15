// Package setupcheck —— Setup/*.xml 的结构一致性校验。
//
// Setup 文件里有两组必须**一一对应**的序列：
//
//	<Param name="X" .../>                 —— 参数声明
//	<Option><Value paramName="X">..</Value> —— 参数取值
//
// 二者数量要相同、名字要配对。一旦错位/笔误（例如声明写成 AlONGasFlowPieceCompens、
// 取值写成 AlOGasFlowPieceCompens），参数在设备侧就取不到值，而且往往只在运行期暴露。
// 本包提供离线校验，供 `check` 子命令与 `apply` 后自检使用。
//
// 判定口径：**全部为 error**。设备软件把 <Param> 与 <Option>/<Value> 当**两个并行数组**
// 按下标读取，所以"名字集合一致但顺序不同"同样是错的（第 i 个声明必须配第 i 个取值）：
//   - count        ：两条序列数量不一致；
//   - orphan-param ：某个 Param name 没有同名 Value；
//   - orphan-value ：某个 Value paramName 没有同名 Param；
//   - duplicate-*  ：Param name / Value paramName 重复；
//   - index        ：第 i 项 Param 与 Value 不同名（含整体错位/顺序不同）；
//   - attr-name    ：身份属性名大小写写错（<Value paramname="X">、<Param Name="X">）；
//   - attr-missing ：身份属性完全缺失（<Value> 没有 paramName）。
//
// 最后两类是"属性名层面"的缺陷：XML 属性名区分大小写，paramName 写成 paramname 后严格
// 匹配会把这个 <Value> 整个漏掉，只剩"数量不一致/下标错位"这类间接症状。这里单独报出来，
// 并仍按书写意图把它计入取值序列，避免再叠一堆误导性的 count/orphan 噪声。
//
// 只有"完全按下标一一同名、且属性名拼写正确"才算通过。
package setupcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"addex/internal/xmldoc"
)

// Severity 是问题级别。当前校验口径下所有问题都是 error（设备按数组下标读取，
// 顺序/错位同样不可接受）；保留类型以便将来扩展"仅提示"类检查。
type Severity string

const (
	Error Severity = "error"
)

// Issue 是一处一致性问题。
type Issue struct {
	File     string // 相对 config/ 的路径(如 Setup/Ch1Setup.xml)
	Kind     string // count | orphan-param | orphan-value | duplicate-param | duplicate-value | index | attr-name | attr-missing
	Detail   string // 人类可读描述
	Severity Severity
}

func (i Issue) String() string { return i.File + ": " + i.Detail }

// HasError 报告问题集合里是否有 error 级问题。
func HasError(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == Error {
			return true
		}
	}
	return false
}

// Sequences 提取 Setup 文档里的两条序列(按文档顺序)：
//   - params：全部 <Param name> 的 name(跳过 <Option> 子树)；
//   - values：全部 <Value paramName> 的 paramName。
//
// 属性名大小写写错(如 paramname)时按书写意图取值参与配对；这类缺陷本身由 CheckDoc 报出。
func Sequences(doc *xmldoc.Document) (params, values []string) {
	params, values, _ = extract(doc)
	return params, values
}

// attrDefect 是一处"身份属性名"缺陷(大小写写错 / 属性缺失)，落成 Issue 时才知道文件路径。
type attrDefect struct {
	tag   string // Param | Value
	want  string // 期望的属性名：name / paramName
	got   string // 实际写成的属性名；空=该属性完全缺失
	value string // 属性上取到的参数名(缺失时为空)
}

// extract 提取两条序列，并收集身份属性名层面的缺陷。
//
// Param 的身份属性是 name，Value 的身份属性是 paramName；XML 属性名区分大小写，
// 所以 <Value paramname="X"> 在严格匹配下等同于"没有 paramName"。这里用大小写不敏感的
// 兜底匹配把它认出来，仍然计入 values（否则会额外冒出 count/index/orphan 等连带噪声）。
func extract(doc *xmldoc.Document) (params, values []string, defects []attrDefect) {
	var walkParams func(n *xmldoc.Node)
	walkParams = func(n *xmldoc.Node) {
		for _, c := range n.Children {
			if c.Removed || c.IsEntity {
				continue
			}
			if c.Tag == "Option" {
				continue // 取值在 Option 里，不属于 Param 序列
			}
			if c.Tag == "Param" {
				if name, ok := takeAttr(c, "Param", "name", &defects); ok {
					params = append(params, name)
				}
			}
			walkParams(c)
		}
	}
	var walkValues func(n *xmldoc.Node)
	walkValues = func(n *xmldoc.Node) {
		for _, c := range n.Children {
			if c.Removed || c.IsEntity {
				continue
			}
			if c.Tag == "Value" {
				if name, ok := takeAttr(c, "Value", "paramName", &defects); ok {
					values = append(values, name)
				}
				continue
			}
			walkValues(c)
		}
	}
	for _, r := range doc.Roots {
		walkParams(r)
		walkValues(r)
	}
	return params, values, defects
}

// takeAttr 取节点 c 上名为 want 的身份属性，并把写法缺陷追加到 defects。
// 返回 false 表示节点上完全没有该属性（也没有大小写变体），此时无值可入序列。
func takeAttr(c *xmldoc.Node, tag, want string, defects *[]attrDefect) (string, bool) {
	value, exact, wrongCase := identityAttr(c, want)
	switch {
	case wrongCase != "":
		*defects = append(*defects, attrDefect{tag: tag, want: want, got: wrongCase, value: value})
	case !exact:
		*defects = append(*defects, attrDefect{tag: tag, want: want})
	}
	if exact || wrongCase != "" {
		return value, true
	}
	return "", false
}

// identityAttr 在 n 上找身份属性 want，返回 (值, 是否精确命中, 大小写写错的实际属性名)。
//
// 两者同时存在时以精确写法为准(设备按精确名读取)；wrongCase 非空即说明存在写法缺陷。
func identityAttr(n *xmldoc.Node, want string) (value string, exact bool, wrongCase string) {
	for _, a := range n.Attrs {
		switch {
		case a.Name == want && !exact:
			value, exact = a.Value, true
		case a.Name != want && wrongCase == "" && strings.EqualFold(a.Name, want):
			wrongCase = a.Name
			if !exact {
				value = a.Value
			}
		}
	}
	return value, exact, wrongCase
}

// CheckDoc 校验一个已解析的 Setup 文档；rel 仅用于问题描述。
func CheckDoc(rel string, doc *xmldoc.Document) []Issue {
	params, values, defects := extract(doc)
	issues := make([]Issue, 0, len(defects))
	for _, d := range defects {
		issues = append(issues, d.issue(rel))
	}
	return append(issues, compare(rel, params, values)...)
}

// issue 把属性名缺陷翻译成人类可读的 Issue（一律 error）。
func (d attrDefect) issue(rel string) Issue {
	if d.got == "" {
		return Issue{File: rel, Kind: "attr-missing", Severity: Error,
			Detail: fmt.Sprintf("%s 缺少 %s 属性", d.tag, d.want)}
	}
	detail := fmt.Sprintf("%s 的属性名写成 %s（应为 %s）", d.tag, d.got, d.want)
	if d.value != "" {
		detail += fmt.Sprintf("：参数名 %s", d.value)
	}
	return Issue{File: rel, Kind: "attr-name", Severity: Error, Detail: detail}
}

// CheckFile 读取并校验一个 Setup 文件。
func CheckFile(path, rel string) ([]Issue, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := xmldoc.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("解析 %s: %w", rel, err)
	}
	return CheckDoc(rel, doc), nil
}

// CheckDir 校验 configDir/Setup 下的全部 *.xml（目录不存在=通过；忽略 ~ 备份文件）。
func CheckDir(configDir string) ([]Issue, error) {
	dir := filepath.Join(configDir, "Setup")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var issues []Issue
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, "~") {
			continue
		}
		rel := "Setup/" + name
		iss, err := CheckFile(filepath.Join(dir, name), rel)
		if err != nil {
			return nil, err
		}
		issues = append(issues, iss...)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].File != issues[j].File {
			return issues[i].File < issues[j].File
		}
		return issues[i].Kind < issues[j].Kind
	})
	return issues, nil
}

func compare(rel string, params, values []string) []Issue {
	if len(params) == 0 && len(values) == 0 {
		return nil
	}
	var issues []Issue
	issues = append(issues, duplicates(rel, "Param", "name", params)...)
	issues = append(issues, duplicates(rel, "Value", "paramName", values)...)

	if len(params) != len(values) {
		issues = append(issues, Issue{File: rel, Kind: "count", Severity: Error,
			Detail: fmt.Sprintf("Param(%d) 与 Value(%d) 数量不一致", len(params), len(values))})
	}

	// 按多重集合找"有声明无取值 / 有取值无声明"：这条能直接把笔误钉到具体名字。
	pc, vc := counts(params), counts(values)
	names := map[string]bool{}
	for n := range pc {
		names[n] = true
	}
	for n := range vc {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		if pc[n] > vc[n] {
			issues = append(issues, Issue{File: rel, Kind: "orphan-param", Severity: Error,
				Detail: fmt.Sprintf("Param name=%s 没有对应的 Value", n)})
		}
		if vc[n] > pc[n] {
			issues = append(issues, Issue{File: rel, Kind: "orphan-value", Severity: Error,
				Detail: fmt.Sprintf("Value paramName=%s 没有对应的 Param", n)})
		}
	}

	// 设备按**数组下标**读取 Param 与 Value，位置即语义：只要第 i 项不同名就是错误
	// （既覆盖"集合一致但顺序不同"，也覆盖插入/删除造成的整体错位）。
	n := len(params)
	if len(values) < n {
		n = len(values)
	}
	const maxIndexIssues = 10
	mismatch := 0
	for i := 0; i < n; i++ {
		if params[i] == values[i] {
			continue
		}
		mismatch++
		if mismatch <= maxIndexIssues {
			issues = append(issues, Issue{File: rel, Kind: "index", Severity: Error,
				Detail: fmt.Sprintf("第 %d 项 Param=%s 与 Value=%s 不同名", i, params[i], values[i])})
		}
	}
	if mismatch > maxIndexIssues {
		issues = append(issues, Issue{File: rel, Kind: "index", Severity: Error,
			Detail: fmt.Sprintf("下标不对应共 %d 处（已列出前 %d 处）", mismatch, maxIndexIssues)})
	}
	return issues
}

func duplicates(rel, tag, key string, seq []string) []Issue {
	seen := map[string]int{}
	var out []Issue
	for _, name := range seq {
		seen[name]++
	}
	sorted := make([]string, 0, len(seen))
	for n := range seen {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		if seen[n] > 1 {
			out = append(out, Issue{File: rel, Kind: "duplicate-" + strings.ToLower(tag), Severity: Error,
				Detail: fmt.Sprintf("%s %s=%s 重复 %d 次", tag, key, n, seen[n])})
		}
	}
	return out
}

func counts(seq []string) map[string]int {
	m := map[string]int{}
	for _, s := range seq {
		m[s]++
	}
	return m
}
