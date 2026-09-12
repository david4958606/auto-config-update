// Package engine —— 把一个 feature 的 steps 应用到各腔室，产出语义 diff、原地落盘。
//
// 编排(照搬 Python engine.py)：
//
//	遍历 Control 片段(每 = 一腔室，实体保留式加载) →
//	  按 steps 顺序执行(共享同一棵内存树，故步2能看到步1刚建的 <IonGauge>) →
//	    每步 anchor 定位(leaf 多实例则 fan-out) →
//	      每个实例：并入腔室级绑定 → require/bind → 执行动作(幂等) →
//	该腔室有改动才回写它自己的片段(保留 &实体; 与原格式；master/其它片段不动)。
//
// 另外支持"文件级步骤"(step.file 非空)：不参与腔室循环，在腔室步骤之后按声明顺序
// 对 config/<file> 各执行一次(用于 Setup/*.xml、SysLog_config.xml、Control_config.xml)。
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"addex/internal/anchor"
	"addex/internal/config"
	"addex/internal/feature"
	"addex/internal/ops"
	"addex/internal/splice"
	"addex/internal/xmldoc"
	"gopkg.in/yaml.v3"
)

// Engine 持有只读逻辑索引与实体声明(构建一次，跨腔室复用)。
type Engine struct {
	configDir     string
	controlMaster string
	driverMaster  string // Driver_config.xml(根 <IOBridge>)；不存在时 IOBridge 步骤跳过
	ioMaster      string // IO_config.xml(根 <IO>)；不存在时 IO 步骤跳过
	idx           config.Indexes
	declared      []string
}

// New 用 configDir 构造引擎。目录由 CLI 决定：--config 指定，
// 缺省为可执行文件同目录下的 config(见 main.go 的 resolveConfigDir)。
func New(configDir string) (*Engine, error) {
	ioMaster := filepath.Join(configDir, "IO_config.xml")
	controlMaster := filepath.Join(configDir, "Control", "Control_config.xml")
	driverMaster := filepath.Join(configDir, "Driver_config.xml")
	idx, err := config.BuildIndexes(ioMaster, controlMaster)
	if err != nil {
		return nil, err
	}
	declared, err := config.DeclaredEntities(controlMaster)
	if err != nil {
		return nil, err
	}
	return &Engine{configDir: configDir, controlMaster: controlMaster, driverMaster: driverMaster, ioMaster: ioMaster, idx: idx, declared: declared}, nil
}

// target 是一个域(Control/IOBridge)在某腔室的落点：一份片段文档 + 累积的字节编辑。
//
// roots 是该腔室在本片段里的全部顶层节点。通常只有一个；但一个片段可含【同名 tag 的
// 多个顶层节点】(如 Driver_Ch1 里既有容器 <Ch1>(内含 PG/IG) 又有 <Ch1 class="DeviceNet">
// 驱动节点)，此时 anchor 需逐 root 解析后汇总，故存为 slice。
type target struct {
	doc   *xmldoc.Document
	path  string
	roots []*xmldoc.Node
	edits []xmldoc.Edit
}

// loadFragByChamber 读取 master 引用的片段，按【顶层元素标签】(腔室名)建索引。
// master 不存在(如未配 Driver_config.xml)时返回空表，让相应域的步骤自然跳过。
func loadFragByChamber(master string) (map[string]*target, error) {
	out := map[string]*target{}
	if _, err := os.Stat(master); err != nil {
		return out, nil
	}
	frags, err := config.FragmentFiles(master)
	if err != nil {
		return nil, err
	}
	for _, fr := range frags {
		src, err := os.ReadFile(fr.Path)
		if err != nil {
			return nil, err
		}
		doc, err := xmldoc.Parse(src)
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", fr.Path, err)
		}
		for _, r := range doc.Roots {
			tg := out[r.Tag]
			if tg == nil {
				tg = &target{doc: doc, path: fr.Path}
				out[r.Tag] = tg
			}
			tg.roots = append(tg.roots, r) // 同名顶层节点累加,不覆盖
		}
	}
	return out, nil
}

// SplitSteps 把 steps 分成"腔室级"与"文件级"两组(保持各自声明顺序)。
// 文件级步骤指声明了 file:(作用于既有文件) 或 new-file:(新建文件) 的步骤。
func SplitSteps(steps []feature.Step) (chamber, files []feature.Step) {
	for _, s := range steps {
		if s.File != "" || s.NewFile != "" {
			files = append(files, s)
		} else {
			chamber = append(chamber, s)
		}
	}
	return
}

