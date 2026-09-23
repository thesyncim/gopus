package gopus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKernelTreeContainsNoAssembly(t *testing.T) {
	var assemblyFiles []string
	var assemblyDeclarations []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch filepath.Base(path) {
			case ".git", "tmp_check":
				return filepath.SkipDir
			}
			return nil
		}

		normalized := filepath.ToSlash(path)
		if strings.HasSuffix(path, ".s") {
			assemblyFiles = append(assemblyFiles, normalized)
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "//go:"+"noescape") {
				assemblyDeclarations = append(assemblyDeclarations, normalized)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
	if len(assemblyFiles) > 0 {
		t.Fatalf("Go-native kernels require zero assembly source files; found: %s", strings.Join(assemblyFiles, ", "))
	}
	if len(assemblyDeclarations) > 0 {
		t.Fatalf("Go-native kernels cannot declare assembly-only noescape symbols; found: %s", strings.Join(assemblyDeclarations, ", "))
	}
}
