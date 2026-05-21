//go:build gopus_extra_controls

package gopus

import (
	"testing"
)

// TestOSCELACEModelLoadsFromLibopusBlob is the Phase 1 smoke test for the
// decoder-side OSCE LACE/NoLACE model loader. It mirrors the OSCE BWE
// `TestDecoderOSCEBWEModelLoadsFromLibopusBlob` binding contract:
//
//   - Build the libopus OSCE LACE+NoLACE blob (libopus_osce_lace_model_blob.c).
//   - Merge it with the core PLC/PitchDNN/FARGAN blob so SetDNNBlob's
//     `ValidateDecoderControl(false)` check passes; the LACE/NoLACE records are
//     then bound additively by `setDNNBlob -> bindOSCELACEModel`.
//   - Verify the decoder reports both the blob-level OSCE flag and the
//     runtime-bound model via `osceLACEModelLoadedRuntime()`.
//
// The test skips cleanly when the libopus helper binaries cannot be built
// (e.g. missing libopus tarball or compiler).
func TestOSCELACEModelLoadsFromLibopusBlob(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(laceBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE) error: %v", err)
	}
	if !dec.osceModelsLoaded {
		t.Fatalf("decoder did not retain osceModelsLoaded after SetDNNBlob")
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	// Re-applying with an OSCE-LACE-free blob must clear the runtime binding
	// while keeping the core neural models loaded.
	if err := dec.SetDNNBlob(coreBlob); err != nil {
		t.Fatalf("SetDNNBlob(core-only) error: %v", err)
	}
	if dec.osceModelsLoaded {
		t.Fatalf("decoder unexpectedly retained osceModelsLoaded after core-only blob")
	}
	if dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder unexpectedly retained OSCE LACE runtime binding after core-only blob")
	}
}