// isTemplatedFileStep 报告文件级步骤的路径里是否含占位符(需按腔室展开)。
// `{X}` 与 `${X}` 都含 `{`，故只判断花括号即可。
func isTemplatedFileStep(s feature.Step) bool {
	return strings.ContainsRune(s.File, '{') || strings.ContainsRune(s.NewFile, '{')
}

// splitTemplatedFiles 把文件级步骤分成两组：
//   - perChamber：路径含占位符 → 按腔室展开，逐腔室用该腔室的绑定解析路径与取值，各执行一次；
//   - global：路径为字面量 → 在腔室循环之后全局执行一次(原有语义)。
func splitTemplatedFiles(steps []feature.Step) (perChamber, global []feature.Step) {
	for _, s := range steps {
		if isTemplatedFileStep(s) {
			perChamber = append(perChamber, s)
			continue
		}
		global = append(global, s)
	}
	return
}

// ApplyFeature 对选定腔室(nil=全部)应用 feature；write=true 才落盘。
// 每个腔室同时持有 Control 与 IOBridge(Driver) 两个域的片段，按 anchor 首段路由；
// 跨域共享 chamberBinds(如 Control 侧绑定的 {ITO}=Ch1 供 IOBridge 步骤的 ${ITO} 使用)。
//
// 文件级步骤分两类：路径含占位符的(如 `file: Setup/Setup_${Chamber}.xml`)在腔室循环内
// **逐腔室展开**，用该腔室的绑定解析路径与取值；路径为字面量的在腔室循环之后各执行一次。
func (e *Engine) ApplyFeature(f *feature.Feature, selected []string, write bool, log func(string)) error {
	chamberSteps, fileSteps := SplitSteps(f.Steps)
	perChamberFiles, globalFiles := splitTemplatedFiles(fileSteps)
	// 纯文件级 feature 且没有按腔室展开的步骤(如 Setup/SysLog 的静态升级)：不必加载任何腔室片段，
	// 省掉对全部 Control/IO/Driver 片段的解析。
	if len(chamberSteps) == 0 && len(perChamberFiles) == 0 {
		return e.runFileSteps(globalFiles, nil, false, write, log)
	}
	frags, err := config.FragmentFiles(e.controlMaster)
	if err != nil {
		return err
	}
	driverByChamber, err := loadFragByChamber(e.driverMaster)
	if err != nil {
		return err
	}
	ioByChamber, err := loadFragByChamber(e.ioMaster)
	if err != nil {
		return err
	}
	var want map[string]bool
	if len(selected) > 0 {
		want = map[string]bool{}
		for _, c := range selected {
			want[c] = true
		}
	}

	for _, fr := range frags {
		src, err := os.ReadFile(fr.Path)
		if err != nil {
			return err
		}
		doc, err := xmldoc.Parse(src)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", fr.Path, err)
		}
		if len(doc.Roots) == 0 {
			continue
		}
		croot := doc.Roots[0]
		chamber := croot.Tag
		if want != nil && !want[chamber] {
			continue
		}

		log(fmt.Sprintf("[%s] (%s)", chamber, filepath.Base(fr.Path)))

		// 该腔室的各域落点。Control 恒有；IOBridge 仅当 Driver 片段存在。
		doms := map[string]*target{"Control": {doc: doc, path: fr.Path, roots: []*xmldoc.Node{croot}}}
		if d := driverByChamber[chamber]; d != nil {
			doms["IOBridge"] = d
		}
		if d := ioByChamber[chamber]; d != nil {
			doms["IO"] = d
		}

		// seed：腔室根的 {类}=腔室标签(如 {ITO}=Ch1)，供 anchor 名称段 ${ITO} 与模板取值。
		// 另加一个与 class 无关的保留绑定 {Chamber}=腔室标签：某些片段(如 Interlock_*)
		// 的根没有 class，或同一改动要跨多个 class 的腔室复用，此时用 ${Chamber} 才能
		// 写出"一份声明、逐腔室自动替换腔室名"的 feature(见 add-pedcurpos-dataex.yaml)。
		chamberBinds := map[string]string{"Chamber": chamber}
		if cls := croot.Class(); cls != "" {
			chamberBinds[cls] = chamber
		}
		for i := range chamberSteps {
			e.runStep(&chamberSteps[i], doms, chamberBinds, chamber, log)
		}
		// 按腔室展开的文件级步骤：用本腔室绑定解析 `${Chamber}`/{Class} 后各执行一次。
		if err := e.runFileSteps(perChamberFiles, chamberBinds, true, write, log); err != nil {
			return err
		}

		// 分域回写(各自片段独立)；driver 片段可能跨腔室共用一个 doc，故按 path 落。
		if write {
			for _, tg := range dedupTargets(doms) {
				if len(tg.edits) == 0 {
					continue
				}
				outBytes := splice.Apply(tg.doc.Src, tg.edits)
				if err := os.WriteFile(tg.path, outBytes, 0o644); err != nil {
					return err
				}
				log(fmt.Sprintf("[%s] + 已写回 %s（%d 处编辑，其余字节不动）", chamber, filepath.Base(tg.path), len(tg.edits)))
			}
		}
	}

	// 字面量路径的文件级步骤(Setup/*.xml、SysLog_config.xml、Control_config.xml 等)。
	return e.runFileSteps(globalFiles, nil, false, write, log)
}

