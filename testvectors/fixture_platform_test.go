package testvectors

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const requirePlatformFixturesEnv = "GOPUS_REQUIRE_PLATFORM_FIXTURES"

func platformFixtureReadPath(generic string) string {
	path := platformFixtureWritePath(generic)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if os.Getenv(requirePlatformFixturesEnv) != "" {
		return path
	}
	return generic
}

func platformFixtureWritePath(generic string) string {
	return platformFixturePathFor(generic, runtime.GOOS, runtime.GOARCH)
}

func platformFixturePathFor(generic, goos, goarch string) string {
	ext := filepath.Ext(generic)
	return strings.TrimSuffix(generic, ext) + "_" + goos + "_" + goarch + ext
}

func TestPlatformFixturePathHelpers(t *testing.T) {
	t.Setenv(requirePlatformFixturesEnv, "")

	dir := t.TempDir()
	generic := filepath.Join(dir, "fixture.json")
	platform := platformFixturePathFor(generic, runtime.GOOS, runtime.GOARCH)

	if got := platformFixtureWritePath(generic); got != platform {
		t.Fatalf("write path=%q want %q", got, platform)
	}
	if got := platformFixtureReadPath(generic); got != generic {
		t.Fatalf("read path without platform file=%q want %q", got, generic)
	}
	if err := os.WriteFile(platform, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write platform fixture: %v", err)
	}
	if got := platformFixtureReadPath(generic); got != platform {
		t.Fatalf("read path with platform file=%q want %q", got, platform)
	}
}

func TestPlatformFixtureRequireEnvDisablesGenericFallback(t *testing.T) {
	t.Setenv(requirePlatformFixturesEnv, "1")

	generic := filepath.Join(t.TempDir(), "fixture.json")
	platform := platformFixturePathFor(generic, runtime.GOOS, runtime.GOARCH)
	if got := platformFixtureReadPath(generic); got != platform {
		t.Fatalf("read path with %s=%q want %q", requirePlatformFixturesEnv, got, platform)
	}
}

func TestRequiredPlatformFixturesPresent(t *testing.T) {
	t.Parallel()
	if os.Getenv(requirePlatformFixturesEnv) == "" {
		t.Skipf("%s not set", requirePlatformFixturesEnv)
	}

	paths := []string{
		libopusDecoderMatrixFixturePath,
		libopusDecoderLossFixturePath,
		libopusDecoderRateMatrixFixturePath,
		encoderCompliancePacketsFixturePath,
		encoderComplianceVariantsFixturePath,
	}
	for _, generic := range paths {
		path := platformFixtureWritePath(generic)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("required platform fixture %s is missing: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("required platform fixture %s is empty", path)
		}
	}
}
