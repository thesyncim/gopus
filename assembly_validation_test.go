package gopus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssemblyValidationContract(t *testing.T) {
	var missingPurego []string
	var missingNoescapePurego []string

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
		switch {
		case strings.HasSuffix(path, ".s"):
			first := firstLine(t, path)
			if !strings.HasPrefix(first, "//go:build") || !strings.Contains(first, "!purego") {
				missingPurego = append(missingPurego, normalized)
			}
		case strings.HasSuffix(path, ".go"):
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(raw)
			if strings.Contains(text, "//go:"+"noescape") {
				first := firstLineFromText(text)
				if !strings.HasPrefix(first, "//go:build") || !strings.Contains(first, "!purego") {
					missingNoescapePurego = append(missingNoescapePurego, normalized)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
	if len(missingPurego) > 0 {
		t.Fatalf("assembly files must be excluded by !purego: %s", strings.Join(missingPurego, ", "))
	}
	if len(missingNoescapePurego) > 0 {
		t.Fatalf("assembly declarations must be excluded by !purego: %s", strings.Join(missingNoescapePurego, ", "))
	}
}

func firstLine(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return firstLineFromText(string(raw))
}

func firstLineFromText(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}