// runFileSteps 逐条执行文件级步骤：每个文件只读一次、改完(可选)写回。
//   - tags 非 nil 时为"按腔室展开"，路径与取值里的占位符用该腔室绑定解析；
//   - perChamber 为真时，文件不存在只告警跳过(不同腔室未必都有该文件)，不影响其它腔室；
//   - new-file 步骤不走解析：文件不存在才逐字写 Content(存在即幂等 no-op，绝不覆盖)。
func (e *Engine) runFileSteps(steps []feature.Step, tags map[string]string, perChamber, write bool, log func(string)) error {
	for i := range steps {
		step := &steps[i]
		if step.NewFile != "" {
			if err := e.createFile(step, tags, write, log); err != nil {
				return err
			}
			continue
		}
		rel := feature.Format(step.File, tags)
		path := filepath.Join(e.configDir, filepath.FromSlash(rel))
		src, err := os.ReadFile(path)
		if err != nil {
			if perChamber && os.IsNotExist(err) {
				log(fmt.Sprintf("[文件 %s] ! 跳过：文件不存在", rel))
				continue
			}
			return fmt.Errorf("读取 %s: %w", rel, err)
		}
		doc, err := xmldoc.Parse(src)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", rel, err)
		}
		if len(doc.Roots) == 0 {
			log(fmt.Sprintf("[文件 %s] 跳过：无顶层元素", rel))
			continue
		}
		log(fmt.Sprintf("[文件 %s]", rel))
		tg := &target{doc: doc, path: path, roots: doc.Roots}
		segs := parseAnchorPlain(feature.Format(step.Anchor, tags))
		where := buildWhere(step.Where, tags)
		var matches []anchor.Match
		for _, root := range doc.Roots {
			if len(segs) == 0 {
				// 空 anchor = 文件根元素本身(整份文件级操作，如 add-setup 的 Param/Option)。
				matches = append(matches, anchor.Match{Node: root, Tags: map[string]string{}})
				continue
			}
			matches = append(matches, anchor.Resolve(root, segs, where)...)
		}
		if len(matches) == 0 {
			log(fmt.Sprintf("  步[%s] 跳过：anchor %s 无匹配", step.Name, step.Anchor))
			continue
		}
		croot := doc.Roots[0]
		for _, m := range matches {
			e.applyActions(step, tg, m, tags, "", croot, log)
		}
		if write && len(tg.edits) > 0 {
			outBytes := splice.Apply(tg.doc.Src, tg.edits)
			if err := os.WriteFile(tg.path, outBytes, 0o644); err != nil {
				return err
			}
			log(fmt.Sprintf("[文件 %s] + 已写回 %s（%d 处编辑，其余字节不动）", rel, rel, len(tg.edits)))
		}
	}
	return nil
}

// createFile 执行 new-file 步骤：config/<new-file> 不存在时按 Content 逐字新建。
// 幂等判据=文件是否已存在——已存在即 no-op，绝不覆盖(与"外科式补丁"一致)。
// tags 非 nil 时(按腔室展开)路径里的占位符用该腔室绑定解析；content 始终按字面写入。
func (e *Engine) createFile(step *feature.Step, tags map[string]string, write bool, log func(string)) error {
	rel := feature.Format(step.NewFile, tags)
	path := filepath.Join(e.configDir, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err == nil {
		log(fmt.Sprintf("[新文件 %s] - 已存在，保持原样(no-op)", rel))
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查 %s: %w", rel, err)
	}
	if !write {
		log(fmt.Sprintf("[新文件 %s] + 待新建（%d 字节）", rel, len(step.Content)))
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("新建目录 %s: %w", dir, err)
		}
	}
	// 逐字落盘：content 原样写入(含 CRLF/LF 与是否带行尾换行)，保证新建文件与声明完全一致。
	content := step.Content
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("新建 %s: %w", rel, err)
	}
	log(fmt.Sprintf("[新文件 %s] + 已新建 %s（%d 字节）", rel, rel, len(content)))
	return nil
}

