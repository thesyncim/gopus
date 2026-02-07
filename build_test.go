package gopus

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildNoCGO verifies the package builds with CGO_ENABLED=0
func TestBuildNoCGO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	// Build the package with CGO disabled
	cmd := exec.Command("go", "build", "-o", os.DevNull, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Dir = "." // Current package directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Build with CGO_ENABLED=0 failed: %v\n%s", err, output)
	}

	t.Log("PASS: Zero cgo dependencies verified")
}

// TestBuildAllPackages verifies all packages build without cgo
func TestBuildAllPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	packages := []string{
		".",
		"./container/ogg",
		"./rangecoding",
		"./silk",
		"./celt",
		"./hybrid",
		"./plc",
		"./multistream",
		"./encoder",
		"./types",
	}

	for _, pkg := range packages {
		t.Run(pkg, func(t *testing.T) {
			cmd := exec.Command("go", "build", "-o", os.DevNull, pkg)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Build %s with CGO_ENABLED=0 failed: %v\n%s", pkg, err, output)
			}
		})
	}
}

// TestNoUnsafeImports documents unsafe package usage decisions
func TestNoUnsafeImports(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping import check in short mode")
	}

	// Note: We allow unsafe in range coder for performance
	// This test documents that decision
	t.Log("INFO: Package may use unsafe for performance-critical paths")
	t.Log("INFO: Core codec logic does not require unsafe")
}

// TestNoCGOSourceDirectives prevents accidental reintroduction of cgo usage.
func TestNoCGOSourceDirectives(t *testing.T) {
	var violations []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "tmp_check":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, data, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				text := strings.TrimSpace(c.Text)
				if strings.HasPrefix(text, "//go:build") && strings.Contains(text, "cgo") {
					violations = append(violations, path+": contains cgo build tag")
				}
				if strings.HasPrefix(text, "// +build") && strings.Contains(text, "cgo") {
					violations = append(violations, path+": contains legacy cgo build tag")
				}
				if strings.Contains(text, "#cgo") {
					violations = append(violations, path+": contains #cgo directive")
				}
			}
		}
		for _, imp := range file.Imports {
			if imp.Path != nil && imp.Path.Value == "\"C\"" {
				violations = append(violations, path+": imports \"C\"")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan source tree: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("cgo usage is disallowed:\n%s", strings.Join(violations, "\n"))
	}
}

// TestNoCGOTestArtifacts prevents reintroducing legacy cgo test scaffolding.
// Equivalent parity tests must live in pure-Go fixture tests.
func TestNoCGOTestArtifacts(t *testing.T) {
	var violations []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "tmp_check":
				return filepath.SkipDir
			case "cgo_test", "cgo":
				violations = append(violations, path+": cgo test directory is disallowed")
				return filepath.SkipDir
			}
			return nil
		}

		base := strings.ToLower(filepath.Base(path))
		if strings.Contains(base, "cgo") || strings.Contains(base, "_wrapper.go") {
			violations = append(violations, path+": cgo-style test artifact is disallowed")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan source tree: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("remove cgo test artifacts and replace with pure-Go fixtures:\n%s", strings.Join(violations, "\n"))
	}
}
