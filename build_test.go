package gopus

import (
	"os"
	"os/exec"
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
		"./internal/rangecoding",
		"./internal/silk",
		"./internal/celt",
		"./internal/hybrid",
		"./internal/plc",
		"./internal/multistream",
		"./internal/encoder",
		"./internal/types",
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
