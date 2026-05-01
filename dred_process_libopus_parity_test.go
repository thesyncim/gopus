//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
)

func TestStandaloneDREDProcessMatchesLibopusOnRealPacket(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	want, err := probeLibopusDREDProcess(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate)
	if err != nil {
		t.Skipf("libopus dred process helper unavailable: %v", err)
	}
	if want.availableSamples < 0 {
		t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
	}
	if want.processRet != 0 {
		t.Fatalf("libopus dred process returned error %d", want.processRet)
	}
	if want.processStage != 2 {
		t.Fatalf("libopus processStage=%d want 2", want.processStage)
	}

	dec := NewDREDDecoder()
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		for _, name := range dnnblob.RequiredDREDDecoderRecordNames() {
			if !blob.HasRecord(name) {
				t.Fatalf("ValidateDREDDecoderControl(real model) error: %v (missing record %q)", err, name)
			}
		}
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	if _, err := rdovae.LoadDecoder(blob); err != nil {
		t.Fatalf("rdovae.LoadDecoder(real model) error: %v", err)
	}
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if available != want.availableSamples {
		t.Fatalf("available=%d want %d", available, want.availableSamples)
	}
	if dredEnd != want.dredEndSamples {
		t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
	}
	if got := dred.LatentCount(); got != want.nbLatents {
		t.Fatalf("LatentCount()=%d want %d", got, want.nbLatents)
	}
	state := make([]float32, len(want.state))
	if n := dred.FillState(state); n != len(want.state) {
		t.Fatalf("FillState count=%d want %d", n, len(want.state))
	}
	assertFloat32BitsEqual(t, state, want.state[:], "state")
	latents := make([]float32, want.nbLatents*internaldred.LatentStride)
	if n := dred.FillLatents(latents); n != len(latents) {
		t.Fatalf("FillLatents count=%d want %d", n, len(latents))
	}
	assertFloat32BitsEqual(t, latents, want.latents, "latents")

	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if got := dred.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := dred.FeatureCount(); got != len(want.features) {
		t.Fatalf("FeatureCount()=%d want %d", got, len(want.features))
	}
	features := make([]float32, dred.FeatureCount())
	if n := dred.FillFeatures(features); n != len(features) {
		t.Fatalf("FillFeatures count=%d want %d", n, len(features))
	}
	assertFloat32ApproxEqual(t, features, want.features, "features", 1e-1)
}

func TestStandaloneDREDProcessLifecycleMatchesLibopusOnRealPacket(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	want, err := probeLibopusDREDProcess(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate)
	if err != nil {
		t.Skipf("libopus dred process helper unavailable: %v", err)
	}
	if want.processRet != 0 || want.processStage != 2 {
		t.Fatalf("libopus process=(ret=%d, stage=%d) want (0,2)", want.processRet, want.processStage)
	}
	if want.secondProcessRet != 0 || want.secondStage != 2 {
		t.Fatalf("libopus second process=(ret=%d, stage=%d) want (0,2)", want.secondProcessRet, want.secondStage)
	}
	if want.cloneProcessRet != 0 || want.cloneStage != 2 {
		t.Fatalf("libopus clone process=(ret=%d, stage=%d) want (0,2)", want.cloneProcessRet, want.cloneStage)
	}

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	if _, _, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("first Process error: %v", err)
	}
	firstState := append([]float32(nil), dred.decoded.State[:]...)
	firstLatents := append([]float32(nil), dred.decoded.Latents[:dred.decoded.NbLatents*internaldred.LatentStride]...)
	firstFeatures := append([]float32(nil), dred.decoded.Features[:dred.decoded.NbLatents*4*internaldred.NumFeatures]...)
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("second Process error: %v", err)
	}
	if got := dred.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	assertFloat32BitsEqual(t, dred.decoded.State[:], firstState, "second state")
	assertFloat32BitsEqual(t, dred.decoded.Latents[:dred.decoded.NbLatents*internaldred.LatentStride], firstLatents, "second latents")
	assertFloat32BitsEqual(t, dred.decoded.Features[:dred.decoded.NbLatents*4*internaldred.NumFeatures], firstFeatures, "second features")

	clone := NewDRED()
	if err := dec.Process(dred, clone); err != nil {
		t.Fatalf("clone Process error: %v", err)
	}
	if got := clone.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("clone ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	assertFloat32BitsEqual(t, clone.decoded.State[:], firstState, "clone state")
	assertFloat32BitsEqual(t, clone.decoded.Latents[:clone.decoded.NbLatents*internaldred.LatentStride], firstLatents, "clone latents")
	assertFloat32BitsEqual(t, clone.decoded.Features[:clone.decoded.NbLatents*4*internaldred.NumFeatures], firstFeatures, "clone features")
}

func assertFloat32ApproxEqual(t *testing.T, got, want []float32, label string, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		diff := math.Abs(float64(got[i] - want[i]))
		if diff > tol {
			t.Fatalf("%s[%d]=%g want %g (|diff|=%g > %g)", label, i, got[i], want[i], diff, tol)
		}
	}
}
