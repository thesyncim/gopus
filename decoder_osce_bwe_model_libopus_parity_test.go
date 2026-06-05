//go:build gopus_osce

package gopus

import (
	"testing"
)

// TestDecoderOSCEBWEModelLoadsFromLibopusBlob is the Phase 1 smoke test for
// the decoder-side OSCE BWE model loader. It mirrors the LPCNet
// `TestDecoder*LoadsFromLibopusBlob`-style binding contract:
//
//   - Build the libopus OSCE BWE blob (libopus_osce_bwe_model_blob.c).
//   - Merge it with the core PLC/PitchDNN/FARGAN blob so SetDNNBlob's
//     `ValidateDecoderControl(false)` check passes; the BWE records are then
//     bound additively by `setDNNBlob -> bindOSCEBWEModel`.
//   - Verify the decoder reports both the blob-level OSCEBWE flag and the
//     runtime-bound model via `osceBWEModelLoadedRuntime()`.
//
// The test skips cleanly when the libopus helper binaries cannot be built
// (e.g. missing libopus tarball or compiler).
func TestDecoderOSCEBWEModelLoadsFromLibopusBlob(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE) error: %v", err)
	}
	if !dec.osceBWEModelLoaded {
		t.Fatalf("decoder did not retain osceBWEModelLoaded after SetDNNBlob")
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	// Re-applying with an OSCE-BWE-free blob must clear the runtime binding
	// while keeping the core neural models loaded.
	if err := dec.SetDNNBlob(coreBlob); err != nil {
		t.Fatalf("SetDNNBlob(core-only) error: %v", err)
	}
	if dec.osceBWEModelLoaded {
		t.Fatalf("decoder unexpectedly retained osceBWEModelLoaded after core-only blob")
	}
	if dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder unexpectedly retained OSCE BWE runtime binding after core-only blob")
	}
}