// dedupTargets 按 path 去重(不同域理论上落不同文件；防御性去重避免重复写盘)。
func dedupTargets(doms map[string]*target) []*target {
	seen := map[string]bool{}
	var out []*target
	// 稳定顺序：Control 先，其余次之。
	order := []string{"Control", "IO", "IOBridge"}
	for _, k := range order {
		if tg := doms[k]; tg != nil && !seen[tg.path] {
			seen[tg.path] = true
			out = append(out, tg)
		}
	}
	return out
}

// inferBd 为 add-io 的 Bd:auto 推断板号：
//  1. 优先取 ig 下已有 IO 点位的 <Bd> 文本(首个非空)——沿用本节点其它点位的板号；
//  2. 否则按腔室号推断：ChN → N*100(Ch1→100, Ch2→200, …)。
func inferBd(ig *xmldoc.Node, chamber string) string {
	for _, pt := range ig.Children {
		if pt.Removed || pt.IsEntity {
			continue
		}
		if bd := xmldoc.FindChild(pt, "Bd"); bd != nil && bd.Text != "" {
			return bd.Text
		}
	}
	// 从腔室标签末尾取连续数字(如 "Ch12"→12)。
	i := len(chamber)
	for i > 0 && chamber[i-1] >= '0' && chamber[i-1] <= '9' {
		i--
	}
	if n, err := strconv.Atoi(chamber[i:]); err == nil {
		return strconv.Itoa(n * 100)
	}
	return ""
}

// isDomain 判断 anchor 首段是否为域前缀(与各 master 根标签同名)。
func isDomain(s string) bool { return s == "Control" || s == "IO" || s == "IOBridge" }

// parseAnchor 把 anchor 拆成 (域, 段列表)。
//   - 首段是 Control/IO/IOBridge → 作域；否则默认 Control(兼容老式无域前缀 anchor)。
//   - {X} 或裸 X → 按 class 匹配；${X} → 按标签名匹配(Match 待用 chamberBinds 解析)。
func parseAnchor(a string) (string, []anchor.Seg) {
	parts := splitAnchor(a)
	domain := "Control"
	if len(parts) > 0 && isDomain(parts[0]) {
		domain = parts[0]
		parts = parts[1:]
	}
	return domain, anchorSegs(parts)
}

// parseAnchorPlain 用于文件级步骤：不做域前缀剥离，整条路径相对文件根元素解析。
func parseAnchorPlain(a string) []anchor.Seg { return anchorSegs(splitAnchor(a)) }

func splitAnchor(a string) []string {
	var parts []string
	for _, p := range strings.Split(a, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func anchorSegs(parts []string) []anchor.Seg {
	segs := make([]anchor.Seg, 0, len(parts))
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "${") && strings.HasSuffix(p, "}"):
			k := p[2 : len(p)-1]
			segs = append(segs, anchor.Seg{ByName: true, Bind: k})
		case strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}"):
			k := p[1 : len(p)-1]
			segs = append(segs, anchor.Seg{Match: k, Bind: k})
		default:
			segs = append(segs, anchor.Seg{Match: p, Bind: p})
		}
	}
	return segs
}

func (e *Engine) runStep(step *feature.Step, doms map[string]*target, chamberBinds map[string]string, chamber string, log func(string)) {
	domain, segs := parseAnchor(step.Anchor)
	tg := doms[domain]
	if tg == nil {
		log(fmt.Sprintf("  步[%s] 跳过：本腔室无 %s 域片段(anchor %s)", step.Name, domain, step.Anchor))
		return
	}
	// 名称段 ${X} 用腔室级绑定解析出目标标签名。
	for i := range segs {
		if segs[i].ByName {
			v, ok := chamberBinds[segs[i].Bind]
			if !ok {
				log(fmt.Sprintf("  步[%s] 跳过：anchor %s 的 ${%s} 未绑定", step.Name, step.Anchor, segs[i].Bind))
				return
			}
			segs[i].Match = v
		}
	}
	where := buildWhere(step.Where, chamberBinds)
	// 逐 root 解析后汇总(片段可含同名 tag 的多个顶层节点)。
	var matches []anchor.Match
	for _, root := range tg.roots {
		matches = append(matches, anchor.Resolve(root, segs, where)...)
	}
	if len(matches) == 0 {
		log(fmt.Sprintf("  步[%s] 跳过：anchor %s 无匹配", step.Name, step.Anchor))
		return
	}
	croot := tg.roots[0] // 腔室根:供 include-entity 实体解析(Control 域 1 腔室 1 root)
	for _, m := range matches {
		e.applyActions(step, tg, m, chamberBinds, chamber, croot, log)
	}
}

