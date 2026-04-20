package libopustooling

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindLibopusToolForOSPrefersWindowsExe(t *testing.T) {
	root := t.TempDir()
	toolPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_compare.exe")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	got, ok := findLibopusToolForOS(DefaultVersion, []string{root}, "opus_compare", "windows")
	if !ok {
		t.Fatal("expected windows tool lookup to find .exe binary")
	}
	if got != toolPath {
		t.Fatalf("tool path mismatch: got %q want %q", got, toolPath)
	}
}

func TestFindLibopusToolForOSRequiresUnixExecBit(t *testing.T) {
	root := t.TempDir()
	toolPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_compare")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	if _, ok := findLibopusToolForOS(DefaultVersion, []string{root}, "opus_compare", "linux"); ok {
		t.Fatal("expected unix tool lookup to reject non-executable file")
	}

	if err := os.Chmod(toolPath, 0o755); err != nil {
		t.Fatalf("chmod tool: %v", err)
	}

	got, ok := findLibopusToolForOS(DefaultVersion, []string{root}, "opus_compare", "linux")
	if !ok {
		t.Fatal("expected unix tool lookup to accept executable file")
	}
	if got != toolPath {
		t.Fatalf("tool path mismatch: got %q want %q", got, toolPath)
	}
}
