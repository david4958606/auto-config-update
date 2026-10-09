package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"addex/internal/config"
	"addex/internal/feature"
	"addex/internal/xmldoc"
)

// 使用最小配置验证 Control 守卫控制文件级 Setup 分支，且 plan/apply 一致、重复执行幂等。
func TestRequireSetupBranches(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(map[bool]string{true: "present", false: "absent"}[present], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Setup.xml")
			original := "<Setup>\n  <Option index=\"1\"></Option>\n</Setup>\n"
			writeFile(t, path, original)
			method := ""
			if present {
				method = `<setOffset type="method"/>`
			}
			doc, err := xmldoc.Parse([]byte(`<Ch1><Heater>` + method + `</Heater></Ch1>`))
			if err != nil {
				t.Fatal(err)
			}
			fp := filepath.Join(dir, "feature.yaml")
			writeFile(t, fp, `id: conditional-setup
version: 1
steps:
  - name: present
    file: Setup.xml
    require:
      - exist: { Method: "/Control/Ch1/Heater/setOffset" }
    add-setup:
      - { param: WithMethod, type: D, value: 1 }
  - name: absent
    file: Setup.xml
    require:
      - non-exist: { Method: "/Control/Ch1/Heater/setOffset" }
    add-setup:
      - { param: WithoutMethod, type: D, value: 0 }
`)
			f, err := feature.Load(fp)
			if err != nil {
				t.Fatal(err)
			}
			e := &Engine{configDir: dir, idx: config.Indexes{"Control": {{Path: "Control_Ch1", Root: doc.Roots[0]}}}}
			run := func(write bool) []string {
				t.Helper()
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
			plan := run(false)
			if raw, err := os.ReadFile(path); err != nil || string(raw) != original {
				t.Fatalf("plan 修改磁盘：%s %v", raw, err)
			}
			applied := run(true)
			if !reflect.DeepEqual(plan, applied) {
				t.Fatalf("plan/apply 不一致：%v / %v", plan, applied)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, unwanted := "WithMethod", "WithoutMethod"
			if !present {
				want, unwanted = unwanted, want
			}
			if !strings.Contains(string(raw), `name="`+want+`"`) || !strings.Contains(string(raw), `paramName="`+want+`"`) || strings.Contains(string(raw), unwanted) {
				t.Fatalf("Setup 分支或参数对错误：%s", raw)
			}
			run(true)
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(raw) {
				t.Fatalf("重复 apply 不幂等：%s %v", after, err)
			}
		})
	}
}
