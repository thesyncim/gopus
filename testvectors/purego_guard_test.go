package testvectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoCBindingsUsageInRepositoryTests(t *testing.T) {
	requireTestTier(t, testTierFast)

	root := filepath.Join("..")
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "tmp_check" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		normalizedPath := filepath.ToSlash(path)
		if strings.HasSuffix(normalizedPath, "testvectors/purego_guard_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(raw)
		if strings.Contains(s, `import "C"`) || strings.Contains(s, "//go:build cgo") {
			hits = append(hits, normalizedPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("c binding usage found in repository: %s", strings.Join(hits, ", "))
	}
}
