package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
	"addex/internal/setupcheck"
)

const setupDoc = `<Ch1Setup comments="config parameters for Ch1 Chamber">
  <Param name="MagnetRotateSpeed" dataObject="/SETUP/Control/Ch1/Source/MagnetRotateSpeed" type="I" min="0" max="100" descriptorList="" units="r/min" default="60"/>
  <Option index="1">
    <Value paramName="MagnetRotateSpeed">60</Value>
  </Option>
</Ch1Setup>
`

// TestAddSetupPrimitive 端到端覆盖 add-setup：无 anchor(缺省=文件根) → Param/Value 成对追加到
// 各自序列末尾、plan 不落盘、二次 apply 幂等、产物通过 Setup 一致性校验。
func TestAddSetupPrimitive(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")
	setupPath := filepath.Join(work, "config", "Setup", "Setup_Ch1.xml")
	writeFile(t, setupPath, setupDoc)

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: setup-demo
version: 1
steps:
  - name: Setup/Setup_Ch1.xml 升级
    file: Setup/Setup_Ch1.xml
    add-setup:
      - param: SourceDCCurrentMax
        dataObject: /SETUP/Control/Ch1/Source/SourceDC/CurrentOutputMaxPercent
        type: D
        min: 0
        max: 100
        units: "%"
        accuracy: 0.1
        default: 70
        value: 70
`)
	feat, err := feature.Load(featPath)
	if err != nil {
		t.Fatal(err)
	}

	// plan：只打印 diff，不落盘。
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, false, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(setupPath); string(got) != setupDoc {
		t.Fatalf("plan 模式不应写盘:\n%s", got)
	}

	// apply：成对追加，Param 落 Param 序列末尾、Value 落 Option 末尾。
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ApplyFeature(feat, nil, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	wantParam := `<Param name="SourceDCCurrentMax" dataObject="/SETUP/Control/Ch1/Source/SourceDC/CurrentOutputMaxPercent"` +
		` type="D" min="0" max="100" units="%" accuracy="0.1" default="70"/>`
	if !strings.Contains(out, wantParam) {
		t.Fatalf("Param 未按规范顺序追加:\n%s", out)
	}
	if !strings.Contains(out, `<Value paramName="SourceDCCurrentMax">70</Value>`) {
		t.Fatalf("Value 未追加:\n%s", out)
	}
	iOldParam := strings.Index(out, `name="MagnetRotateSpeed"`)
	iNewParam := strings.Index(out, `name="SourceDCCurrentMax"`)
	iOption := strings.Index(out, "<Option")
	if !(0 <= iOldParam && iOldParam < iNewParam && iNewParam < iOption) {
		t.Fatalf("Param 追加位置不对(应在既有 Param 之后、<Option> 之前):\n%s", out)
	}
	iOldVal := strings.Index(out, `<Value paramName="MagnetRotateSpeed">`)
	iNewVal := strings.Index(out, `<Value paramName="SourceDCCurrentMax">`)
	iClose := strings.Index(out, "</Option>")
	if !(0 <= iOldVal && iOldVal < iNewVal && iNewVal < iClose) {
		t.Fatalf("Value 追加位置不对(应在既有 Value 之后、</Option> 之前):\n%s", out)
	}

	// 一致性闸门：Param/Value 按下标一一同名。
	issues, err := setupcheck.CheckFile(setupPath, "Setup/Setup_Ch1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("Setup 一致性校验不应有问题: %+v", issues)
	}

	// 二次 apply：逐字节幂等，且报告 no-op。
	before := out
	eng, err = New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := eng.ApplyFeature(feat, nil, true, func(s string) { lines = append(lines, s) }); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(setupPath); string(after) != before {
		t.Fatalf("二次 apply 不幂等:\n%s", after)
	}
	if logged := strings.Join(lines, "\n"); !strings.Contains(logged, "- Setup 参数 SourceDCCurrentMax 及其取值已存在") {
		t.Fatalf("二次 apply 应报告 no-op，实得:\n%s", logged)
	}
}

// TestAddSetupSpecAttrOrder 校验 <Param> 属性的规范顺序与 attrs 覆盖：
// type S 的 maxLength 落在 type 之后、default 之前；attrs 同名键覆盖便捷字段。
func TestAddSetupSpecAttrOrder(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "config", "Control", "Control_config.xml"), "<Control></Control>")
	writeFile(t, filepath.Join(work, "config", "IO_config.xml"), "<IO></IO>")
	setupPath := filepath.Join(work, "config", "Setup", "S.xml")
	writeFile(t, setupPath, "<S>\n  <Option index=\"1\"></Option>\n</S>\n")

	featPath := filepath.Join(work, "up.yaml")
	writeFile(t, featPath, `id: order
version: 1
steps:
  - name: 加字符串参数
    file: Setup/S.xml
    add-setup:
      - param: SpeedSetting
        dataObject: /SETUP/SpeedSetting
        type: S
        maxLength: 100
        default: ""
        value: ""
        attrs: { descriptorList: "" }
`)
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
	raw, _ := os.ReadFile(setupPath)
	out := string(raw)
	want := `<Param name="SpeedSetting" dataObject="/SETUP/SpeedSetting" type="S" maxLength="100" descriptorList="" default=""/>`
	if !strings.Contains(out, want) {
		t.Fatalf("Param 属性顺序/覆盖不符:\n%s", out)
	}
	if !strings.Contains(out, `<Value paramName="SpeedSetting"></Value>`) {
		t.Fatalf("空取值应渲染成成对空标签:\n%s", out)
	}
}
