// Package engine —— 把一个 feature 的 steps 应用到各腔室，产出语义 diff、原地落盘。
//
// 编排(照搬 Python engine.py)：
//
//	遍历 Control 片段(每 = 一腔室，实体保留式加载) →
//	  按 steps 顺序执行(共享同一棵内存树，故步2能看到步1刚建的 <IonGauge>) →
//	    每步 anchor 定位(leaf 多实例则 fan-out) →
//	      每个实例：并入腔室级绑定 → require/bind → 执行动作(幂等) →
//	该腔室有改动才回写它自己的片段(保留 &实体; 与原格式；master/其它片段不动)。
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
	controlMaster string
	driverMaster  string // Driver_config.xml(根 <IOBridge>)；不存在时 IOBridge 步骤跳过
	ioMaster      string // IO_config.xml(根 <IO>)；不存在时 IO 步骤跳过
	idx           config.Indexes
	declared      []string
}

// New 用 configDir(按当前工作目录解析，见 GO_PORT_PLAN.md §7.3) 构造引擎。
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
	return &Engine{controlMaster: controlMaster, driverMaster: driverMaster, ioMaster: ioMaster, idx: idx, declared: declared}, nil
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

// ApplyFeature 对选定腔室(nil=全部)应用 feature；write=true 才落盘。
// 每个腔室同时持有 Control 与 IOBridge(Driver) 两个域的片段，按 anchor 首段路由；
// 跨域共享 chamberBinds(如 Control 侧绑定的 {ITO}=Ch1 供 IOBridge 步骤的 ${ITO} 使用)。
func (e *Engine) ApplyFeature(f *feature.Feature, selected []string, write bool, log func(string)) error {
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
		chamberBinds := map[string]string{}
		if cls := croot.Class(); cls != "" {
			chamberBinds[cls] = chamber
		}
		for i := range f.Steps {
			e.runStep(&f.Steps[i], doms, chamberBinds, chamber, log)
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
	var parts []string
	for _, p := range strings.Split(a, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	domain := "Control"
	if len(parts) > 0 && isDomain(parts[0]) {
		domain = parts[0]
		parts = parts[1:]
	}
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
	return domain, segs
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
	var where *anchor.Where
	if step.Where != nil {
		where = &anchor.Where{TagGlob: step.Where.TagGlob, HasGlob: step.Where.TagGlob != "", Attr: step.Where.Attr}
	}
	// 逐 root 解析后汇总(片段可含同名 tag 的多个顶层节点)。
	var matches []anchor.Match
	for _, root := range tg.roots {
		matches = append(matches, anchor.Resolve(root, segs, where)...)
	}
	croot := tg.roots[0] // 腔室根:供 include-entity 实体解析(Control 域 1 腔室 1 root)
	if len(matches) == 0 {
		log(fmt.Sprintf("  步[%s] 跳过：anchor %s 无匹配", step.Name, step.Anchor))
		return
	}

	for _, m := range matches {
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
			continue
		}
		for _, n := range notes {
			where := "?"
			if n.Path != "" {
				where = filepath.Base(n.Path)
			}
			log(fmt.Sprintf("  步[%s] %s: %s 命中于 %s（绑定 {%s}）", step.Name, inst, n.Logical, where, n.Var))
		}

		var results []ops.Result
		for _, nd := range step.AddNode {
			attrs := make([]xmldoc.Attr, 0, 4)
			for _, a := range nd.AttrPairs() {
				attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
			}
			results = append(results, ops.AddNode(m.Node, nd.Tag, nd.Class, attrs))
			chamberBinds[nd.Tag] = "./" + nd.Tag // 登记对象引用，供后续步骤 {标签}

			if nd.IncludeEntity != "" {
				target := xmldoc.FindChild(m.Node, nd.Tag) // 新建或既有目标
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
			children := make([]xmldoc.Attr, 0, 5)
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
			results = append(results, ops.AddData(m.Node, d.Name, attrs, children))
		}
		// where.before-method：把本步新增方法插到该既有方法之前(而非追加末尾)。
		var before *xmldoc.Node
		if step.Where != nil && step.Where.BeforeMethod != nil {
			name := step.Where.BeforeMethod.Name
			if before = xmldoc.FindChild(m.Node, name); before == nil {
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
			results = append(results, ops.AddMethod(m.Node, mm.Name, val, hasVal, extra, before))
		}
		for _, mm := range step.RemoveMethod {
			val := ""
			if mm.Value != nil {
				val = feature.Format(*mm.Value, tags)
			}
			results = append(results, ops.RemoveMethod(m.Node, mm.Name, val))
		}
		for i := range step.AddIO {
			io := &step.AddIO[i]
			attrs := make([]xmldoc.Attr, 0, 4)
			for _, a := range io.AttrPairs() {
				attrs = append(attrs, xmldoc.Attr{Name: a.Name, Value: feature.Format(a.Value, tags)})
			}
			bd := io.Bd.Value
			if bd == "auto" {
				bd = inferBd(m.Node, chamber)
			}
			children := []xmldoc.Attr{{Name: "Bd", Value: bd}, {Name: "Ch", Value: io.Ch.Value}}
			if io.Min.Kind != 0 {
				children = append(children, xmldoc.Attr{Name: "Min", Value: io.Min.Value})
			}
			if io.Max.Kind != 0 {
				children = append(children, xmldoc.Attr{Name: "Max", Value: io.Max.Value})
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
			simEntity := ""
			if io.HasAttr("simulated") {
				simEntity = "Simulated_" + chamber
			}
			results = append(results, ops.AddIO(m.Node, io.Name, attrs, children, simEntity))
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
		}
	}
}
