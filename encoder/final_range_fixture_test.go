package encoder

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

type finalRangeVariantFixtureFile struct {
	Cases []finalRangeVariantFixtureCase `json:"cases"`
}

type finalRangeVariantFixtureCase struct {
	Name         string                           `json:"name"`
	Variant      string                           `json:"variant"`
	FrameSize    int                              `json:"frame_size"`
	Channels     int                              `json:"channels"`
	Bitrate      int                              `json:"bitrate"`
	SignalFrames int                              `json:"signal_frames"`
	SignalSHA256 string                           `json:"signal_sha256"`
	Packets      []finalRangeVariantFixturePacket `json:"packets"`
}

type finalRangeVariantFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

func TestSILKFinalRangeUsesLastPacketModeWithCELTSidecar(t *testing.T) {
	const (
		caseName = "SILK-NB-20ms-mono-16k"
		variant  = testsignal.EncoderVariantImpulseTrainV1
	)

	c := loadFinalRangeVariantFixtureCase(t, caseName, variant)
	totalSamples := c.SignalFrames * c.FrameSize * c.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
	if err != nil {
		t.Fatalf("generate signal: %v", err)
	}
	if hash := testsignal.HashFloat32LE(signal); hash != c.SignalSHA256 {
		t.Fatalf("signal hash mismatch: got=%s want=%s", hash, c.SignalSHA256)
	}

	enc := NewEncoder(48000, c.Channels)
	enc.ensureCELTEncoder()
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(c.Bitrate)
	enc.SetBitrateMode(ModeCBR)
	enc.SetComplexity(10)

	samplesPerFrame := c.FrameSize * c.Channels
	packetIndex := 0
	for i := 0; i < c.SignalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pkt := encodeFinalRangeFixtureFrame(t, enc, signal[start:end], c.FrameSize)
		assertFinalRangeFixturePacket(t, c, packetIndex, pkt, enc.FinalRange())
		packetIndex++
	}

	silence := make([]float64, samplesPerFrame)
	for packetIndex < len(c.Packets) {
		pkt, err := enc.Encode(silence, c.FrameSize)
		if err != nil {
			t.Fatalf("flush frame %d: %v", packetIndex, err)
		}
		if len(pkt) == 0 {
			continue
		}
		assertFinalRangeFixturePacket(t, c, packetIndex, pkt, enc.FinalRange())
		packetIndex++
	}
}

func loadFinalRangeVariantFixtureCase(t *testing.T, name, variant string) finalRangeVariantFixtureCase {
	t.Helper()
	path := filepath.Join("..", "testvectors", "testdata", "encoder_compliance_libopus_variants_fixture.json")
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		path = filepath.Join("..", "testvectors", "testdata", "encoder_compliance_libopus_variants_fixture_linux_amd64.json")
	}
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

func encodeFinalRangeFixtureFrame(t *testing.T, enc *Encoder, frame []float32, frameSize int) []byte {
	t.Helper()
	pcm := make([]float64, len(frame))
	const inv24 = 1.0 / 8388608.0
	for i, s := range frame {
		q := math.Floor(0.5 + float64(s)*8388608.0)
		pcm[i] = q * inv24
	}
	pkt, err := enc.Encode(pcm, frameSize)
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

func assertFinalRangeFixturePacket(t *testing.T, c finalRangeVariantFixtureCase, index int, got []byte, gotRange uint32) {
	t.Helper()
	if index >= len(c.Packets) {
		t.Fatalf("unexpected packet %d", index)
	}
	want, err := base64.StdEncoding.DecodeString(c.Packets[index].DataB64)
	if err != nil {
		t.Fatalf("decode fixture packet %d: %v", index, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("packet %d mismatch:\ngot  % x\nwant % x", index, got, want)
	}
	if gotRange != c.Packets[index].FinalRange {
		t.Fatalf(
			"packet %d final range mismatch: got=0x%08x want=0x%08x",
			index,
			gotRange,
			c.Packets[index].FinalRange,
		)
	}
}
