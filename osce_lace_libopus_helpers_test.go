//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// OSCE LACE/NoLACE postfilter is a quarantined libopus feature. The helpers
// in this file build and invoke `tools/csrc/libopus_osce_lace_model_blob.c`
// to extract the upstream `lacelayers_arrays` and `nolacelayers_arrays`
// weight tables as a single USE_WEIGHTS_FILE-compatible blob. The blob can
// then be fed to parity tests that exercise quarantined OSCE LACE/NoLACE
// behaviour.
//
// IMPORTANT: the libopus 1.6.1 default DRED build (configured with
// `--enable-dred` only) does NOT compile `dnn/lace_data.c` /
// `dnn/nolace_data.c` into libopus.a; they live behind `--enable-osce`. The
// helper sidesteps that by including the generated sources directly so the
// LACE/NoLACE weight arrays end up in the helper binary itself. Only
// `linear_init` (from nnet.c, present in the DRED build) has to be resolved
// at link time.
//
// If a future build matrix drops the DRED scalar build, this helper must
// either depend on its own OSCE-enabled libopus build or fall back to
// bundling `nnet.c` directly into the helper TU.

var (
	libopusOSCELACEModelBlobHelperOnce sync.Once
	libopusOSCELACEModelBlobHelperPath string
	libopusOSCELACEModelBlobHelperErr  error
)

// getLibopusOSCELACEModelBlobHelperPath compiles (once) the OSCE LACE/NoLACE
// blob extractor helper against the project's DRED scalar libopus build and
// returns the resulting binary path.
func getLibopusOSCELACEModelBlobHelperPath() (string, error) {
	libopusOSCELACEModelBlobHelperOnce.Do(func() {
		libopusOSCELACEModelBlobHelperPath, libopusOSCELACEModelBlobHelperErr = buildLibopusDREDHelper(
			"libopus_osce_lace_model_blob.c",
			"gopus_libopus_osce_lace_model_blob",
			true,
		)
	})
	if libopusOSCELACEModelBlobHelperErr != nil {
		return "", libopusOSCELACEModelBlobHelperErr
	}
	return libopusOSCELACEModelBlobHelperPath, nil
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
		t.Skipf("libopus OSCE LACE model blob helper unavailable: %v", err)
	}
	return blob
}

// TestOSCELACEModelBlobExtractionSmoke is a minimal smoke test that exercises
// the OSCE LACE/NoLACE blob extraction pipeline end-to-end. It skips cleanly
// when the libopus tarball or build environment is unavailable, so it is
// safe to run in environments that do not (yet) prepare a libopus DRED
// build.
func TestOSCELACEModelBlobExtractionSmoke(t *testing.T) {
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
