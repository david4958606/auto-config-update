package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout 在 f 执行期间把 os.Stdout 重定向到管道，返回其输出。
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	w.Close()
	os.Stdout = old
	return <-done
}

// TestRunSwitchCLI 校验 switch 子命令的接线：目标值校验、切换落盘、幂等。
func TestRunSwitchCLI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "SimulatedFlag_Ch1")
	if err := os.WriteFile(p, []byte(`<setSimulated type="method">false</setSimulated>`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 参数校验：缺值 / 非法值都返回 2，且不碰文件。
	if code := runSwitch(dir, nil); code != 2 {
		t.Fatalf("缺值应返回 2，得到 %d", code)
	}
	if code := runSwitch(dir, []string{"maybe"}); code != 2 {
		t.Fatalf("非法值应返回 2，得到 %d", code)
	}

	out := captureStdout(t, func() {
		if code := runSwitch(dir, []string{"true"}); code != 0 {
			t.Errorf("switch true 应返回 0，得到 %d", code)
		}
	})
	if !strings.Contains(out, "改写 1") {
		t.Errorf("输出应报告改写 1 个文件：\n%s", out)
	}
	if b, _ := os.ReadFile(p); string(b) != `<setSimulated type="method">true</setSimulated>` {
		t.Errorf("文件未切到 true：%q", b)
	}

	// 幂等：第二次不再写盘。
	out = captureStdout(t, func() {
		if code := runSwitch(dir, []string{"true"}); code != 0 {
			t.Errorf("二次 switch 应返回 0，得到 %d", code)
		}
	})
	if !strings.Contains(out, "改写 0") {
		t.Errorf("二次运行应为 no-op：\n%s", out)
	}
}

// TestVerifySetupGate 校验 CLI 的硬闸门：Setup 里 Param/Value 笔误必须让 verifySetup 返回 1。
func TestVerifySetupGate(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "config", "Setup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 复现 GasFlowCompens 的笔误：声明 AlONG...，取值 AlOG...。
	bad := `<X>
  <Param name="AlONGasFlowPieceCompens" type="S" />
  <Option index="1"><Value paramName="AlOGasFlowPieceCompens">0</Value></Option>
</X>`
	if err := os.WriteFile(filepath.Join(dir, "Bad.xml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := verifySetup(filepath.Join(work, "config"), true); code != 1 {
		t.Fatalf("笔误应返回非零退出码，得到 %d", code)
	}
	// 一致的文件必须通过。
	if err := os.Remove(filepath.Join(dir, "Bad.xml")); err != nil {
		t.Fatal(err)
	}
	good := `<X>
  <Param name="A" type="S" />
  <Option index="1"><Value paramName="A">0</Value></Option>
</X>`
	if err := os.WriteFile(filepath.Join(dir, "Good.xml"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := verifySetup(filepath.Join(work, "config"), true); code != 0 {
		t.Fatalf("一致配置应返回 0，得到 %d", code)
	}
}

// TestResolveConfigDir 校验 --config 的寻址规则：缺省=exe 同目录的 config，
// 相对路径以 exe 目录为基准，绝对路径原样。
func TestResolveConfigDir(t *testing.T) {
	exe := string(filepath.Separator) + filepath.Join("opt", "addex")
	cases := []struct {
		name, in, want string
	}{
		{"缺省取 exe 同目录 config", "", filepath.Join(exe, "config")},
		{"相对路径以 exe 目录为基准", "config-14346", filepath.Join(exe, "config-14346")},
		{"相对路径带 ./ 前缀", "./config-14346", filepath.Join(exe, "config-14346")},
		{"相对路径带尾斜杠", "config-14346/", filepath.Join(exe, "config-14346")},
		{"绝对路径原样", filepath.Join(exe, "cfg2"), filepath.Join(exe, "cfg2")},
		{"绝对路径做 Clean", filepath.Join(exe, "cfg2") + string(filepath.Separator), filepath.Join(exe, "cfg2")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveConfigDir(c.in, exe); got != c.want {
				t.Fatalf("resolveConfigDir(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// TestReorderFlags 校验「flag 前移、位置参数后移」：switch 的目标值写在 --config 之前时，
// flag 也不能被 stdlib 的“遇位置参数即停”吞掉。
func TestReorderFlags(t *testing.T) {
	needsValue := func(name string) bool { return name == "config" || name == "feature" }
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "位置参数后的 flag 前移",
			in:   []string{"true", "--config", "config-14346"},
			want: []string{"--config", "config-14346", "true"},
		},
		{
			name: "已在前面的 flag 保持相对顺序",
			in:   []string{"--feature", "f.yaml", "true", "--config", "c1"},
			want: []string{"--feature", "f.yaml", "--config", "c1", "true"},
		},
		{
			name: "布尔 flag 不吞值",
			in:   []string{"--no-verify", "true"},
			want: []string{"--no-verify", "true"},
		},
		{
			name: "= 绑定形式不吞值",
			in:   []string{"--config=c1", "true"},
			want: []string{"--config=c1", "true"},
		},
		{
			name: "-- 终止符后原样保留",
			in:   []string{"true", "--", "--config", "c1"},
			want: []string{"true", "--", "--config", "c1"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reorderFlags(c.in, needsValue)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Fatalf("reorderFlags(%v) = %v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

// TestRunSwitchConfigFlag 端到端校验 --config：绝对路径指向的 config-<id> 被改写，
// 且 flag 无论写在位置参数前后都生效。
func TestRunSwitchConfigFlag(t *testing.T) {
	cases := []struct {
		name string
		argv func(dir string) []string
	}{
		{"flag 在位置参数前", func(d string) []string { return []string{"switch", "--config", d, "true"} }},
		{"flag 在位置参数后", func(d string) []string { return []string{"switch", "true", "--config", d} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "config-14346")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, "SimulatedFlag_Ch1")
			if err := os.WriteFile(p, []byte(`<setSimulated type="method">false</setSimulated>`), 0o644); err != nil {
				t.Fatal(err)
			}
			argv := c.argv(dir)
			out := captureStdout(t, func() {
				if code := run(argv); code != 0 {
					t.Errorf("run(%v) 应返回 0，得到 %d", argv, code)
				}
			})
			if !strings.Contains(out, "改写 1") {
				t.Errorf("run(%v) 应改写 1 个文件：\n%s", argv, out)
			}
			if b, _ := os.ReadFile(p); string(b) != `<setSimulated type="method">true</setSimulated>` {
				t.Errorf("config-14346 未切到 true：%q", b)
			}
		})
	}
}

// TestExpandChambers 校验 --chamber 与 -c 两种写法都支持连续多值。
func TestExpandChambers(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "长写法连续多值",
			in:   []string{"--feature", "f.yaml", "--chamber", "Ch1", "Ch2", "--config", "c1"},
			want: []string{"--feature", "f.yaml", "--chamber", "Ch1", "--chamber", "Ch2", "--config", "c1"},
		},
		{
			name: "短写法 -c 同样连续多值",
			in:   []string{"-c", "Ch1", "Ch2", "Ch3"},
			want: []string{"-c", "Ch1", "-c", "Ch2", "-c", "Ch3"},
		},
		{
			name: "两种写法混用",
			in:   []string{"-c", "Ch1", "--chamber", "Ch2", "Ch3"},
			want: []string{"-c", "Ch1", "--chamber", "Ch2", "--chamber", "Ch3"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := expandChambers(c.in)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Fatalf("expandChambers(%v) = %v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

func TestExpandVariadic(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "连续多值展开为重复写法",
			in:   []string{"--feature", "f.yaml", "--chamber", "Ch1", "Ch2", "Ch3"},
			want: []string{"--feature", "f.yaml", "--chamber", "Ch1", "--chamber", "Ch2", "--chamber", "Ch3"},
		},
		{
			name: "单横线同样支持",
			in:   []string{"-chamber", "Ch1", "Ch2"},
			want: []string{"-chamber", "Ch1", "-chamber", "Ch2"},
		},
		{
			name: "已有重复写法保持不变",
			in:   []string{"--chamber", "Ch1", "--chamber", "Ch2"},
			want: []string{"--chamber", "Ch1", "--chamber", "Ch2"},
		},
		{
			name: "遇到下一个 flag 停止吞值",
			in:   []string{"--chamber", "Ch1", "Ch2", "--feature", "f.yaml"},
			want: []string{"--chamber", "Ch1", "--chamber", "Ch2", "--feature", "f.yaml"},
		},
		{
			name: "= 绑定形式只取单值",
			in:   []string{"--chamber=Ch1", "Ch2"},
			want: []string{"--chamber=Ch1", "Ch2"},
		},
		{
			name: "-- 终止符后原样保留",
			in:   []string{"--chamber", "Ch1", "--", "Ch2", "Ch3"},
			want: []string{"--chamber", "Ch1", "--", "Ch2", "Ch3"},
		},
		{
			name: "缺值时原样交给 flag 报错",
			in:   []string{"--chamber"},
			want: []string{"--chamber"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := expandVariadic(c.in, "chamber")
			if len(got) != len(c.want) {
				t.Fatalf("长度不符\n得到 %v\n期望 %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 项不符\n得到 %v\n期望 %v", i, got, c.want)
				}
			}
		})
	}
}
