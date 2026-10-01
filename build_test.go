package gopus_test

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

// TestBuildAllPackages builds the selected core packages with cgo disabled.
func TestBuildAllPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	packages := []string{
		".",
		"./container/ogg",
		"./internal/rangecoding",
		"./internal/silk",
		"./internal/celt",
		"./internal/hybrid",
		"./internal/plc",
		"./multistream",
		"./internal/encoder",
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

// TestDefaultBuildIsZeroCostForGatedFeatures checks that default public and
// core-package import graphs exclude optional-feature packages.
func TestDefaultBuildIsZeroCostForGatedFeatures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dep-graph check in short mode")
	}

	// Check public entrypoints and core packages.
	publicPkgs := []string{".", "./internal/encoder", "./multistream", "./internal/hybrid", "./internal/silk", "./internal/celt"}

	// Optional-feature packages must be absent from the untagged dependency graph.
	const modulePrefix = "github.com/thesyncim/gopus/"
	gatedPkgs := []string{
		modulePrefix + "internal/dred",        // ENABLE_DRED (RDOVAE driver)
		modulePrefix + "internal/dred/rdovae", // ENABLE_DRED neural codec
		modulePrefix + "internal/lpcnetplc",   // ENABLE_DEEP_PLC (PitchDNN / FARGAN)
		modulePrefix + "internal/osce",        // ENABLE_OSCE
		modulePrefix + "internal/osce/lace",   // ENABLE_OSCE (LACE / NoLACE)
		modulePrefix + "internal/osce/bwe",    // ENABLE_OSCE_BWE
		modulePrefix + "internal/celt/custom", // CUSTOM_MODES
		modulePrefix + "internal/fixedpoint",  // gopus_fixed_point (integer CELT codec)
	}

	for _, pkg := range publicPkgs {
		// Explicitly clear build tags: this is the DEFAULT ./configure-equivalent build.
		cmd := exec.Command("go", "list", "-deps", "-tags", "", pkg)
		cmd.Env = append(os.Environ(), "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s failed: %v\n%s", pkg, err, out)
		}
		deps := make(map[string]bool)
		for line := range strings.SplitSeq(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				deps[line] = true
			}
		}
		for _, gated := range gatedPkgs {
			if deps[gated] {
				t.Errorf("zero-cost contract violation: default build of %s links gated package %s "+
					"(libopus gates the equivalent C code behind a compile flag); it must be reachable "+
					"only under the matching build tag", pkg, gated)
			}
		}
	}
}
