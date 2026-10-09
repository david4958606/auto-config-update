package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"addex/internal/feature"
	"addex/internal/xmldoc"
)

// 两步文件补丁必须在 plan/apply 中做出相同判定，且方法插入字节幂等。
func TestFileStepsShareVirtualBytes(t *testing.T) {
	for _, child := range []string{"<Child><seed/></Child>", "<Child/>"} {
		for _, pattern := range []string{"X.xml", "*.xml"} {
			t.Run(child+pattern, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "X.xml")
				original := "<X>\n</X>\n"
				writeFile(t, path, original)
				yaml := "id: file-state\nversion: 1\nsteps:\n" +
					"  - name: create-child\n    file: X.xml\n    anchor: X\n    add-xml:\n      - xml: '" + child + "'\n" +
					"  - name: add-method\n    file: '" + pattern + "'\n    anchor: X/Child\n    add-method:\n      - { name: Run, value: '1' }\n"
				fp := filepath.Join(dir, "feature.yaml")
				writeFile(t, fp, yaml)
				f, err := feature.Load(fp)
				if err != nil {
					t.Fatal(err)
				}
				e := &Engine{configDir: dir}
				run := func(write bool) []string {
					t.Helper()
					var semantic []string
					err := e.ApplyFeature(f, nil, write, func(s string) {
						if strings.HasPrefix(s, "  步[") {
							semantic = append(semantic, s)
						}
					})
					if err != nil {
						t.Fatal(err)
					}
					return semantic
				}
				plan := run(false)
				if raw, err := os.ReadFile(path); err != nil || string(raw) != original {
					t.Fatalf("plan 修改了磁盘: %q %v", raw, err)
				}
				applied := run(true)
				if !reflect.DeepEqual(plan, applied) {
					t.Fatalf("plan/apply 不一致:\n%v\n%v", plan, applied)
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				doc, err := xmldoc.Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				c := xmldoc.FindChild(doc.Roots[0], "Child")
				if c == nil || xmldoc.FindChild(c, "Run") == nil {
					t.Fatalf("第二步未新增方法:\n%s", raw)
				}
				// add-xml 按完整结构判重；Child 后续被扩展后不再等于原片段。
				// 此处验证第二步重复执行不会重复方法，不改变 add-xml 的既有判重契约。
				f.Steps = f.Steps[1:]
				run(true)
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(raw) {
					t.Fatalf("二次 apply 不幂等:\n%s\n%v", after, err)
				}
			})
		}
	}
}

func TestNewFileVisibleToLaterSteps(t *testing.T) {
	for _, pattern := range []string{"new/X.xml", "new/*.xml"} {
		t.Run(pattern, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "feature.yaml")
			writeFile(t, fp, "id: new-state\nversion: 1\nsteps:\n  - name: create\n    new-file: new/X.xml\n    content: '<X/>'\n  - name: duplicate\n    new-file: new/X.xml\n    content: 'DO NOT OVERWRITE'\n  - name: method\n    file: '"+pattern+"'\n    anchor: X\n    add-method:\n      - { name: Run, value: '1' }\n")
			f, err := feature.Load(fp)
			if err != nil {
				t.Fatal(err)
			}
			e := &Engine{configDir: dir}
			var logs []string
			if err := e.ApplyFeature(f, nil, false, func(s string) { logs = append(logs, s) }); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "new", "X.xml")
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("plan 创建了目录: %v", err)
			}
			joined := strings.Join(logs, "\n")
			if !strings.Contains(joined, "已存在") || !strings.Contains(joined, "步[method] X(class=): +") {
				t.Fatalf("虚拟新文件未生效:\n%s", joined)
			}
			if err := e.ApplyFeature(f, nil, true, func(string) {}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(raw), "<Run") {
				t.Fatalf("apply 失败: %s %v", raw, err)
			}
		})
	}
}

// 分组并非全局声明顺序：各腔室先普通步骤，再模板文件步骤，最后全局文件步骤。
func TestStepExecutionGroupOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Control", "Control_config.xml"), "<!DOCTYPE Control [<!ENTITY Ch1 SYSTEM \"Control_Ch1\">]><Control>&Ch1;</Control>")
	writeFile(t, filepath.Join(dir, "Control", "Control_Ch1"), "<Ch1 class=\"Chamber\">\n</Ch1>\n")
	writeFile(t, filepath.Join(dir, "IO_config.xml"), "<IO/>")
	writeFile(t, filepath.Join(dir, "Shared.xml"), "<X>\n</X>\n")
	writeFile(t, filepath.Join(dir, "Ch1.xml"), "<X>\n</X>\n")
	fp := filepath.Join(dir, "feature.yaml")
	writeFile(t, fp, `id: group-order
version: 1
steps:
  - name: global-1
    file: Shared.xml
    add-method: [{ name: G1 }]
  - name: template-1
    file: '${Chamber}.xml'
    add-method: [{ name: T1 }]
  - name: chamber-1
    anchor: Control/Chamber
    add-method: [{ name: C1 }]
  - name: global-2
    file: Shared.xml
    add-method: [{ name: G2 }]
  - name: template-2
    file: '${Chamber}.xml'
    add-method: [{ name: T2 }]
  - name: chamber-2
    anchor: Control/Chamber
    add-method: [{ name: C2 }]
`)
	f, err := feature.Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	for _, write := range []bool{false, true} {
		e, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		if err := e.ApplyFeature(f, nil, write, func(s string) {
			if strings.HasPrefix(s, "  步[") {
				names = append(names, strings.SplitN(strings.TrimPrefix(s, "  步["), "]", 2)[0])
			}
		}); err != nil {
			t.Fatal(err)
		}
		want := []string{"chamber-1", "chamber-2", "template-1", "template-2", "global-1", "global-2"}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("分组顺序不符: %v", names)
		}
	}
}
