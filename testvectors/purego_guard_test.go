package testvectors

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoCBindingsUsageInRepositoryTests(t *testing.T) {
	requireTestTier(t, testTierFast)

	root := filepath.Join("..")
	var hits []string

	paths, err := trackedGoFiles(root)
	if err != nil {
		t.Fatalf("list repository Go files: %v", err)
	}
	for _, path := range paths {
		normalizedPath := filepath.ToSlash(path)
		if skipPureGoGuardPath(normalizedPath) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", normalizedPath, err)
		}
		s := string(raw)
		if strings.Contains(s, `import "C"`) || strings.Contains(s, "//go:build cgo") {
			hits = append(hits, normalizedPath)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("c binding usage found in repository: %s", strings.Join(hits, ", "))
	}
}

func trackedGoFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.go")
	out, err := cmd.Output()
	if err == nil {
		parts := bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0})
		paths := make([]string, 0, len(parts))
		for _, part := range parts {
			if len(part) == 0 {
				continue
			}
			paths = append(paths, filepath.Join(root, string(part)))
		}
		return paths, nil
	}

	var paths []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" ||
				base == ".gocache" ||
				base == ".claude" ||
				base == ".docker-cache" ||
				base == ".idea" ||
				base == ".tmp" ||
				base == "reports" ||
				base == "tmp_check" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

func skipPureGoGuardPath(path string) bool {
	return strings.HasSuffix(path, "testvectors/purego_guard_test.go") ||
		strings.Contains(path, "/tmp_check/")
}
