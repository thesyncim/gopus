//go:build gopus_osce

package gopus

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	"os"
	"testing"
)

var libopusOSCEBWEModelBlobHelper libopustest.HelperCache

// getLibopusOSCEBWEModelBlobHelperPath compiles (once) the OSCE BWE blob
// extractor helper against the project's DRED scalar libopus build and
// returns the resulting binary path.
func getLibopusOSCEBWEModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusOSCEBWEModelBlobHelper, "libopus_osce_bwe_model_blob.c", "gopus_libopus_osce_bwe_model_blob", true)
}

// probeLibopusOSCEBWEModelBlob builds (lazily) and runs the OSCE BWE blob
// extractor, returning the raw `bbwenetlayers_arrays` weight blob. Returns
// the first error encountered so callers can decide whether to skip.
func probeLibopusOSCEBWEModelBlob() ([]byte, error) {
	binPath, err := getLibopusOSCEBWEModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

// requireLibopusOSCEBWEModelBlob returns the OSCE BWE blob or skips the test
// if libopus 1.6.1 cannot be linked with the OSCE BWE source on this host
// (e.g. missing tarball, OSCE source absent, or compiler unavailable).
func requireLibopusOSCEBWEModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusOSCEBWEModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE model blob", err)
	}
	return blob
}

// TestOSCEBWEModelBlobExtractionSmoke is a minimal smoke test that exercises
// the OSCE BWE blob extraction pipeline end-to-end. It skips cleanly when the
// libopus tarball or build environment is unavailable, so it is safe to run
// in environments that do not (yet) prepare a libopus DRED build.
func TestOSCEBWEModelBlobExtractionSmoke(t *testing.T) {
	libopustest.RequireOracle(t)
	blob := requireLibopusOSCEBWEModelBlob(t)
	if len(blob) == 0 {
		t.Fatalf("OSCE BWE blob is empty")
	}
	// Sanity: blob must start with the "DNNw" weight-record magic emitted by
	// the helper's write_weights routine. If the helper successfully produced
	// any records at all the leading 4 bytes will be 'D','N','N','w'.
	if len(blob) < 4 || string(blob[:4]) != "DNNw" {
		t.Fatalf("OSCE BWE blob missing DNNw magic; first bytes = %q", blob[:min(len(blob), 8)])
	}

	// Verify the blob actually satisfies the OSCE BWE manifest from
	// internal/dnnblob/model_manifests_generated.go. SupportsOSCEBWE returns
	// true only when every required `bbwenet_*` record is present.
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(OSCE BWE blob): %v", err)
	}
	if !parsed.SupportsOSCEBWE() {
		t.Fatalf("parsed OSCE BWE blob does not report OSCEBWE support (manifest mismatch)")
	}
}

var libopusOSCELACEModelBlobHelper libopustest.HelperCache

// getLibopusOSCELACEModelBlobHelperPath compiles (once) the OSCE LACE/NoLACE
// blob extractor helper against the project's DRED scalar libopus build and
// returns the resulting binary path.
func getLibopusOSCELACEModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusOSCELACEModelBlobHelper, "libopus_osce_lace_model_blob.c", "gopus_libopus_osce_lace_model_blob", true)
}

// probeLibopusOSCELACEModelBlob builds (lazily) and runs the OSCE LACE/NoLACE
// blob extractor, returning the concatenated `lacelayers_arrays` +
// `nolacelayers_arrays` weight blob. Returns the first error encountered so
// callers can decide whether to skip.
func probeLibopusOSCELACEModelBlob() ([]byte, error) {
	binPath, err := getLibopusOSCELACEModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

// requireLibopusOSCELACEModelBlob returns the OSCE LACE/NoLACE blob or skips
// the test if libopus 1.6.1 cannot be linked with the OSCE LACE/NoLACE source
// on this host (e.g. missing tarball, OSCE source absent, or compiler
// unavailable).
func requireLibopusOSCELACEModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusOSCELACEModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE LACE model blob", err)
	}
	return blob
}

// TestOSCELACEModelBlobExtractionSmoke is a minimal smoke test that exercises
// the OSCE LACE/NoLACE blob extraction pipeline end-to-end. It skips cleanly
// when the libopus tarball or build environment is unavailable, so it is
// safe to run in environments that do not (yet) prepare a libopus DRED
// build.
func TestOSCELACEModelBlobExtractionSmoke(t *testing.T) {
	libopustest.RequireOracle(t)
	blob := requireLibopusOSCELACEModelBlob(t)
	if len(blob) == 0 {
		t.Fatalf("OSCE LACE blob is empty")
	}
	// Sanity: blob must start with the "DNNw" weight-record magic emitted by
	// the helper's write_weights routine.
	if len(blob) < 4 || string(blob[:4]) != "DNNw" {
		t.Fatalf("OSCE LACE blob missing DNNw magic; first bytes = %q", blob[:min(len(blob), 8)])
	}

	// Verify the blob actually satisfies both LACE and NoLACE manifests from
	// internal/dnnblob/model_manifests_generated.go.
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(OSCE LACE blob): %v", err)
	}
	if !parsed.SupportsOSCELACE() {
		t.Fatalf("parsed OSCE LACE blob does not report OSCELACE support (manifest mismatch)")
	}
	if !parsed.SupportsOSCENoLACE() {
		t.Fatalf("parsed OSCE LACE blob does not report OSCENoLACE support (manifest mismatch)")
	}
}

func buildLibopusOSCEHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildOSCEHelper(repoRoot, sourceFile, outputBase, includeInternal)
}

func cachedLibopusOSCEHelperPath(cache *libopustest.HelperCache, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return cache.Path(func() (string, error) {
		return buildLibopusOSCEHelper(sourceFile, outputBase, includeInternal)
	})
}
