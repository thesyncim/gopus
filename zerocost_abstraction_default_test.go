//go:build !gopus_dred && !gopus_osce && !gopus_qext && !gopus_fixed_point

package gopus

import "testing"

// TestDefaultBuildGatedDispatchIsCompileTimeNoOp documents and locks in the
// abstraction pattern that makes optional features truly zero-cost.
//
// The hot float decode/encode path calls gated capability methods directly on
// the concrete *Decoder / *Encoder type (e.g. d.is96kHz(), d.osceLACEActive(),
// d.beginFixedPacket()). There is NO interface dispatch and NO runtime
// "am I fixed-point?" flag: the selection is purely compile-time build-tag file
// selection. In the default build the gated files are excluded and a sibling
// _default.go file supplies a no-op / constant-returning stub for each method.
//
// Because the receiver type is concrete and the stub bodies are trivial, the Go
// compiler inlines each call to its constant result and then dead-code
// eliminates the guarded branch entirely. This test asserts the stubs return
// the inert values the dead-code-elimination relies on; if any stub were to
// start doing real work (or return a non-constant), the gated branch would no
// longer fold away and this guard would surface the regression.
func TestDefaultBuildGatedDispatchIsCompileTimeNoOp(t *testing.T) {
	d := &Decoder{}
	if d.is96kHz() {
		t.Error("Decoder.is96kHz() must be a constant false in the default build")
	}
	if d.osceBWEActive() {
		t.Error("Decoder.osceBWEActive() must be a constant false in the default build")
	}
	if d.osceLACEActive() {
		t.Error("Decoder.osceLACEActive() must be a constant false in the default build")
	}
	if d.fixedInt16Ready(0) {
		t.Error("Decoder.fixedInt16Ready() must be a constant false in the default build")
	}
	if d.fixedSnapshotHandled() {
		t.Error("Decoder.fixedSnapshotHandled() must be a constant false in the default build")
	}
	if d.celtDecodeLostFixedAPIRate(0) {
		t.Error("Decoder.celtDecodeLostFixedAPIRate() must be a constant false in the default build")
	}
	if d.armFixedHybridLost(0, false) {
		t.Error("Decoder.armFixedHybridLost() must be a constant false in the default build")
	}
	if d.finishFixedHybridLost(0) {
		t.Error("Decoder.finishFixedHybridLost() must be a constant false in the default build")
	}

	e := &Encoder{}
	if e.is96kHz() {
		t.Error("Encoder.is96kHz() must be a constant false in the default build")
	}
}
