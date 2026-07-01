package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"addex/internal/feature"
)

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

func TestGoldenParity(t *testing.T) {
	features := []string{"ig-auto-close", "add-pedcurpos-dataex"}
	for _, f := range features {
		t.Run(f, func(t *testing.T) {
			work := t.TempDir()
			copyTree(t, "../../config", filepath.Join(work, "config"))
			featurePath := filepath.Join("../../features", f+".yaml")

			// plan：不写盘，仅比对语义 diff。
			if got, want := run(t, work, featurePath, false), stripHeader(readGolden(t, "../../testdata/golden/"+f+".plan.txt")); got != want {
				t.Errorf("plan 输出不一致\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}

			// apply：比对语义 diff + 逐字节比对写盘结果。
			if got, want := run(t, work, featurePath, true), stripHeader(readGolden(t, "../../testdata/golden/"+f+".apply.txt")); got != want {
				t.Errorf("apply 输出不一致\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
			for c := 1; c <= 4; c++ {
				name := "Control_Ch" + string(rune('0'+c))
				got := readGolden(t, filepath.Join(work, "config", "Control", name))
				want := readGolden(t, filepath.Join("../../testdata/golden", f, name))
				if got != want {
					t.Errorf("写盘 %s 不一致", name)
				}
			}

			// 幂等：第二次 apply 零改动。
			if got, want := run(t, work, featurePath, true), stripHeader(readGolden(t, "../../testdata/golden/"+f+".apply2.txt")); got != want {
				t.Errorf("幂等 apply2 输出不一致\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}
