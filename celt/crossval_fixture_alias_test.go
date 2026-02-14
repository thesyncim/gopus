package celt

import (
	"runtime"
	"testing"
)

func TestOpusdecCrossvalFixtureIncludesWindowsAMD64MonoAlias(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64-specific fixture coverage")
	}

	const windowsMonoSingleHash = "500a2af1eac1eaa7b40fb0d9e1041c04ede5155e5aeb4b07353185a41a20fcc3"

	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		t.Fatalf("loadOpusdecCrossvalFixtureMap: %v", err)
	}
	if _, ok := entries[windowsMonoSingleHash]; !ok {
		t.Fatalf("missing windows amd64 opusdec fixture alias for hash %s", windowsMonoSingleHash)
	}
}
