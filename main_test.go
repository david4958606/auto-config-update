package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
