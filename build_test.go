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

// TestDefaultBuildIsZeroCostForGatedFeatures enforces the optional-feature
// contract: the DEFAULT build (no build tags) links ZERO of the code that
// libopus also gates behind a compile flag in its default ./configure build.
//
// Tag <-> libopus compile-flag mapping enforced here:
//
//	gopus_dred           <-> --enable-dred / ENABLE_DRED        (default: no)
//	gopus_extra_controls <-> --enable-osce / ENABLE_OSCE and    (default: no)
//	                          the deep-PLC family / ENABLE_DEEP_PLC
//	gopus_qext           <-> --enable-qext / ENABLE_QEXT        (default: no)
//	gopus_custom         <-> --enable-custom-modes / CUSTOM_MODES (default: no)
//
// In a default libopus build LPCNET_SOURCES (the DNN / PitchDNN / FARGAN /
// RDOVAE neural code) is empty, so none of it is compiled. The gated Go
// packages below mirror that code and must likewise be absent from the
// default-build import graph.
func TestDefaultBuildIsZeroCostForGatedFeatures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dep-graph check in short mode")
	}

	// Public packages whose default build must stay neural/feature free.
	publicPkgs := []string{".", "./multistream", "./encoder"}

	// Packages that mirror libopus code gated behind a compile flag. None may
	// appear in the default (untagged) import graph of any public package.
	const modulePrefix = "github.com/thesyncim/gopus/"
	gatedPkgs := []string{
		modulePrefix + "internal/dred",          // ENABLE_DRED (RDOVAE driver)
		modulePrefix + "internal/dred/rdovae",    // ENABLE_DRED neural codec
		modulePrefix + "internal/lpcnetplc",      // ENABLE_DEEP_PLC (PitchDNN / FARGAN)
		modulePrefix + "internal/osce",           // ENABLE_OSCE
		modulePrefix + "internal/osce/lace",      // ENABLE_OSCE (LACE / NoLACE)
		modulePrefix + "internal/osce/bwe",       // ENABLE_OSCE_BWE
		modulePrefix + "celt/custom",             // CUSTOM_MODES
		modulePrefix + "internal/fixedpoint",     // gopus_fixedpoint (integer CELT codec)
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
		for _, line := range strings.Split(string(out), "\n") {
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

// TestDefaultBinaryHasNoFixedPointSymbols compiles a tiny program that imports
// the public package under the DEFAULT (untagged) build and inspects the linked
// binary's symbol table. It asserts that no gopus_fixedpoint-only symbol made it
// in: neither any internal/fixedpoint symbol, nor the package-local shims that
// now live in //go:build gopus_fixedpoint files. This is a stronger guarantee
// than the import-graph check because it inspects the actual link output.
func TestDefaultBinaryHasNoFixedPointSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping symbol check in short mode")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}

	// Place the probe in a temp subdirectory of THIS module so it shares the
	// module's go.mod and resolves all in-tree imports without a separate
	// module / replace directive.
	probeDir, err := os.MkdirTemp(".", "fixedpointprobe")
	if err != nil {
		t.Fatalf("create probe dir: %v", err)
	}
	defer os.RemoveAll(probeDir)

	src := "package main\n\n" +
		"import (\n" +
		"\t_ \"github.com/thesyncim/gopus\"\n" +
		"\t_ \"github.com/thesyncim/gopus/celt\"\n" +
		"\t_ \"github.com/thesyncim/gopus/rangecoding\"\n" +
		")\n\n" +
		"func main() {}\n"
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write probe main: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "probe.bin")
	// Default build: no build tags. GOFLAGS cleared so the host environment
	// cannot inject -tags=gopus_fixedpoint.
	build := exec.Command(goBin, "build", "-tags", "", "-o", binPath, "./"+filepath.Base(probeDir))
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build default probe binary: %v\n%s", err, out)
	}

	nm := exec.Command(goBin, "tool", "nm", binPath)
	nm.Env = append(os.Environ(), "GOWORK=off")
	out, err := nm.CombinedOutput()
	if err != nil {
		t.Skipf("go tool nm unavailable on this platform: %v\n%s", err, out)
	}
	syms := string(out)

	// Substrings that must never appear in a default-build symbol table.
	forbidden := []string{
		"github.com/thesyncim/gopus/internal/fixedpoint",
		// Package-local shims moved into //go:build gopus_fixedpoint files.
		"github.com/thesyncim/gopus/celt.MaxPulsesBitsExport",
		"github.com/thesyncim/gopus/celt.DecodeCELTAllocation",
		"github.com/thesyncim/gopus/celt.TFDecode",
		"github.com/thesyncim/gopus/rangecoding.(*Decoder).SkipToTell",
		"github.com/thesyncim/gopus/rangecoding.(*Encoder).SkipToTell",
		"github.com/thesyncim/gopus/rangecoding.(*Encoder).Snapshot",
		"github.com/thesyncim/gopus/rangecoding.(*Encoder).Restore",
	}
	for _, sym := range forbidden {
		if strings.Contains(syms, sym) {
			t.Errorf("zero-cost contract violation: default-build binary contains gopus_fixedpoint-only symbol %q", sym)
		}
	}
}