// buildWhere 把 step 的 where 谓词转成 anchor.Where，并对 tag-glob / attr 值做占位符替换。
// 与所有取值字段同一口径：`{X}` 与 `${X}` 同解为 tags[X]。文件级步骤没有腔室绑定，传 nil 即原样。
func buildWhere(w *feature.WhereSpec, tags map[string]string) *anchor.Where {
	if w == nil {
		return nil
	}
	attrs := make(map[string]string, len(w.Attr))
	for k, v := range w.Attr {
		attrs[k] = feature.Format(v, tags)
	}
	return &anchor.Where{TagGlob: feature.Format(w.TagGlob, tags), HasGlob: w.TagGlob != "", Attr: attrs}
}

// toSel 把 feature 选择器转成 ops 选择器，并做占位符替换。
func toSel(s *feature.SelSpec, tags map[string]string) *ops.Sel {
	if s == nil {
		return nil
	}
	attrs := make(map[string]string, len(s.Attr))
	for k, v := range s.Attr {
		attrs[k] = feature.Format(v, tags)
	}
	sel := &ops.Sel{Tag: feature.Format(s.Tag, tags), Attrs: attrs}
	if s.Value != nil {
		val := feature.Format(*s.Value, tags)
		sel.Value = &val
	}
	return sel
}

// resolveBefore 把 before/after 选择器解析成"插到哪个既有兄弟之前"。
func resolveBefore(anchorNode *xmldoc.Node, sel *feature.SelSpec, after bool, tags map[string]string) *xmldoc.Node {
	os := toSel(sel, tags)
	if os == nil {
		return nil
	}
	if after {
		return ops.InsertAfterNode(anchorNode, *os)
	}
	return ops.InsertBeforeNode(anchorNode, os, "")
}

