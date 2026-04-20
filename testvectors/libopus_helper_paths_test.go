package testvectors

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperBinaryPathUsesWindowsExeSuffix(t *testing.T) {
	got := helperBinaryPath("gopus_helper", "windows", "amd64")
	if !strings.HasSuffix(got, ".exe") {
		t.Fatalf("expected windows helper path to end with .exe, got %q", got)
	}
	if filepath.Base(got) != "gopus_helper_windows_amd64.exe" {
		t.Fatalf("unexpected windows helper basename: %q", filepath.Base(got))
	}
}

func TestHelperBinaryPathLeavesUnixPathsUnsuffixed(t *testing.T) {
	got := helperBinaryPath("gopus_helper", "linux", "amd64")
	if strings.HasSuffix(got, ".exe") {
		t.Fatalf("expected unix helper path without .exe, got %q", got)
	}
	if filepath.Base(got) != "gopus_helper_linux_amd64" {
		t.Fatalf("unexpected unix helper basename: %q", filepath.Base(got))
	}
}
