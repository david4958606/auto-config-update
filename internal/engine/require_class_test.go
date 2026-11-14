package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"addex/internal/feature"
)

func TestRequireClassOtherControl(t *testing.T) {
	dir := writeChamberConfig(t,
		chamberSpec{Tag: "Ch1", Class: "PVD", Body: `<Wrap><First class="FuncHeater"/><Heater class="FuncHeater"><PhyHeater3/></Heater></Wrap><Target class="OtherControl"/><Target2 class="OtherControl"/>`},
		chamberSpec{Tag: "Ch2", Class: "PVD", Body: `<Heater class="FuncHeater"/><Target class="OtherControl"/>`},
	)
	// 一个 IO 片段可含多个腔室根；工作索引替换不能丢失同文件其他根。
	writeFile(t, filepath.Join(dir, "IO_config.xml"), `<!DOCTYPE IO [<!ENTITY Shared SYSTEM "./IO_shared">]><IO>&Shared;</IO>`)
	writeFile(t, filepath.Join(dir, "IO_shared"), `<Ch1><Point/></Ch1><Ch2><Sensor class="SensorClass"><Max>100</Max></Sensor></Ch2>`)
	fp := filepath.Join(filepath.Dir(dir), "class.yaml")
	writeFile(t, fp, `id: require-class
version: 1
steps:
  - name: guarded
    anchor: /Control/{PVD}/{OtherControl}
    require:
      - exist: { Heater3: "/Control/{PVD}/{FuncHeater}/PhyHeater3" }
    add-method:
      - { name: setRef, value: "${Heater3}" }
  - name: shared-io-root
    anchor: /Control/{PVD}/{OtherControl}
    require:
      - exist: { SharedMax: "/IO/Ch2/{SensorClass}/Max" }
    add-method:
      - { name: setSharedMax, value: "${SharedMax}" }
  - name: create-later
    anchor: /Control/{PVD}
    add-node:
      - { tag: Later, class: LaterClass }
  - name: see-later
    anchor: /Control/{PVD}/{OtherControl}
    require:
      - exist: { Ref: "/Control/${Chamber}/{LaterClass}" }
    add-method:
      - { name: setLater, value: "${Ref}" }
`)
	f, err := feature.Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(dir, "Control", "Control_Ch1"), filepath.Join(dir, "Control", "Control_Ch2")}
	readAll := func() []string {
		t.Helper()
		var out []string
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, string(raw))
		}
		return out
	}
	run := func(write bool) []string {
		t.Helper()
		e, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}
		var logs []string
		if err := e.ApplyFeature(f, nil, write, func(s string) {
			if strings.HasPrefix(s, "  步[") {
				logs = append(logs, s)
			}
		}); err != nil {
			t.Fatal(err)
		}
		return logs
	}
	original := readAll()
	plan := run(false)
	if !reflect.DeepEqual(original, readAll()) {
		t.Fatal("plan 不应写盘")
	}
	applied := run(true)
	if !reflect.DeepEqual(plan, applied) {
		t.Fatalf("plan/apply 日志不一致：%v / %v", plan, applied)
	}
	after := readAll()
	if strings.Count(after[0], `<setRef type="method">/Control/Ch1/Wrap/Heater/PhyHeater3</setRef>`) != 2 || strings.Contains(after[1], "setRef") {
		t.Fatalf("跨节点守卫/多实例/腔室隔离错误：%v", after)
	}
	if strings.Count(after[0], `<setSharedMax type="method">/IO/Ch2/Sensor/Max</setSharedMax>`) != 2 {
		t.Fatalf("共享 IO 文件的其他根不可丢失：%s", after[0])
	}
	for i, tag := range []string{"Ch1", "Ch2"} {
		if !strings.Contains(after[i], `<setLater type="method">/Control/`+tag+`/Later</setLater>`) {
			t.Fatalf("后一步看不到前一步新建 class 节点：%s", after[i])
		}
	}
	run(true)
	if !reflect.DeepEqual(after, readAll()) {
		t.Fatal("重复 apply 不幂等")
	}
}
