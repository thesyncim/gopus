package encoder

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

type finalRangeVariantFixtureFile struct {
	Cases []finalRangeVariantFixtureCase `json:"cases"`
}

type finalRangeVariantFixtureCase struct {
	Name         string `json:"name"`
	Variant      string `json:"variant"`
	FrameSize    int    `json:"frame_size"`
	Channels     int    `json:"channels"`
	Bitrate      int    `json:"bitrate"`
	SignalFrames int    `json:"signal_frames"`
	SignalSHA256 string `json:"signal_sha256"`
}

func TestSILKFinalRangeUsesLastPacketModeWithCELTSidecar(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		caseName             = "SILK-NB-20ms-mono-16k"
		variant              = testsignal.EncoderVariantImpulseTrainV1
		expectedSignalFrames = 50
		expectedPacketCount  = expectedSignalFrames + 1 // one silence flush frame
	)

	c := loadFinalRangeVariantFixtureCase(t, caseName, variant)
	if c.SignalFrames != expectedSignalFrames {
		t.Fatalf("fixture signal frames=%d want=%d", c.SignalFrames, expectedSignalFrames)
	}
	if c.FrameSize != 960 || c.Channels != 1 || c.Bitrate != 16000 {
		t.Fatalf("fixture config=%dHz-frame/%d-ch/%dbit-s: want 960/1/16000", c.FrameSize, c.Channels, c.Bitrate)
	}
	totalSamples := c.SignalFrames * c.FrameSize * c.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
	if err != nil {
		t.Fatalf("generate signal: %v", err)
	}
	if hash := testsignal.HashFloat32LE(signal); hash != c.SignalSHA256 {
		t.Fatalf("signal hash mismatch: got=%s want=%s", hash, c.SignalSHA256)
	}

	// Match encodeFinalRangeFixtureFrame's float32-to-24-bit quantization before
	// passing the same frames through libopus opus_encode_float. The final frame
	// is the zero-valued silence flush used by the Go side below.
	const inv24 = 1.0 / 8388608.0
	samplesPerFrame := c.FrameSize * c.Channels
	oraclePCM := make([]float32, expectedPacketCount*samplesPerFrame)
	for i, sample := range signal {
		q := math.Floor(0.5 + float64(sample)*8388608.0)
		oraclePCM[i] = float32(q * inv24)
	}
	oracleRecords, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate:    48000,
		Channels:      c.Channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     libopustest.EncodeDiffForceModeSILKOnly,
		Bandwidth:     libopustest.EncodeDiffBandwidthNarrowband,
		Bitrate:       c.Bitrate,
		Complexity:    10,
		Signal:        libopustest.EncodeDiffSignalAuto,
		VBR:           false,
		VBRConstraint: true,
		LSBDepth:      24,
		FrameSize:     c.FrameSize,
		FrameCount:    expectedPacketCount,
		PCM:           oraclePCM,
	})
	if err != nil {
		t.Fatalf("build/run sidecar final-range C reference: %v", err)
	}
	if len(oracleRecords) != expectedPacketCount {
		t.Fatalf("C records=%d want=%d", len(oracleRecords), expectedPacketCount)
	}
	for i, record := range oracleRecords {
		if record.Ret <= 0 || len(record.Packet) != record.Ret {
			t.Fatalf("C frame %d: unexpected encode result ret=%d packetLen=%d", i, record.Ret, len(record.Packet))
		}
	}
	t.Logf(
		"live C oracle input: raw-signal-sha256=%s quantized-signal-pcm-sha256=%s quantized-51-frame-pcm-sha256=%s frames=%d silence-frames=1",
		c.SignalSHA256,
		testsignal.HashFloat32LE(oraclePCM[:len(signal)]),
		testsignal.HashFloat32LE(oraclePCM),
		expectedPacketCount,
	)

	enc := NewEncoder(48000, c.Channels)
	enc.ensureCELTEncoder()
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(c.Bitrate)
	enc.SetBitrateMode(ModeCBR)
	enc.SetVBRConstraint(true)
	enc.SetComplexity(10)
	enc.SetSignalType(types.SignalAuto)
	enc.SetLSBDepth(24)

	packetIndex := 0
	for i := 0; i < c.SignalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pkt := encodeFinalRangeFixtureFrame(t, enc, signal[start:end], c.FrameSize)
		assertFinalRangeOraclePacket(t, oracleRecords, packetIndex, pkt, enc.FinalRange())
		packetIndex++
	}

	silence := make([]float64, samplesPerFrame)
	if packetIndex != c.SignalFrames {
		t.Fatalf("signal packet count=%d want=%d", packetIndex, c.SignalFrames)
	}
	pkt, err := encodeTest(enc, silence, c.FrameSize)
	if err != nil {
		t.Fatalf("flush frame %d: %v", packetIndex, err)
	}
	if len(pkt) == 0 {
		t.Fatalf("flush frame %d: Go encoder returned no packet", packetIndex)
	}
	assertFinalRangeOraclePacket(t, oracleRecords, packetIndex, pkt, enc.FinalRange())
	packetIndex++
	if packetIndex != expectedPacketCount {
		t.Fatalf("packet count=%d want=%d", packetIndex, expectedPacketCount)
	}
}

func loadFinalRangeVariantFixtureCase(t *testing.T, name, variant string) finalRangeVariantFixtureCase {
	t.Helper()
	path := finalRangeVariantFixturePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read variants fixture: %v", err)
	}
	var fixture finalRangeVariantFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse variants fixture: %v", err)
	}
	for _, c := range fixture.Cases {
		if c.Name == name && c.Variant == variant {
			return c
		}
	}
	t.Fatalf("missing variants fixture case %s/%s", name, variant)
	return finalRangeVariantFixtureCase{}
}

func finalRangeVariantFixturePath() string {
	generic := filepath.Join("..", "..", "testvectors", "testdata", "encoder_compliance_libopus_variants_fixture.json")
	ext := filepath.Ext(generic)
	platform := strings.TrimSuffix(generic, ext) + "_" + runtime.GOOS + "_" + runtime.GOARCH + ext
	if _, err := os.Stat(platform); err == nil {
		return platform
	}
	return generic
}

func encodeFinalRangeFixtureFrame(t *testing.T, enc *Encoder, frame []float32, frameSize int) []byte {
	t.Helper()
	pcm := make([]float64, len(frame))
	const inv24 = 1.0 / 8388608.0
	for i, s := range frame {
		q := math.Floor(0.5 + float64(s)*8388608.0)
		pcm[i] = q * inv24
	}
	pkt, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("empty packet")
	}
	out := make([]byte, len(pkt))
	copy(out, pkt)
	return out
}

func assertFinalRangeOraclePacket(t *testing.T, records []libopustest.EncodeDiffRecord, index int, got []byte, gotRange uint32) {
	t.Helper()
	if index >= len(records) {
		t.Fatalf("unexpected packet %d", index)
	}
	want := records[index]
	if !bytes.Equal(got, want.Packet) {
		t.Fatalf("packet %d mismatch:\ngot  % x\nwant % x", index, got, want.Packet)
	}
	if gotRange != want.FinalRange {
		t.Fatalf(
			"packet %d final range mismatch: got=0x%08x want=0x%08x",
			index,
			gotRange,
			want.FinalRange,
		)
	}
}
