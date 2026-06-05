package gopus

import (
	"math"
	"testing"
)

type nopPacketSink struct{}

func (nopPacketSink) WritePacket(packet []byte) (int, error) {
	return len(packet), nil
}

func testCELTPacket() []byte {
	packet := make([]byte, 50)
	packet[0] = 0xF8 // config=31 (CELT FB 20ms), mono, code 0
	for i := 1; i < len(packet); i++ {
		packet[i] = byte(i * 7)
	}
	return packet
}

func testStereoCELTPacket() []byte {
	packet := make([]byte, 50)
	packet[0] = 0xFC // config=31 (CELT FB 20ms), stereo, code 0
	for i := 1; i < len(packet); i++ {
		packet[i] = byte(i * 7)
	}
	return packet
}

func testSineFrame(samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	return pcm
}

func TestHotPathAllocsEncodeFloat32(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := testSineFrame(960)
	packet := make([]byte, 4000)

	for i := 0; i < 5; i++ {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Encode(float32) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsEncodeInt16(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := make([]int16, 960)
	packet := make([]byte, 4000)

	for i := 0; i < 5; i++ {
		if _, err := enc.EncodeInt16(pcm, packet); err != nil {
			t.Fatalf("warmup EncodeInt16: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := enc.EncodeInt16(pcm, packet); err != nil {
			t.Fatalf("EncodeInt16: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Encode(int16) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsEncodeRestrictedSilkLowComplexity(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedSilk})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(32000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(0); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}

	pcm := testSineFrame(960)
	packet := make([]byte, 4000)
	for i := 0; i < 5; i++ {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	})
	if allocs > encodeRestrictedSilkHotPathAllocBudget {
		t.Fatalf("Encode(restricted SILK complexity 0) allocs/op = %.2f, want <= %d", allocs, encodeRestrictedSilkHotPathAllocBudget)
	}
}

func TestHotPathAllocsDecodeFloat32(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testCELTPacket()
	pcm := make([]float32, 960)

	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(float32) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsDecodeInt16(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testCELTPacket()
	pcm := make([]int16, 960)

	if _, err := dec.DecodeInt16(packet, pcm); err != nil {
		t.Fatalf("warmup DecodeInt16: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.DecodeInt16(packet, pcm); err != nil {
			t.Fatalf("DecodeInt16: %v", err)
		}
	})
	// The default (float) build is strictly zero-alloc. Under
	// -tags gopus_fixed_point, DecodeInt16 additionally runs the integer
	// FIXED_POINT CELT decoder for libopus-exact output, which is not yet
	// zero-alloc; allow its documented per-frame allocation budget there.
	if allocs > decodeInt16HotPathAllocBudget {
		t.Fatalf("Decode(int16) allocs/op = %.2f, want <= %d", allocs, decodeInt16HotPathAllocBudget)
	}
}

func TestHotPathAllocsDecodePLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testCELTPacket()
	pcm := make([]float32, 960)

	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}
	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("warmup Decode PLC: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("Decode PLC: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(PLC) allocs/op = %.2f, want 0", allocs)
	}
}

// TestHotPathAllocsDecodeSILKPLCMono guards the SILK packet-loss path: a
// steady-state Decode(nil) after a SILK packet must not allocate in the gopus
// decode entry. The only permitted allocations are the SILK PLC concealment
// kernel's own working buffers (plc.ConcealSILKWithLTP), bounded by the budget.
func TestHotPathAllocsDecodeSILKPLCMono(t *testing.T) {
	packet := encodeFrameForDecodeGuard(t, ApplicationVoIP, 1, BandwidthWideband, 24000)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("warmup Decode PLC: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(300, func() {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("Decode PLC: %v", err)
		}
	})
	if allocs > silkPLCMonoHotPathAllocBudget {
		t.Fatalf("Decode(SILK mono PLC) allocs/op = %.2f, want <= %d", allocs, silkPLCMonoHotPathAllocBudget)
	}
}

// TestHotPathAllocsDecodeSILKPLCStereo guards the stereo SILK packet-loss path.
// As with mono, the gopus decode entry is zero-alloc; the residual is the SILK
// PLC concealment kernel run once per internal channel (mid/side).
func TestHotPathAllocsDecodeSILKPLCStereo(t *testing.T) {
	packet := encodeFrameForDecodeGuard(t, ApplicationVoIP, 2, BandwidthWideband, 32000)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("warmup Decode PLC: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(300, func() {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("Decode PLC: %v", err)
		}
	})
	if allocs > silkPLCStereoHotPathAllocBudget {
		t.Fatalf("Decode(SILK stereo PLC) allocs/op = %.2f, want <= %d", allocs, silkPLCStereoHotPathAllocBudget)
	}
}

func TestHotPathAllocsDecodeStereo(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testStereoCELTPacket()
	pcm := make([]float32, 960*2)

	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode stereo: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode stereo: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(stereo) allocs/op = %.2f, want 0", allocs)
	}
}

// encodeFrameForDecodeGuard warms an encoder and returns a steady-state packet
// for the requested configuration so the decode guard exercises a real SILK /
// Hybrid bitstream rather than a synthetic CELT TOC.
func encodeFrameForDecodeGuard(t *testing.T, app Application, channels int, bw Bandwidth, bitrate int) []byte {
	t.Helper()
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: app})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if bw != 0 {
		if err := enc.SetBandwidth(bw); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}
	}
	if bitrate != 0 {
		if err := enc.SetBitrate(bitrate); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
	}
	pcm := testSineFrame(960 * channels)
	packet := make([]byte, 4000)
	var n int
	for i := 0; i < 6; i++ {
		if n, err = enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		}
	}
	out := make([]byte, n)
	copy(out, packet[:n])
	return out
}