// applyActions 对一个 anchor 命中实例执行该步的全部动作。
func (e *Engine) applyActions(step *feature.Step, tg *target, m anchor.Match, chamberBinds map[string]string, chamber string, croot *xmldoc.Node, log func(string)) {
	inst := fmt.Sprintf("%s(class=%s)", m.Node.Tag, m.Node.Class())
	// 并入跨步对象引用；anchor 绑定优先。
	tags := make(map[string]string, len(chamberBinds)+len(m.Tags))
	for k, v := range chamberBinds {
		tags[k] = v
	}
	for k, v := range m.Tags {
		tags[k] = v
	}

	tags, notes, err := feature.ResolveBindings(*step, tags, e.idx)
	if err != nil {
		log(fmt.Sprintf("  步[%s] %s 跳过：%s", step.Name, inst, err.Error()))
		return
	}
	for _, n := range notes {
		where := "?"
		if n.Path != "" {
			where = filepath.Base(n.Path)
		}
		log(fmt.Sprintf("  步[%s] %s: %s 命中于 %s（绑定 {%s}）", step.Name, inst, n.Logical, where, n.Var))
	}

	var results []ops.Result
	src := tg.doc.Src
	// resolveEntity 把 include-entity 的 glob 按顶层声明解析成真名；无匹配→""并告警(供 add-io/add-data 复用)。
	resolveEntity := func(glob string) string {
		g := feature.Format(glob, tags)
		name := config.ResolveEntityName(e.declared, g, croot)
		if name == "" {
			log(fmt.Sprintf("  步[%s] %s: ! include-entity 未在 Control_config.xml 找到匹配 %s 的实体", step.Name, inst, g))
		}
		return name
	}
	for _, nd := range step.AddNode {
		attrs := make([]xmldoc.Attr, 0, 4)
		for _, a := range nd.AttrPairs() {
			attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		before := resolveBefore(m.Node, nd.Before, false, tags)
		if before == nil && nd.After != nil {
			before = resolveBefore(m.Node, nd.After, true, tags)
		}
		results = append(results, ops.AddNode(m.Node, feature.Format(nd.Tag, tags), feature.Format(nd.Class, tags), attrs, before))
		chamberBinds[feature.Format(nd.Tag, tags)] = "./" + feature.Format(nd.Tag, tags) // 登记对象引用，供后续步骤 {标签}

		if nd.IncludeEntity != "" {
			target := xmldoc.FindChild(m.Node, feature.Format(nd.Tag, tags)) // 新建或既有目标
			entName := config.ResolveEntityName(e.declared, feature.Format(nd.IncludeEntity, tags), croot)
			if entName != "" && target != nil {
				results = append(results, ops.AddEntityRef(target, entName))
			} else {
				log(fmt.Sprintf("  步[%s] %s: ! include-entity 未在 Control_config.xml 找到匹配 %s 的实体",
					step.Name, inst, feature.Format(nd.IncludeEntity, tags)))
			}
		}
	}
	// add-data 先于 add-method 处理：数据点位在本节点里排在方法调用之前(与 YAML 声明序一致)，
	// 且方法(如 setTempB4Offset)常引用该数据节点(./TempB4OffsetVp)。
	for i := range step.AddData {
		d := &step.AddData[i]
		attrs := make([]xmldoc.Attr, 0, 4)
		for _, a := range d.AttrPairs() {
			attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		children := make([]xmldoc.Attr, 0, 7)
		for _, f := range []struct {
			name string
			node *yaml.Node
		}{
			{"Bd", &d.Bd}, {"Ch", &d.Ch}, {"Min", &d.Min}, {"Max", &d.Max}, {"Accuracy", &d.Accuracy},
		} {
			if f.node.Kind == 0 {
				continue
			}
			val := feature.Format(feature.ScalarText(f.node), tags)
			if f.name == "Bd" && val == "auto" {
				val = inferBd(m.Node, chamber)
			}
			children = append(children, xmldoc.Attr{Name: f.name, Value: val})
		}
		// DescriptorList / Unit 顺序对齐 add-io：置于 Accuracy 之后。
		if desc := feature.Format(d.Descriptors(), tags); desc != "" {
			children = append(children, xmldoc.Attr{Name: "DescriptorList", Value: desc})
		}
		// Unit 与 Bd/Ch/Min/Max/Accuracy 同族：Kind==0 缺省跳过，NULL/空串→<Unit></Unit>。
		if d.Unit.Kind != 0 {
			children = append(children, xmldoc.Attr{Name: "Unit", Value: feature.Format(feature.ScalarText(&d.Unit), tags)})
		}
		entity := ""
		if d.IncludeEntity != "" {
			entity = resolveEntity(d.IncludeEntity)
		}
		before := resolveBefore(m.Node, d.Before, false, tags)
		if before == nil && d.After != nil {
			before = resolveBefore(m.Node, d.After, true, tags)
		}
		results = append(results, ops.AddData(m.Node, feature.Format(d.Name, tags), attrs, children, entity, before))
	}
	// 普通元素(Param/Value/FileSize/spare 等)。
	for i := range step.AddElement {
		ae := &step.AddElement[i]
		attrs := make([]xmldoc.Attr, 0, 4)
		for _, a := range ae.AttrPairs() {
			attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		before := resolveBefore(m.Node, ae.Before, false, tags)
		if before == nil && ae.After != nil {
			before = resolveBefore(m.Node, ae.After, true, tags)
		}
		results = append(results, ops.AddElement(m.Node, feature.Format(ae.Tag, tags), attrs,
			feature.Format(ae.Text, tags), ae.SelfClose, ae.PairedEmpty, before))
	}
	// add-setup：Setup 的 <Param>/<Value> 成对追加(都落在各自序列末尾)。
	for i := range step.AddSetup {
		sp := &step.AddSetup[i]
		attrs := make([]xmldoc.Attr, 0, 9)
		for _, a := range sp.ParamAttrs() {
			attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		results = append(results, ops.AddSetupPair(m.Node, feature.Format(sp.Param, tags),
			attrs, feature.Format(sp.ValueText(), tags)))
	}
	// add-xml：内联 XML 片段(新对象/新方法块)，按结构判重。
	for i := range step.AddXML {
		ax := &step.AddXML[i]
		raw := feature.Format(ax.XML, tags)
		sub, err := xmldoc.Parse([]byte(raw))
		if err != nil || len(sub.Roots) == 0 {
			log(fmt.Sprintf("  步[%s] %s: ! add-xml 片段解析失败: %v", step.Name, inst, err))
			continue
		}
		frag := sub.Roots[0]
		xmldoc.MarkSynthetic(frag)
		before := resolveBefore(m.Node, ax.Before, false, tags)
		if before == nil && ax.After != nil {
			before = resolveBefore(m.Node, ax.After, true, tags)
		}
		results = append(results, ops.AddRawFragment(m.Node, frag, indentBlock(raw, xmldoc.RealDepth(m.Node)+1), before))
	}
	// rename-node：给选中元素改名(开/闭标签同步)并/或增改属性。
	// 排在 set-text/set-attr/remove-node 之前，便于同一步里"先改名、再按新名字改写"。
	for i := range step.RenameNode {
		rn := &step.RenameNode[i]
		sel := ops.Sel{Tag: feature.Format(rn.Tag, tags), Attrs: formatAttrs(rn.Attr, tags), Has: toSel(rn.Has, tags)}
		if rn.Value != nil {
			v := feature.Format(*rn.Value, tags)
			sel.Value = &v
		}
		attrs := make([]xmldoc.Attr, 0, len(rn.Attrs.Content)/2)
		for _, a := range rn.AttrPairs() {
			attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		results = append(results, ops.RenameNode(m.Node, sel, feature.Format(rn.Child, tags),
			feature.Format(rn.To, tags), attrs))
	}
	// set-text / set-attr / remove-node：原地改写已存在节点。
	for i := range step.SetText {
		st := &step.SetText[i]
		sel := ops.Sel{Tag: feature.Format(st.Tag, tags), Attrs: formatAttrs(st.Attr, tags)}
		old := ""
		hasOld := st.Old != nil
		if hasOld {
			old = feature.Format(*st.Old, tags)
		}
		results = append(results, ops.SetText(m.Node, sel, feature.Format(st.Child, tags), old, hasOld, feature.Format(st.Value, tags)))
	}
	for i := range step.SetAttr {
		sa := &step.SetAttr[i]
		sel := ops.Sel{Tag: feature.Format(sa.Tag, tags), Attrs: formatAttrs(sa.Attr, tags)}
		old := ""
		hasOld := sa.Old != nil
		if hasOld {
			old = feature.Format(*sa.Old, tags)
		}
		results = append(results, ops.SetAttr(m.Node, sel, feature.Format(sa.Child, tags), old, hasOld,
			feature.Format(sa.Name, tags), feature.Format(sa.Value, tags)))
	}
	for i := range step.RemoveNode {
		rn := &step.RemoveNode[i]
		sel := ops.Sel{Tag: feature.Format(rn.Tag, tags), Attrs: formatAttrs(rn.Attr, tags), Has: toSel(rn.Has, tags)}
		if rn.Value != nil {
			v := feature.Format(*rn.Value, tags)
			sel.Value = &v
		}
		results = append(results, ops.RemoveNode(m.Node, sel, feature.Format(rn.Child, tags)))
	}
	// wrap：注释掉 / CDATA 化一段节点区间。
	for i := range step.Wrap {
		wr := &step.Wrap[i]
		open, close := wr.Open, wr.Close
		if wr.Comment {
			open, close = "<!--", "-->"
		}
		if open == "" {
			open = "<!--"
		}
		if close == "" {
			close = "-->"
		}
		open, close = feature.Format(open, tags), feature.Format(close, tags)
		sels := make([]ops.Sel, 0, len(wr.Select))
		for j := range wr.Select {
			sel := ops.Sel{Tag: feature.Format(wr.Select[j].Tag, tags), Attrs: formatAttrs(wr.Select[j].Attr, tags)}
			if wr.Select[j].Value != nil {
				v := feature.Format(*wr.Select[j].Value, tags)
				sel.Value = &v
			}
			sels = append(sels, sel)
		}
		results = append(results, ops.WrapRange(m.Node, sels, feature.Format(wr.Child, tags), open, close, src))
	}
	// uncomment：放开/删除被注释包住的块(文本级)。
	for i := range step.Uncomment {
		uc := &step.Uncomment[i]
		open, close := uc.Open, uc.Close
		if open == "" {
			open = "<!--"
		}
		if close == "" {
			close = "-->"
		}
		open, close = feature.Format(open, tags), feature.Format(close, tags)
		results = append(results, ops.Uncomment(src, feature.Format(uc.Find, tags), open, close, uc.Drop))
	}
	// add-blank / add-comment：空行与注释，作为方法块的前置注解，排在 add-method 之前。
	// 注释/空行不进解析树，故判重按 anchor 的原始字节区间(open..close)整串扫描——二次 apply 幂等。
	innerRaw := ""
	if m.Node.Start >= 0 && m.Node.CloseStart >= 0 {
		innerRaw = string(src[m.Node.Start:m.Node.CloseStart])
	}
	if step.AddBlank > 0 {
		results = append(results, ops.AddBlank(m.Node, step.AddBlank, innerRaw))
	}
	for _, raw := range step.AddComment {
		results = append(results, ops.AddComment(m.Node, feature.Format(raw, tags), innerRaw))
	}
	// where.before-method：把本步新增方法插到该既有方法之前(而非追加末尾)。
	var stepBefore *xmldoc.Node
	if step.Where != nil && step.Where.BeforeMethod != nil {
		name := feature.Format(step.Where.BeforeMethod.Name, tags)
		if stepBefore = xmldoc.FindChild(m.Node, name); stepBefore == nil {
			log(fmt.Sprintf("  步[%s] %s: ! before-method 未找到方法 %s，改为追加末尾", step.Name, inst, name))
		}
	}
	for _, mm := range step.AddMethod {
		val := ""
		hasVal := mm.Value != nil
		if hasVal {
			val = feature.Format(*mm.Value, tags)
		}
		var extra []xmldoc.Attr
		for _, a := range mm.AttrPairs() {
			extra = append(extra, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		before := stepBefore
		if mm.Before != nil {
			before = resolveBefore(m.Node, mm.Before, false, tags)
		} else if mm.After != nil {
			before = resolveBefore(m.Node, mm.After, true, tags)
		}
		results = append(results, ops.AddMethod(m.Node, feature.Format(mm.Name, tags), val, hasVal, extra, before))
	}
	for _, mm := range step.RemoveMethod {
		val := ""
		if mm.Value != nil {
			val = feature.Format(*mm.Value, tags)
		}
		results = append(results, ops.RemoveMethod(m.Node, feature.Format(mm.Name, tags), val))
	}
	for i := range step.AddIO {
		io := &step.AddIO[i]
		attrs := make([]xmldoc.Attr, 0, 4)
		for _, a := range io.AttrPairs() {
			attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
		}
		bd := feature.Format(io.Bd.Value, tags)
		if bd == "auto" {
			bd = inferBd(m.Node, chamber)
		}
		children := []xmldoc.Attr{{Name: "Bd", Value: bd}, {Name: "Ch", Value: feature.Format(io.Ch.Value, tags)}}
		if io.Min.Kind != 0 {
			children = append(children, xmldoc.Attr{Name: "Min", Value: feature.Format(io.Min.Value, tags)})
		}
		if io.Max.Kind != 0 {
			children = append(children, xmldoc.Attr{Name: "Max", Value: feature.Format(io.Max.Value, tags)})
		}
		if io.Accuracy.Kind != 0 {
			children = append(children, xmldoc.Attr{Name: "Accuracy", Value: feature.Format(feature.ScalarText(&io.Accuracy), tags)})
		}
		if desc := feature.Format(io.Descriptors(), tags); desc != "" {
			children = append(children, xmldoc.Attr{Name: "DescriptorList", Value: desc})
		}
		if io.Unit != nil {
			children = append(children, xmldoc.Attr{Name: "Unit", Value: feature.Format(*io.Unit, tags)})
		}
		entity := ""
		if io.IncludeEntity != "" {
			entity = resolveEntity(io.IncludeEntity)
		}
		before := resolveBefore(m.Node, io.Before, false, tags)
		if before == nil && io.After != nil {
			before = resolveBefore(m.Node, io.After, true, tags)
		}
		results = append(results, ops.AddIO(m.Node, feature.Format(io.Name, tags), attrs, children, entity, before))
	}

	for _, r := range results {
		mark := "-"
		if r.Changed {
			mark = "+"
		}
		log(fmt.Sprintf("  步[%s] %s: %s %s", step.Name, inst, mark, r.Message))
		if r.Edit != nil {
			tg.edits = append(tg.edits, *r.Edit)
		}
		for i := range r.Edits {
			tg.edits = append(tg.edits, r.Edits[i])
		}
	}
}

// indentBlock 给多行XML原文的每一行加上 depth 层缩进(4 空格/层)，首尾空行去掉。
func indentBlock(raw string, depth int) string {
	pad := strings.Repeat("    ", depth)
	lines := strings.Split(strings.Trim(raw, "\r\n"), "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = pad + strings.TrimLeft(l, " \t")
	}
	return strings.Join(lines, "\n")
}

// formatAttrs 把 attr 映射做占位符替换。
func formatAttrs(attrs map[string]string, tags map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		out[k] = feature.Format(v, tags)
	}
	return out
}
