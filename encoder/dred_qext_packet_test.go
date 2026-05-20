//go:build gopus_dred && gopus_qext
// +build gopus_dred,gopus_qext

package encoder

import (
	"bytes"
	"math"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/types"
)

func seedDREDPacketRuntimeForQEXTTest(t *testing.T, enc *Encoder) {
	t.Helper()
	runtime := enc.ensureActiveDREDRuntime()
	if runtime == nil {
		t.Fatal("ensureActiveDREDRuntime() returned nil")
	}
	for i := 0; i < internaldred.StateDim; i++ {
		runtime.stateBuffer[i] = 0.02 * float32((i%7)-3)
	}
	for i := 0; i < 6*internaldred.LatentDim; i++ {
		runtime.latentsBuffer[i] = 0.03 * float32((i%9)-4)
	}
	for i := range runtime.activity {
		runtime.activity[i] = 1
	}
	runtime.latentsFill = 6
	runtime.dredOffset = 12
	runtime.latentOffset = 0
}

func assertPacketCarriesQEXTBeforeDRED(t *testing.T, packet, qextPayload []byte) {
	t.Helper()
	qextNeedle := append([]byte{byte(qextExtensionID<<1) | 1, byte(len(qextPayload))}, qextPayload...)
	qextAt := bytes.Index(packet, qextNeedle)
	if qextAt < 0 {
		t.Fatalf("packet missing non-final QEXT extension:\npacket=%x\nqext=%x", packet, qextPayload)
	}
	dredNeedle := []byte{byte(internaldred.ExtensionID << 1), 'D', internaldred.ExperimentalVersion}
	dredAt := bytes.Index(packet, dredNeedle)
	if dredAt < 0 {
		t.Fatalf("packet missing final DRED extension:\npacket=%x", packet)
	}
	if qextAt > dredAt {
		t.Fatalf("QEXT extension offset=%d after DRED offset=%d", qextAt, dredAt)
	}
}

func TestMaybeBuildSingleFrameDREDPacketCarriesQEXTAndDRED(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	enc.SetPacketLoss(20)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(8); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	seedDREDPacketRuntimeForQEXTTest(t, enc)

	frameData := make([]byte, 40)
	qextPayload := []byte{0x11, 0x22, 0x33}
	packet, ok, err := enc.maybeBuildSingleFrameDREDPacket(
		frameData,
		ModeCELT,
		types.BandwidthFullband,
		960,
		false,
		[]packetExtension{{ID: qextExtensionID, Data: qextPayload}},
	)
	if err != nil {
		t.Fatalf("maybeBuildSingleFrameDREDPacket() error: %v", err)
	}
	if !ok {
		t.Fatal("maybeBuildSingleFrameDREDPacket()=false want true")
	}
	if packet[0]&0x03 != 0x03 {
		t.Fatalf("toc code=%d want 3", packet[0]&0x03)
	}
	if packet[1]&0x40 == 0 {
		t.Fatalf("count byte=0x%02x missing padding flag", packet[1])
	}
	assertPacketCarriesQEXTBeforeDRED(t, packet, qextPayload)
}

func TestEncodeCELTDREDQEXTPacketCarriesBothExtensions(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	enc.SetPacketLoss(20)
	enc.SetQEXT(true)
	enc.SetDNNBlob(mustMakeLoadableDREDEncoderBlob(t))
	if err := enc.SetDREDDuration(8); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	seedDREDPacketRuntimeForQEXTTest(t, enc)

	pcm := make([]float64, 960)
	for i := range pcm {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[i] = 0.45 * math.Sin(phase)
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	qextPayload := enc.celtEncoder.LastQEXTPayload()
	if len(qextPayload) == 0 {
		t.Fatal("CELT encoder retained empty QEXT payload")
	}
	assertPacketCarriesQEXTBeforeDRED(t, packet, qextPayload)
}
