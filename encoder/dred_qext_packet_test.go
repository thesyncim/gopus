//go:build gopus_dred && gopus_qext

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

func assertPacketCarriesDREDBeforeQEXT(t *testing.T, packet, qextPayload []byte) {
	t.Helper()
	qextAt := bytes.Index(packet, append([]byte{byte(qextExtensionID << 1)}, qextPayload...))
	if qextAt < 0 {
		qextAt = bytes.Index(packet, append([]byte{byte(qextExtensionID<<1) | 1, byte(len(qextPayload))}, qextPayload...))
	}
	if qextAt < 0 {
		t.Fatalf("packet missing QEXT extension:\npacket=%x\nqext=%x", packet, qextPayload)
	}
	dredNeedle := []byte{'D', internaldred.ExperimentalVersion}
	dredAt := bytes.Index(packet, dredNeedle)
	if dredAt < 0 {
		t.Fatalf("packet missing final DRED extension:\npacket=%x", packet)
	}
	if dredAt > qextAt {
		t.Fatalf("DRED extension offset=%d after QEXT offset=%d", dredAt, qextAt)
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
	assertPacketCarriesDREDBeforeQEXT(t, packet, qextPayload)
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
	assertPacketCarriesDREDBeforeQEXT(t, packet, qextPayload)
}

func TestMaybeBuildLongCELTDREDQEXTPacketCarriesBothExtensions(t *testing.T) {
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

	framesIn := [][]byte{
		bytes.Repeat([]byte{0xAA}, 40),
		bytes.Repeat([]byte{0xBB}, 42),
	}
	qextExtensions := []packetExtension{
		{ID: qextExtensionID, Data: []byte{0x11, 0x22}, Frame: 0},
		{ID: qextExtensionID, Data: []byte{0x33, 0x44, 0x55}, Frame: 1},
	}

	packet, ok, err := enc.maybeBuildMultiFrameDREDPacket(
		framesIn,
		ModeCELT,
		types.BandwidthFullband,
		1920,
		960,
		300,
		false,
		true,
		qextExtensions,
	)
	if err != nil {
		t.Fatalf("maybeBuildMultiFrameDREDPacket: %v", err)
	}
	if !ok {
		t.Fatal("maybeBuildMultiFrameDREDPacket()=false want true")
	}
	padding, frames := code3PaddingForEncoderTest(t, packet)
	if frames != 2 {
		t.Fatalf("frames=%d want=2", frames)
	}
	dredAt := bytes.Index(padding, []byte{'D', internaldred.ExperimentalVersion})
	if dredAt < 0 {
		t.Fatalf("packet padding missing DRED extension:\npacket=%x\npadding=%x", packet, padding)
	}
	qextAt := bytes.Index(padding, []byte{byte(qextExtensionID<<1) | 1})
	if qextAt < 0 {
		t.Fatalf("packet padding missing frame-0 QEXT extension:\npacket=%x\npadding=%x", packet, padding)
	}
	if dredAt > qextAt {
		t.Fatalf("DRED extension offset=%d after QEXT offset=%d in padding=%x", dredAt, qextAt, padding)
	}
	if !bytes.Contains(padding, qextExtensions[1].Data) {
		t.Fatalf("padding missing frame-1 QEXT payload:\npadding=%x\npayload=%x", padding, qextExtensions[1].Data)
	}
}
