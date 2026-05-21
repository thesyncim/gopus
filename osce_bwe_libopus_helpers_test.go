//go:build gopus_extra_controls

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// OSCE BWE (blind bandwidth extension) is an extra-control libopus feature. The
// helpers in this file build and invoke `tools/csrc/libopus_osce_bwe_model_blob.c`
// to extract the upstream `bbwenetlayers_arrays` weight table as a
// USE_WEIGHTS_FILE-compatible blob. The blob can then be fed to parity tests
// that exercise extra-control OSCE BWE behaviour.
//
// IMPORTANT: the libopus 1.6.1 default DRED build (configured with
// `--enable-dred` only) does NOT compile `dnn/bbwenet_data.c` into libopus.a;
// it lives behind `--enable-osce`. The helper sidesteps that by including the
// generated source directly so the BBWENET weight arrays end up in the helper
// binary itself. Only `linear_init` (from nnet.c, present in the DRED build)
// has to be resolved at link time.
//
// If a future build matrix drops the DRED scalar build, this helper must
// either depend on its own OSCE-enabled libopus build or fall back to bundling
// `nnet.c` directly into the helper TU.

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
