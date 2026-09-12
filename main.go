// Command addex —— 配置幂等语义补丁引擎(Go 版)。
//
// 用法(features/ 按当前工作目录解析；config/ 缺省取可执行文件同目录下的 config，
// 也可用 --config <dir> 指定)：
//
//	cd <含 features/ 的目录>
//	addex plan  --feature features/ig-auto-close.yaml
//	addex apply --feature features/ig-auto-close.yaml --chamber Ch1
//	addex apply --feature features/ig-auto-close.yaml --config config-14346
//	addex check                          # Setup 的 Param/Value 一致性自检
//	addex switch <true|false>            # 切换 config/*Simulated* 的 setSimulated 开关
//
// 骨架阶段：CLI 入口与参数已定，引擎逐阶段接入(计划 §6)。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"addex/internal/engine"
	"addex/internal/feature"
	"addex/internal/setupcheck"
	"addex/internal/simswitch"
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
	noVerify := fs.Bool("no-verify", false, "apply 后跳过 Setup 一致性自检")
	configPath := fs.String("config", "", "config 文件夹；缺省=<可执行文件同目录>/config，相对路径也以该目录为基准")

	var chambers chamberList
	fs.Var(&chambers, "chamber", "限定腔室，可连续指定或重复；缺省=全部（如 --chamber Ch1 Ch2 Ch3）")
	fs.Var(&chambers, "c", "限定腔室，可连续指定或重复；缺省=全部（如 -c Ch1 Ch2 Ch3）")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: addex <plan|apply> --feature <path> [--chamber Ch1 Ch2 ...] [--no-verify] [--config <dir>]")
		fmt.Fprintln(os.Stderr, "      addex check                  # 校验 <config>/Setup 的 Param/Value 一一对应")
		fmt.Fprintln(os.Stderr, "      addex switch <true|false>    # 切换 <config>/*Simulated* 的 setSimulated 开关")
		fs.PrintDefaults()
	}

	if len(argv) == 0 {
		fs.Usage()
		return 2
	}
	mode := argv[0]
	if mode != "plan" && mode != "apply" && mode != "check" && mode != "switch" {
		fmt.Fprintf(os.Stderr, "未知模式 %q（应为 plan / apply / check / switch）\n", mode)
		fs.Usage()
		return 2
	}
	// 先展开变参(--chamber A B → --chamber A --chamber B)，再重排为「flag 在前、位置参数在后」，
	// 这样 switch 的位置参数(true/false)写在 --config 之前也不会被 stdlib flag 提前截断。
	args := reorderFlags(expandChambers(argv[1:]), takesValue(fs))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	configDir := resolveConfigDir(*configPath, executableDir())
	if mode == "check" {
		fmt.Print("配置一致性校验 | Setup 的 Param/Value 按下标一一对应\n\n")
		return verifySetup(configDir, true)
	}
	if mode == "switch" {
		return runSwitch(configDir, fs.Args())
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

	eng, err := engine.New(configDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log := func(s string) { fmt.Println(s) }
	if err := eng.ApplyFeature(feat, chambers, mode == "apply", log); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// 后置自检：Setup 的 Param/Value 必须一一对应。这类笔误/错位往往只在设备侧才暴露，
	// 这里提前拦下：apply 默认自检并以非零退出码报错；plan 只提示；--no-verify 可显式跳过。
	if mode == "apply" && !*noVerify {
		return verifySetup(configDir, true)
	}
	if mode == "plan" {
		verifySetup(configDir, false)
	}
	return 0
}

// runSwitch 处理 `switch <true|false>`：把 config/*Simulated* 里 <setSimulated> 的开关
// 批量切成目标值。语义等价于 `sed -i 's/\bfalse\b/true/g' config/*Simulated*`，但只动
// 文本、幂等，并对"找不到开关"的文件告警(非零退出码)。
func runSwitch(configDir string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "缺少目标值：用法 switch <true|false>")
		return 2
	}
	if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "switch 只接受一个目标值，收到 %d 个：%s\n", len(args), strings.Join(args, " "))
		return 2
	}
	var target bool
	switch args[0] {
	case "true":
		target = true
	case "false":
		target = false
	default:
		fmt.Fprintf(os.Stderr, "目标值 %q 无效（应为 true 或 false）\n", args[0])
		return 2
	}
	want := args[0]
	fmt.Printf("Simulate 开关 | 目标=%s\n\n", want)

	rep, err := simswitch.Switch(configDir, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(rep.Results) == 0 {
		fmt.Fprintf(os.Stderr, "! %s/*Simulated* 下没有可切换的文件\n", configDir)
		return 1
	}
	for _, r := range rep.Results {
		switch {
		case r.Warn != "":
			fmt.Printf("! %s %s\n", r.Name, r.Warn)
		case r.Changed:
			fmt.Printf("+ %s：%d 处 setSimulated → %s\n", r.Name, r.Edits, want)
		default:
			fmt.Printf("- %s 已是 %s\n", r.Name, want)
		}
	}
	fmt.Printf("\n共 %d 个文件：改写 %d，已是目标 %d，告警 %d。\n",
		len(rep.Results), rep.ChangedFiles(), rep.UnchangedFiles(), rep.WarnedFiles())
	if rep.WarnedFiles() > 0 {
		return 1
	}
	return 0
}

