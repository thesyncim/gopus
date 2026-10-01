package libopustest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestPinnedSourceReadWorksWithoutVariantBuildTrees(t *testing.T) {
	root := t.TempDir()
	sourcePath := pinnedSourcePath(root, "dnn", "nnet.h")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	const want = "pinned source only"
	if err := os.WriteFile(sourcePath, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []libopustooling.LibopusReferenceVariant{
		libopustooling.LibopusReferenceScalar,
		libopustooling.LibopusReferenceSIMD,
	} {
		suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "tmp_check", "opus-"+libopustooling.DefaultVersion+suffix)); !os.IsNotExist(err) {
			t.Fatalf("variant tree %s unexpectedly exists: %v", variant, err)
		}
	}

	got := string(readPinnedSourceFileOrSkip(t, root, "nnet.h", "dnn", "nnet.h"))
	if got != want {
		t.Fatalf("source=%q want %q", got, want)
	}
}
