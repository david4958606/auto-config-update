// Package simswitch —— 批量切换 config/*Simulated* 里 <setSimulated> 的布尔开关。
//
// 设备配置把"是否模拟"写在各 SimulatedFlag_* 文件的
// `<setSimulated type="method">true|false</setSimulated>` 里。现场此前用
// `sed -i 's/\bfalse\b/true/g' config/*Simulated*` 手工切换，本包把它做成一条可靠命令：
//
//   - 只改 <setSimulated> 元素的**文本**，属性与元素之外的字节逐字保留（外科式）；
//   - 目标值显式传入(true/false)，幂等：已是目标值的文件不写盘；
//   - 文件里没有 true/false 字面量时只告警、不改写（不静默吞掉）。
package simswitch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var (
	// 匹配一个完整的 <setSimulated ...>文本</setSimulated>；文本组里不允许出现 '<'。
	reElem = regexp.MustCompile(`(?s)(<setSimulated\b[^>]*>)([^<]*)(</setSimulated\s*>)`)
	// 元素文本里的布尔字面量。只在这段文本里替换，避免误伤属性值。
	reBool = regexp.MustCompile(`\b(true|false)\b`)
)

// Result 是单个文件的处理结果。
type Result struct {
	Name    string // 文件名(base)，仅用于日志
	Changed bool   // 是否真的改写了文件
	Edits   int    // 被改写的 <setSimulated> 处数
	Matched int    // 命中的 <setSimulated> 元素总数
	SawBool bool   // 元素文本里是否出现过 true/false
	Warn    string // 非空 = 该文件未切换的原因
}

// Report 汇总一次切换的结果。
type Report struct {
	Target  bool // 目标值：true=打开模拟，false=关闭模拟
	Results []Result
}

// ChangedFiles 返回被改写的文件数。
func (r Report) ChangedFiles() int {
	n := 0
	for _, x := range r.Results {
		if x.Changed {
			n++
		}
	}
	return n
}

// UnchangedFiles 返回已是目标值、未写盘的文件数。
func (r Report) UnchangedFiles() int {
	n := 0
	for _, x := range r.Results {
		if x.Warn == "" && !x.Changed {
			n++
		}
	}
	return n
}

// WarnedFiles 返回未切换(告警)的文件数。
func (r Report) WarnedFiles() int {
	n := 0
	for _, x := range r.Results {
		if x.Warn != "" {
			n++
		}
	}
	return n
}

// Switch 把 configDir 下所有名字含 "Simulated" 的文件(等价于 shell 的
// `configDir/*Simulated*`)里的 <setSimulated> 文本改成 target。
//
// 已是目标值的文件不写盘；返回的 Report 里逐文件说明结果。目录会被跳过。
func Switch(configDir string, target bool) (Report, error) {
	rep := Report{Target: target}
	paths, err := filepath.Glob(filepath.Join(configDir, "*Simulated*"))
	if err != nil {
		return rep, err
	}
	sort.Strings(paths)
	want := text(target)
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return rep, err
		}
		if info.IsDir() {
			continue
		}
		res, err := switchFile(p, want, info.Mode().Perm())
		if err != nil {
			return rep, err
		}
		res.Name = filepath.Base(p)
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}

// switchFile 处理单个文件：只替换 <setSimulated> 文本里的布尔值，其余字节不动。
func switchFile(path, want string, perm os.FileMode) (Result, error) {
	var res Result
	raw, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	out, edits, matched, sawBool := replaceSimulated(raw, want)
	res.Edits, res.Matched, res.SawBool = edits, matched, sawBool

	switch {
	case matched == 0:
		res.Warn = "未找到 <setSimulated> 元素"
		return res, nil
	case !sawBool:
		res.Warn = "<setSimulated> 文本里没有 true/false"
		return res, nil
	case edits == 0:
		return res, nil // 已是目标值，不写盘
	}
	if err := os.WriteFile(path, out, perm); err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	res.Changed = true
	return res, nil
}

// replaceSimulated 把 raw 里每个 <setSimulated> 元素文本中的 true/false 全部换成 want，
// 返回新内容、被改写的元素处数、命中的元素总数，以及文本里是否出现过布尔字面量。
func replaceSimulated(raw []byte, want string) (out []byte, edits, matched int, sawBool bool) {
	locs := reElem.FindAllSubmatchIndex(raw, -1)
	if len(locs) == 0 {
		return raw, 0, 0, false
	}
	out = make([]byte, 0, len(raw))
	last := 0
	for _, m := range locs {
		out = append(out, raw[last:m[4]]...) // 文本组之前的全部字节原样保留
		text := raw[m[4]:m[5]]
		if reBool.Match(text) {
			sawBool = true
		}
		newText := reBool.ReplaceAll(text, []byte(want))
		if !bytes.Equal(newText, text) {
			edits++
		}
		out = append(out, newText...)
		last = m[5]
	}
	out = append(out, raw[last:]...)
	return out, edits, len(locs), sawBool
}

func text(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