func TestHotPathAllocsDecodeSILKMono(t *testing.T) {
	packet := encodeFrameForDecodeGuard(t, ApplicationVoIP, 1, BandwidthWideband, 24000)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, 960)
	for i := 0; i < 3; i++ {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("warmup Decode: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(SILK mono) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsDecodeHybridMono(t *testing.T) {
	packet := encodeFrameForDecodeGuard(t, ApplicationAudio, 1, BandwidthFullband, 64000)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, 960)
	for i := 0; i < 3; i++ {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("warmup Decode: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(Hybrid mono) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsDecodeInt24(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testCELTPacket()
	pcm := make([]int32, 960)
	if _, err := dec.DecodeInt24(packet, pcm); err != nil {
		t.Fatalf("warmup DecodeInt24: %v", err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.DecodeInt24(packet, pcm); err != nil {
			t.Fatalf("DecodeInt24: %v", err)
		}
	})
	// Default (float) build is strictly zero-alloc; the fixed-point build reuses
	// the int16 integer-decoder budget.
	if allocs > decodeInt16HotPathAllocBudget {
		t.Fatalf("DecodeInt24 allocs/op = %.2f, want <= %d", allocs, decodeInt16HotPathAllocBudget)
	}
}

func TestHotPathAllocsMultistreamEncode(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault: %v", err)
	}
	pcm := testSineFrame(960 * 2)
	packet := make([]byte, 4000)
	for i := 0; i < 6; i++ {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	})
	if allocs > multistreamEncodeHotPathAllocBudget {
		t.Fatalf("Multistream Encode allocs/op = %.2f, want <= %d", allocs, multistreamEncodeHotPathAllocBudget)
	}
}

func TestHotPathAllocsMultistreamDecode(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault: %v", err)
	}
	pcmIn := testSineFrame(960 * 2)
	scratch := make([]byte, 4000)
	var n int
	for i := 0; i < 6; i++ {
		if n, err = enc.Encode(pcmIn, scratch); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		}
	}
	packet := make([]byte, n)
	copy(packet, scratch[:n])

	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault: %v", err)
	}
	pcm := make([]float32, 960*2)
	for i := 0; i < 3; i++ {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("warmup Decode: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs > multistreamDecodeHotPathAllocBudget {
		t.Fatalf("Multistream Decode allocs/op = %.2f, want <= %d", allocs, multistreamDecodeHotPathAllocBudget)
	}
}

// longPacketAllocCase covers a multi-frame (40/60/80/100/120 ms) caller-buffer
// encode path that must stay at 0 allocs/op after warmup.
type longPacketAllocCase struct {
	name      string
	frameSize int
}

var longPacketAllocCases = []longPacketAllocCase{
	{"40ms", 1920},
	{"60ms", 2880},
	{"80ms", 3840},
	{"100ms", 4800},
	{"120ms", 5760},
}

func runLongPacketAllocGuard(t *testing.T, app Application, mode EncoderMode, bw Bandwidth, bitrate, channels int) {
	t.Helper()
	for _, c := range longPacketAllocCases {
		t.Run(c.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: app})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetMode(mode); err != nil {
				t.Fatalf("SetMode: %v", err)
			}
			if err := enc.SetFrameSize(c.frameSize); err != nil {
				t.Fatalf("SetFrameSize: %v", err)
			}
			if err := enc.SetBandwidth(bw); err != nil {
				t.Fatalf("SetBandwidth: %v", err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			pcm := testSineFrame(c.frameSize * channels)
			packet := make([]byte, 4000)
			for i := 0; i < 5; i++ {
				if _, err := enc.Encode(pcm, packet); err != nil {
					t.Fatalf("warmup Encode: %v", err)
				}
			}

			allocs := testing.AllocsPerRun(200, func() {
				if _, err := enc.Encode(pcm, packet); err != nil {
					t.Fatalf("Encode: %v", err)
				}
			})
			if allocs != 0 {
				t.Fatalf("long-packet Encode allocs/op = %.2f, want 0", allocs)
			}
		})
	}
}

func TestHotPathAllocsEncodeLongPacketCELT(t *testing.T) {
	runLongPacketAllocGuard(t, ApplicationAudio, EncoderModeCELT, BandwidthFullband, 128000, 1)
}

func TestHotPathAllocsEncodeLongPacketHybrid(t *testing.T) {
	runLongPacketAllocGuard(t, ApplicationAudio, EncoderModeHybrid, BandwidthFullband, 64000, 1)
}

func TestHotPathAllocsEncodeLongPacketSILK(t *testing.T) {
	runLongPacketAllocGuard(t, ApplicationVoIP, EncoderModeSILK, BandwidthWideband, 24000, 1)
}

func TestHotPathAllocsStreamWriterFloat32(t *testing.T) {
	writer, err := NewWriter(48000, 2, nopPacketSink{}, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	pcmBytes := generateFloat32Bytes(48000, 2, 960, 440.0)

	for i := 0; i < 5; i++ {
		if _, err := writer.Write(pcmBytes); err != nil {
			t.Fatalf("warmup Write: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := writer.Write(pcmBytes); err != nil {
			t.Fatalf("Write: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("stream Writer.Write allocs/op = %.2f, want 0", allocs)
	}
}
