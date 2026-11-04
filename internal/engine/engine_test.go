package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// chamberSpec 描述最小夹具里的一个腔室：Control_<Tag> 片段由 writeChamberConfig 生成。
type chamberSpec struct {
	Tag      string   // 腔室标签(同时用作片段文件名 Control_<Tag> 与腔室名)
	Class    string   // 根元素 class；空表示不写 class 属性
	Body     string   // 根元素内的正文(可为空)
	Entities []string // 需在 Control_config.xml DTD 额外声明的实体名(供 include-entity 解析)
}

// writeChamberConfig 在 t.TempDir() 下构造一份最小 config/，只含调用方声明的腔室：
//
//	config/Control/Control_config.xml   DTD 按 specs 声明并引用各 Control_<Tag> 片段
//	config/Control/Control_<Tag>        每个腔室一个片段
//	config/IO_config.xml                空 IO master
//
// 这样用例不再依赖仓库根 config/ 现场夹具(该目录被 .gitignore 忽略、不入库)，
// 任何环境都能复现。返回 config 目录。
func writeChamberConfig(t *testing.T, specs ...chamberSpec) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	var dtd, refs strings.Builder
	dtd.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<!DOCTYPE Control [\n")
	declared := map[string]bool{}
	for _, s := range specs {
		fmt.Fprintf(&dtd, "<!ENTITY Ctrl_%s SYSTEM \"./Control_%s\">\n", s.Tag, s.Tag)
		for _, e := range s.Entities {
			if declared[e] {
				continue
			}
			declared[e] = true
			fmt.Fprintf(&dtd, "<!ENTITY %s SYSTEM \"./%s\">\n", e, e)
		}
		fmt.Fprintf(&refs, "    &Ctrl_%s;\n", s.Tag)
		class := ""
		if s.Class != "" {
			class = fmt.Sprintf(" class=%q", s.Class)
		}
		frag := fmt.Sprintf("<%s%s type=\"instance\">\n%s</%s>\n", s.Tag, class, s.Body, s.Tag)
		writeFile(t, filepath.Join(dir, "Control", "Control_"+s.Tag), frag)
	}
	dtd.WriteString("]>\n<Control>\n")
	writeFile(t, filepath.Join(dir, "Control", "Control_config.xml"), dtd.String()+refs.String()+"</Control>\n")
	writeFile(t, filepath.Join(dir, "IO_config.xml"), "<IO></IO>\n")
	return dir
}

// degasConfig 是占位符 / include-entity 类用例的最小夹具：单腔室 ChC(class Degas)，
// 内含 class=PhyHeater 的 Heater 实例及这些用例涉及的既有方法/子节点；master DTD 里
// 另声明 SimulatedFlag_ChC 供 include-entity 的 glob 解析。
func degasConfig(t *testing.T) string {
	t.Helper()
	const body = `    &SimulatedFlag_ChC;

    <Heater class="PhyHeater" type="instance" alias="/Control/ChCExports/Heater">
        <setTemperatureSp type="method">/IO/Platform/Heater_ChC/ClosedLoopAO</setTemperatureSp>
        <TempB4OffsetVp type="method" dataType="D" alias="/IO/ChCExports/Heater_TempB4Offset">
            <Bd></Bd>
        </TempB4OffsetVp>
        <exportWarningTempLow type="method">/IO/ChCExports/WarningTempLow</exportWarningTempLow>
        <setTimeIntegralSp type="method">/IO/Platform/Heater_ChC/TimeIntegral</setTimeIntegralSp>
    </Heater>

    <TChC class="PhyPlaceholderProbe" type="instance" id="ChC">
        <seed type="method">1</seed>
    </TChC>

    <!~ChC-uc-ChC-ChC~>
`
	return writeChamberConfig(t, chamberSpec{
		Tag:      "ChC",
		Class:    "Degas",
		Body:     body,
		Entities: []string{"SimulatedFlag_ChC"},
	})
}
