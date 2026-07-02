package main

import "testing"

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
