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
	"strings"

	"addex/internal/anchor"
	"addex/internal/config"
	"addex/internal/feature"
	"addex/internal/ops"
	"addex/internal/splice"
	"addex/internal/xmldoc"
)

// Engine 持有只读逻辑索引与实体声明(构建一次，跨腔室复用)。
type Engine struct {
	controlMaster string
	idx           config.Indexes
	declared      []string
}

// New 用 configDir(按当前工作目录解析，见 GO_PORT_PLAN.md §7.3) 构造引擎。
func New(configDir string) (*Engine, error) {
	ioMaster := filepath.Join(configDir, "IO_config.xml")
	controlMaster := filepath.Join(configDir, "Control", "Control_config.xml")
	idx, err := config.BuildIndexes(ioMaster, controlMaster)
	if err != nil {
		return nil, err
	}
	declared, err := config.DeclaredEntities(controlMaster)
	if err != nil {
		return nil, err
	}
	return &Engine{controlMaster: controlMaster, idx: idx, declared: declared}, nil
}

// ApplyFeature 对选定腔室(nil=全部)应用 feature；write=true 才落盘。
func (e *Engine) ApplyFeature(f *feature.Feature, selected []string, write bool, log func(string)) error {
	frags, err := config.FragmentFiles(e.controlMaster)
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
		chamberBinds := map[string]string{}
		var edits []xmldoc.Edit
		for i := range f.Steps {
			e.runStep(&f.Steps[i], croot, chamberBinds, &edits, log)
		}

		if len(edits) > 0 && write {
			out := splice.Apply(doc.Src, edits)
			if err := os.WriteFile(fr.Path, out, 0o644); err != nil {
				return err
			}
			log(fmt.Sprintf("[%s] ✎ 已写回 %s（%d 处编辑，其余字节不动）", chamber, filepath.Base(fr.Path), len(edits)))
		}
	}
	return nil
}

func (e *Engine) runStep(step *feature.Step, croot *xmldoc.Node, chamberBinds map[string]string, edits *[]xmldoc.Edit, log func(string)) {
	var classPath []string
	for _, c := range strings.Split(step.Anchor, "/") {
		if c != "" {
			classPath = append(classPath, c)
		}
	}
	var where *anchor.Where
	if step.Where != nil {
		where = &anchor.Where{TagGlob: step.Where.TagGlob, HasGlob: step.Where.TagGlob != "", Attr: step.Where.Attr}
	}
	matches := anchor.Resolve(croot, classPath, where)
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
					log(fmt.Sprintf("  步[%s] %s: ⚠ include-entity 未在 Control_config.xml 找到匹配 %s 的实体",
						step.Name, inst, feature.Format(nd.IncludeEntity, tags)))
				}
			}
		}
		for _, mm := range step.AddMethod {
			val := ""
			hasVal := mm.Value != nil
			if hasVal {
				val = feature.Format(*mm.Value, tags)
			}
			results = append(results, ops.AddMethod(m.Node, mm.Name, val, hasVal))
		}
		for _, mm := range step.RemoveMethod {
			val := ""
			if mm.Value != nil {
				val = feature.Format(*mm.Value, tags)
			}
			results = append(results, ops.RemoveMethod(m.Node, mm.Name, val))
		}

		for _, r := range results {
			mark := "·"
			if r.Changed {
				mark = "✎"
			}
			log(fmt.Sprintf("  步[%s] %s: %s %s", step.Name, inst, mark, r.Message))
			if r.Edit != nil {
				*edits = append(*edits, *r.Edit)
			}
		}
	}
}
