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
	var chambers chamberList
	fs.Var(&chambers, "chamber", "限定腔室，可重复；缺省=全部")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: addex <plan|apply> --feature <path> [--chamber Ch1]...")
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
	if err := fs.Parse(argv[1:]); err != nil {
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

// pyStrList 复刻 Python str(list) 的写法，如 ['Ch1', 'Ch2']。
func pyStrList(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = "'" + x + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
