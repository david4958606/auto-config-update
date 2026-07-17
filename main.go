// Command addex —— 配置幂等语义补丁引擎(Go 版)。
//
// 用法(沿用 Python 现状；config/ 按当前工作目录解析，见 GO_PORT_PLAN.md §7.3)：
//
//	cd <含 config/ features/ 的目录>
//	addex plan  --feature features/ig-auto-close.yaml
//	addex apply --feature features/ig-auto-close.yaml --chamber Ch1
//
// 骨架阶段：CLI 入口与参数已定，引擎逐阶段接入(计划 §6)。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"addex/internal/engine"
	"addex/internal/feature"
)

type chamberList []string

func (c *chamberList) String() string     { return strings.Join(*c, ",") }
func (c *chamberList) Set(v string) error { *c = append(*c, v); return nil }

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	fs := flag.NewFlagSet("addex", flag.ContinueOnError)
	featurePath := fs.String("feature", "", "feature YAML 路径(必填)")
	fs.StringVar(featurePath, "f", "", "feature YAML 路径(必填，--feature 的别名)")

	var chambers chamberList
	fs.Var(&chambers, "chamber", "限定腔室，可连续指定或重复；缺省=全部（如 --chamber Ch1 Ch2 Ch3）")
	fs.Var(&chambers, "c", "限定腔室，可连续指定或重复；缺省=全部（如 -c Ch1 Ch2 Ch3）")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: addex <plan|apply> --feature <path> [--chamber Ch1 Ch2 ...]")
		fs.PrintDefaults()
	}

	if len(argv) == 0 {
		fs.Usage()
		return 2
	}
	mode := argv[0]
	if mode != "plan" && mode != "apply" {
		fmt.Fprintf(os.Stderr, "未知模式 %q（应为 plan 或 apply）\n", mode)
		fs.Usage()
		return 2
	}
	if err := fs.Parse(expandVariadic(argv[1:], "chamber")); err != nil {
		return 2
	}
	if *featurePath == "" {
		fmt.Fprintln(os.Stderr, "缺少 --feature")
		fs.Usage()
		return 2
	}

	feat, err := feature.Load(*featurePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// 头部与 Python cli.main 逐字节一致(config 按 CWD 解析，见 §7.3)。
	header := fmt.Sprintf("功能 %s v%s | 模式=%s", feat.ID, feat.Ver(), mode)
	if len(chambers) > 0 {
		header += " | 腔室=" + pyStrList(chambers)
	}
	fmt.Print(header + "\n\n")

	eng, err := engine.New("config")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log := func(s string) { fmt.Println(s) }
	if err := eng.ApplyFeature(feat, chambers, mode == "apply", log); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// expandVariadic 让指定的 --name 支持连续多值写法（如 --chamber Ch1 Ch2 Ch3），
// 做法是把它重写为 stdlib flag 能识别的重复写法（--chamber Ch1 --chamber Ch2 ...）。
// 规则：遇到 -name / --name（不含 = 号）后，吞掉其后所有不以 - 开头的 token 作为该
// flag 的值；`--` 终止符及后续 token 原样保留。--name=Ch1 这种绑定形式只取单值。
func expandVariadic(argv []string, name string) []string {
	dash1, dash2 := "-"+name, "--"+name
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" { // 终止符：其后不再解析 flag
			out = append(out, argv[i:]...)
			break
		}
		if tok != dash1 && tok != dash2 {
			out = append(out, tok)
			continue
		}
		// 连续吞掉后续非 flag token，逐个展开为 --name <val>
		vals := 0
		for j := i + 1; j < len(argv) && !strings.HasPrefix(argv[j], "-"); j++ {
			out = append(out, tok, argv[j])
			vals++
		}
		if vals == 0 { // 无值：交给 flag 报缺参错误
			out = append(out, tok)
		}
		i += vals
	}
	return out
}

// pyStrList 复刻 Python str(list) 的写法，如 ['Ch1', 'Ch2']。
func pyStrList(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = "'" + x + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
