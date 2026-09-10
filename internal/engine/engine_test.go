package engine

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

// updateGolden 为真时重新生成 testdata/golden(金标准由 Go 引擎输出生成，见 README)。
var updateGolden = flag.Bool("update-golden", false, "重新生成 testdata/golden")

// 对拍：以 Python 引擎产物为金标准(testdata/golden)，逐字节比对 Go 的 plan/apply 输出与写盘结果。
// 金标准文件含 cli 头部两行(功能.../空行)；引擎本身只产出其后的日志，故比对时剥掉这两行。

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("拷贝 %s: %v", src, err)
	}
}

// stripHeader 去掉金标准的头部两行(功能... + 空行)，返回其余日志文本。
func stripHeader(golden string) string {
	parts := strings.SplitN(golden, "\n", 3)
	if len(parts) < 3 {
		return golden
	}
	return parts[2]
}

func run(t *testing.T, work, featurePath string, write bool) string {
	t.Helper()
	feat, err := feature.Load(featurePath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(filepath.Join(work, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	log := func(s string) { lines = append(lines, s) }
	if err := eng.ApplyFeature(feat, nil, write, log); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n") + "\n"
}

func readGolden(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkGolden 比对(或 -update-golden 时重写)一份日志型金标准。
func checkGolden(t *testing.T, name, mode, got string) {
	t.Helper()
	path := filepath.Join("../../testdata/golden", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(goldenHeader(name, mode)+got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if want := stripHeader(readGolden(t, path)); got != want {
		t.Errorf("%s 输出不一致\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// goldenHeader 复刻 CLI 头部两行(run 只产出其后的日志，比对时被剥掉)。
func goldenHeader(name, mode string) string {
	base := strings.TrimSuffix(name, ".txt")
	base = strings.TrimSuffix(base, ".plan")
	base = strings.TrimSuffix(base, ".apply2")
	base = strings.TrimSuffix(base, ".apply")
	feat := base
	v := "1"
	if f, err := feature.Load(filepath.Join("../../features", base+".yaml")); err == nil {
		feat, v = f.ID, f.Ver()
	}
	return fmt.Sprintf("功能 %s v%s | 模式=%s\n\n", feat, v, mode)
}

// checkFileGolden 比对(或 -update-golden 时重写)一份写盘产物金标准。
func checkFileGolden(t *testing.T, rel, gotPath string) {
	t.Helper()
	got := readGolden(t, gotPath)
	path := filepath.Join("../../testdata/golden", rel)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if want := readGolden(t, path); got != want {
		t.Errorf("写盘 %s 不一致", rel)
	}
}

func TestGoldenParity(t *testing.T) {
	// control/bridge：各 feature 需逐字节比对的写盘片段。temp-diff 只动 Degas 腔室(ChC/ChD)。
	cases := []struct {
		name    string
		control []string
		bridge  []string
	}{
		{"ig-auto-close", []string{"Control_Ch1", "Control_Ch2", "Control_Ch3", "Control_Ch4"}, []string{"Driver_Ch1", "IO_Ch1"}},
		{"add-pedcurpos-dataex", []string{"Control_Ch1", "Control_Ch2", "Control_Ch3", "Control_Ch4"}, nil},
		{"temp-diff", []string{"Control_ChC", "Control_ChD"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work := t.TempDir()
			copyTree(t, "../../config", filepath.Join(work, "config"))
			featurePath := filepath.Join("../../features", c.name+".yaml")

			// plan：不写盘，仅比对语义 diff。
			checkGolden(t, c.name+".plan.txt", "plan", run(t, work, featurePath, false))

			// apply：比对语义 diff + 逐字节比对写盘结果。
			checkGolden(t, c.name+".apply.txt", "apply", run(t, work, featurePath, true))
			for _, name := range c.control {
				checkFileGolden(t, filepath.Join(c.name, name), filepath.Join(work, "config", "Control", name))
			}

			// 可选：IOBridge/Driver、IO 片段(仅涉及该域的 feature 才有 golden)。
			for _, frag := range c.bridge {
				checkFileGolden(t, filepath.Join(c.name, frag), filepath.Join(work, "config", "IOBridge", frag))
			}

			// 幂等：第二次 apply 零改动。
			checkGolden(t, c.name+".apply2.txt", "apply", run(t, work, featurePath, true))
		})
	}
}
