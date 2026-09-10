package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// TestPlaceholdersAllPrimitives 逐原语验证 `{X}` 与 `${X}` 两种写法都被替换。
//
// 语法（与 features/ig-auto-close.yaml 的注释一致）：
//   - anchor 段：`{X}` = 按 class 匹配并把 X 绑定为该实例标签；`${X}` = 按标签名匹配（X 需已绑定，
//     如腔室 class 绑定 / require / bind）；
//   - 其余所有取值字段（tag/class/attrs/xml/text/value/old/name/child/where/open/close/find…）：
//     两者**同解**，都替换为 tags[X]。
//
// 本用例把 Demo 配置的 Degas 腔室（ChC/ChD）当靶子，两种写法混用，最后断言：期望的替换结果都
// 出现，且产物里**不残留**任何 `{Degas}` / `${Degas}`。
func TestPlaceholdersAllPrimitives(t *testing.T) {
	work := t.TempDir()
	copyTree(t, "../../config", filepath.Join(work, "config"))

	// 给 Control_ChC 注入：一个带 class/id 的探针节点（测 where 的 tag-glob/attr 占位符），
	// 以及一段自定义标记的"注释块"（测 uncomment 的 find/open/close 占位符）。
	target := filepath.Join(work, "config", "Control", "Control_ChC")
	src, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	injected := strings.Replace(string(src), "</ChC>",
		"    <TChC class=\"PhyPlaceholderProbe\" type=\"instance\" id=\"ChC\">\n"+
			"        <seed type=\"method\">1</seed>\n"+
			"    </TChC>\n"+
			"    <!~ChC-uc-ChC-ChC~>\n"+
			"</ChC>", 1)
	if injected == string(src) {
		t.Fatal("夹具注入失败：未找到 </ChC>")
	}
	if err := os.WriteFile(target, []byte(injected), 0o644); err != nil {
		t.Fatal(err)
	}

	// 两种写法刻意混用：有的字段用 {X}，有的用 ${X}。
	featYAML := `id: placeholder-probe
version: 1
steps:
  - name: where 占位符（tag-glob / attr）
    anchor: /Control/{Degas}/{PhyPlaceholderProbe}
    where: { tag-glob: "T{Degas}", attr: { id: "${Degas}" } }
    add-method:
      - { name: "hit{Degas}", value: "/IO/${Degas}/hit" }

  - name: add-node 占位符（tag / class / attrs）
    anchor: /Control/{Degas}
    add-node:
      - tag: NewObj${Degas}
        class: Cls{Degas}
        attrs: { alias: "/Control/${Degas}Exports/NewObj", note: "n-{Degas}" }

  - name: add-element 占位符（tag / attrs / text / after）
    anchor: /Control/{Degas}
    add-element:
      - tag: El{Degas}
        attrs: { k: "v-${Degas}" }
        text: "t-{Degas}"
        after: { tag: "NewObj{Degas}" }

  - name: add-xml 占位符
    anchor: /Control/{Degas}
    add-xml:
      - xml: "<Xml{Degas} a=\"${Degas}\"><child type=\"method\">c-{Degas}</child></Xml${Degas}>"

  - name: add-method / remove-method 占位符（name / value / attrs）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    add-method:
      - name: "setP{Degas}"
        value: "/IO/${Degas}/P"
        attrs: { comment: "cm-{Degas}" }
    remove-method:
      - name: setTemperatureSp
        value: "/IO/Platform/Heater_${Degas}/ClosedLoopAO"

  - name: set-text 占位符（child / value）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    bind:
      - B: Bd
    set-text:
      - { tag: TempB4OffsetVp, child: "{B}", old: "", value: "42-${Degas}" }

  - name: set-attr 占位符（value）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    set-attr:
      - { tag: TempB4OffsetVp, attr: { dataType: "D" }, name: alias, old: "/IO/ChCExports/Heater_TempB4Offset", value: "/IO/${Degas}Exports/renamed-{Degas}" }

  - name: remove-node 占位符（tag / value）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    bind:
      - W: Low
    remove-node:
      - { tag: "exportWarningTemp{W}", value: "/IO/${Degas}Exports/WarningTempLow" }

  - name: add-io 占位符（name / attrs / Bd / Ch / Min / Max / Accuracy / DescriptorList / Unit）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    add-io:
      - name: Io{Degas}
        attrs: { dataType: "I", alias: "/IO/${Degas}Exports/Io" }
        Bd: "B{Degas}"
        Ch: "C${Degas}"
        Min: "min-{Degas}"
        Max: "max-${Degas}"
        Accuracy: "acc-{Degas}"
        DescriptorList:
          - { name: "D{Degas}", value: 0 }
        Unit: "u-{Degas}"

  - name: add-data 占位符（同上，首属性 type=data）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    add-data:
      - name: Data{Degas}
        attrs: { dataType: "D", alias: "/IO/${Degas}Exports/Data" }
        Bd: "B{Degas}"
        Ch: "C${Degas}"
        Min: "min-{Degas}"
        Max: "max-${Degas}"
        Accuracy: "acc-{Degas}"
        DescriptorList:
          - { name: "D{Degas}", value: 1 }
        Unit: "u-{Degas}"

  - name: add-comment 占位符
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    add-comment:
      - "<!--c-{Degas}-->"

  - name: wrap 占位符（open / close）
    anchor: /Control/{Degas}/{PhyHeater}
    where: { tag-glob: "Heater" }
    wrap:
      - open: "<!--w-{Degas}"
        close: "{Degas}-w-->"
        select: [ { tag: setTimeIntegralSp } ]

  - name: uncomment 占位符（find / open / close）
    anchor: /Control/{Degas}
    uncomment:
      - { find: "uc-{Degas}", open: "<!~{Degas}-", close: "-{Degas}~>" }
`
	featPath := filepath.Join(work, "probe.yaml")
	if err := os.WriteFile(featPath, []byte(featYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	xml := string(out)

	wants := []struct{ frag, why string }{
		{`<hitChC type="method">/IO/ChC/hit</hitChC>`, "where 占位符 + add-method name/value"},
		{`<NewObjChC class="ClsChC"`, "add-node tag/class"},
		{`alias="/Control/ChCExports/NewObj" note="n-ChC"`, "add-node attrs"},
		{`<ElChC k="v-ChC">t-ChC</ElChC>`, "add-element tag/attrs/text/after"},
		{`<XmlChC a="ChC"><child type="method">c-ChC</child></XmlChC>`, "add-xml"},
		{`<setPChC type="method" comment="cm-ChC">/IO/ChC/P</setPChC>`, "add-method name/value/attrs"},
		{`<Bd>42-ChC</Bd>`, "set-text child/value"},
		{`alias="/IO/ChCExports/renamed-ChC"`, "set-attr value"},
		{`<IoChC dataType="I" alias="/IO/ChCExports/Io">`, "add-io name/attrs"},
		{`<Bd>BChC</Bd>`, "add-io Bd"},
		{`<Ch>CChC</Ch>`, "add-io Ch"},
		{`<Min>min-ChC</Min>`, "add-io Min"},
		{`<Max>max-ChC</Max>`, "add-io Max"},
		{`<Accuracy>acc-ChC</Accuracy>`, "add-io Accuracy"},
		{`<DescriptorList>DChC:0</DescriptorList>`, "add-io DescriptorList"},
		{`<Unit>u-ChC</Unit>`, "add-io Unit"},
		{`<DataChC type="data" dataType="D" alias="/IO/ChCExports/Data">`, "add-data name/attrs/type=data"},
		{`<DescriptorList>DChC:1</DescriptorList>`, "add-data DescriptorList"},
		{`<!--c-ChC-->`, "add-comment"},
		{`<!--w-ChC`, "wrap open"},
		{`ChC-w-->`, "wrap close"},
		{`uc-ChC`, "uncomment find/open/close（标记已去除、内容保留）"},
	}
	for _, w := range wants {
		if !strings.Contains(xml, w.frag) {
			t.Errorf("缺少 %s：%s\n%s", w.why, w.frag, excerpt(xml, "ChC"))
		}
	}

	// remove-method / remove-node 必须真的删掉。
	if strings.Contains(xml, "ClosedLoopAO") {
		t.Errorf("remove-method 未生效（setTemperatureSp 仍在）")
	}
	if strings.Contains(xml, "exportWarningTempLow") {
		t.Errorf("remove-node 未生效（exportWarningTempLow 仍在）")
	}
	// uncomment 的自定义标记必须被去掉、不能残留。
	if strings.Contains(xml, "<!~ChC-") || strings.Contains(xml, "-ChC~>") {
		t.Errorf("uncomment 未去掉自定义标记")
	}
	// 任何字段都不应残留占位符。
	for _, leftover := range []string{"{Degas}", "${Degas}"} {
		if strings.Contains(xml, leftover) {
			t.Errorf("产物残留占位符 %s\n%s", leftover, excerpt(xml, leftover))
		}
	}
}
