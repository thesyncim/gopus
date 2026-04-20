package libopustooling

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeFileInfo struct {
	mode os.FileMode
	dir  bool
}

func (f fakeFileInfo) Name() string       { return "stub" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

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

func TestFindLibopusToolForOSRejectsUnixFileWithoutExecBit(t *testing.T) {
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
}

func TestLibopusToolIsRunnableUsesPlatformSemantics(t *testing.T) {
	tests := []struct {
		name string
		info fakeFileInfo
		goos string
		want bool
	}{
		{
			name: "unix requires exec bit",
			info: fakeFileInfo{mode: 0o644},
			goos: "linux",
			want: false,
		},
		{
			name: "unix accepts exec bit",
			info: fakeFileInfo{mode: 0o755},
			goos: "linux",
			want: true,
		},
		{
			name: "windows ignores exec bit",
			info: fakeFileInfo{mode: 0o644},
			goos: "windows",
			want: true,
		},
		{
			name: "directories are never runnable",
			info: fakeFileInfo{mode: 0o755, dir: true},
			goos: "windows",
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := libopusToolIsRunnable(tc.info, tc.goos); got != tc.want {
				t.Fatalf("runnable mismatch: got %v want %v", got, tc.want)
			}
		})
	}
}