// verifySetup 校验 config/Setup 的 Param/Value 一一对应并打印问题。
// fail=true 且存在 error 级问题时返回 1（供 apply / check 作硬闸门）。
func verifySetup(configDir string, fail bool) int {
	issues, err := setupcheck.CheckDir(configDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(issues) == 0 {
		return 0
	}
	errors := 0
	fmt.Println("── Setup 一致性自检 ──")
	for _, is := range issues {
		mark := "! "
		if is.Severity == setupcheck.Error {
			mark = "!!"
			errors++
		}
		fmt.Printf("  %s %s\n", mark, is)
	}
	fmt.Printf("共 %d 处问题，均必须修复。\n", len(issues))
	if fail && errors > 0 {
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

// expandChambers 展开腔室的两种写法(--chamber / -c)的连续多值形式。
func expandChambers(argv []string) []string {
	return expandVariadic(expandVariadic(argv, "chamber"), "c")
}

// executableDir 返回可执行文件所在目录(软链已解析)；取不到时退回当前工作目录。
func executableDir() string {
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		return filepath.Dir(exe)
	}
	if wd, werr := os.Getwd(); werr == nil {
		return wd
	}
	return "."
}

// resolveConfigDir 解析 --config 的目标目录：
//   - 未指定(空)  → <exeDir>/config
//   - 绝对路径     → 原样(仅 Clean)
//   - 相对路径     → <exeDir>/<path>，如 --config config-14346 即 exe 同目录下的 config-14346
func resolveConfigDir(specified, exeDir string) string {
	if specified == "" {
		return filepath.Join(exeDir, "config")
	}
	if filepath.IsAbs(specified) {
		return filepath.Clean(specified)
	}
	return filepath.Join(exeDir, specified)
}

// takesValue 判断某个 flag 是否要取值(布尔 flag 之外都要)，供 reorderFlags 判断是否吞掉下一个 token。
func takesValue(fs *flag.FlagSet) func(string) bool {
	return func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			return false
		}
		return true
	}
}

// reorderFlags 把 flag(连同其取值)前移、位置参数后移，如 `switch true --config X`
// → `--config X true`。stdlib flag 遇到首个位置参数即停止解析，而 switch 天然带一个位置
// 参数(目标值)，不重排就会静默忽略写在它后面的 --config 等 flag。
// `--` 及其后 token 一律按位置参数原样保留。
func reorderFlags(argv []string, needsValue func(string) bool) []string {
	flags := make([]string, 0, len(argv))
	rest := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" {
			rest = append(rest, argv[i:]...)
			break
		}
		if len(tok) < 2 || tok[0] != '-' {
			rest = append(rest, tok)
			continue
		}
		flags = append(flags, tok)
		if !strings.Contains(tok, "=") && needsValue(flagName(tok)) && i+1 < len(argv) {
			i++
			flags = append(flags, argv[i])
		}
	}
	return append(flags, rest...)
}

// flagName 取 `--name` / `-name` / `--name=v` 里的 name。
func flagName(tok string) string {
	name := strings.TrimLeft(tok, "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	return name
}

// pyStrList 复刻 Python str(list) 的写法，如 ['Ch1', 'Ch2']。
func pyStrList(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = "'" + x + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
