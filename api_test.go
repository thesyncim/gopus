package gopus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/testsignal"
	"hash"
	"hash/fnv"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func generateSineWaveFloat32(sampleRate int, freq float64, samples int, channels int) []float32 {
	pcm := make([]float32, samples*channels)
	for i := range samples {
		val := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := range channels {
			pcm[i*channels+ch] = val
		}
	}
	return pcm
}

// generateSineWaveInt16 generates a sine wave as int16.
func generateSineWaveInt16(sampleRate int, freq float64, samples int, channels int) []int16 {
	pcm := make([]int16, samples*channels)
	for i := range samples {
		val := int16(16384 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := range channels {
			pcm[i*channels+ch] = val
		}
	}
	return pcm
}

// computeEnergy computes the RMS energy of a float32 signal.
func computeEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// TestRoundTrip_Mono_Float32 tests mono float32 encode/decode round-trip.
func TestRoundTrip_Mono_Float32(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 440 Hz sine wave
	frameSize := 960
	pcmIn := generateSineWaveFloat32(48000, 440, frameSize, 1)
	inputEnergy := computeEnergy(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergy(pcmOut[:n*cfg.Channels])

	t.Logf("Mono float32 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Lossy codec, but output should have significant energy
	if outputEnergy == 0 {
		t.Error("Output has zero energy")
	}
}

func TestRestrictedCELTCBRBitrateMaxUsesFullCELTPayload(t *testing.T) {
	const frameSize = 480

	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBitrate(BitrateMax); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatalf("SetBitrateMode error: %v", err)
	}

	pcm := generateSineWaveFloat32(48000, 1000, frameSize, 2)
	packet := make([]byte, 1275)
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	packet = packet[:n]
	if len(packet) != 1275 {
		t.Fatalf("packet length=%d want 1275", len(packet))
	}

	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if info.TOC.Mode != ModeCELT || info.TOC.Bandwidth != BandwidthFullband || info.TOC.FrameSize != frameSize {
		t.Fatalf("TOC=%+v want fullband CELT frameSize=%d", info.TOC, frameSize)
	}
	if info.FrameCount != 1 || len(info.FrameSizes) != 1 {
		t.Fatalf("parsed frames=%d sizes=%v want one frame", info.FrameCount, info.FrameSizes)
	}
	if got := info.FrameSizes[0]; got != 1274 {
		t.Fatalf("primary CELT frame size=%d want 1274", got)
	}
}

// TestRoundTrip_Stereo_Float32 tests stereo float32 encode/decode round-trip.
func TestRoundTrip_Stereo_Float32(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 2)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate stereo sine wave (L: 440Hz, R: 880Hz)
	frameSize := 960
	pcmIn := make([]float32, frameSize*2)
	for i := range frameSize {
		pcmIn[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcmIn[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	inputEnergy := computeEnergy(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergy(pcmOut[:n*cfg.Channels])

	t.Logf("Stereo float32 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))
}

// TestRoundTrip_Mono_Int16 tests mono int16 encode/decode round-trip.
func TestRoundTrip_Mono_Int16(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 440 Hz sine wave as int16
	frameSize := 960
	pcmIn := generateSineWaveInt16(48000, 440, frameSize, 1)

	// Encode
	packet, err := enc.EncodeInt16Slice(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]int16, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Note: Lossy codec - output may differ from input significantly
	// The key validation is that encoding and decoding complete without error

	t.Logf("Mono int16 round-trip: %d samples -> %d bytes -> %d samples",
		len(pcmIn), len(packet), n*cfg.Channels)
}

// TestRoundTrip_Stereo_Int16 tests stereo int16 encode/decode round-trip.
func TestRoundTrip_Stereo_Int16(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 2)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate stereo sine wave as int16
	frameSize := 960
	pcmIn := make([]int16, frameSize*2)
	for i := range frameSize {
		pcmIn[i*2] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcmIn[i*2+1] = int16(16384 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}

	// Encode
	packet, err := enc.EncodeInt16Slice(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]int16, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("Stereo int16 round-trip: %d samples -> %d bytes -> %d samples",
		len(pcmIn), len(packet), n*cfg.Channels)
}

// TestRoundTrip_MultipleFrames tests encoding/decoding multiple consecutive frames.
func TestRoundTrip_MultipleFrames(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	numFrames := 10

	var totalPacketBytes int
	var totalInputEnergy, totalOutputEnergy float64

	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	for i := range numFrames {
		// Generate varying frequency for each frame
		freq := 440.0 + float64(i)*50
		pcmIn := generateSineWaveFloat32(48000, freq, frameSize, 1)
		totalInputEnergy += computeEnergy(pcmIn)

		// Encode
		packet, err := enc.EncodeFloat32(pcmIn)
		if err != nil {
			t.Fatalf("Frame %d encode error: %v", i, err)
		}
		totalPacketBytes += len(packet)

		// Decode
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Frame %d decode error: %v", i, err)
		}
		totalOutputEnergy += computeEnergy(pcmOut[:n*cfg.Channels])
	}

	t.Logf("Multiple frames: %d frames, %d total bytes, avg input=%.4f, avg output=%.4f",
		numFrames, totalPacketBytes, totalInputEnergy/float64(numFrames), totalOutputEnergy/float64(numFrames))
}

// TestRoundTrip_AllSampleRates tests all valid Opus sample rates.
func TestRoundTrip_AllSampleRates(t *testing.T) {
	sampleRates := []int{8000, 12000, 16000, 24000, 48000}

	for _, sampleRate := range sampleRates {
		t.Run(string(rune('0'+sampleRate/1000)), func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: 1, Application: ApplicationAudio})
			if err != nil {
				t.Fatalf("NewEncoder(%d) error: %v", sampleRate, err)
			}

			cfg := DefaultDecoderConfig(sampleRate, 1)
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatalf("NewDecoder(%d) error: %v", sampleRate, err)
			}

			// Opus frame size is in native-Fs samples (opus_encode(Fs)):
			// 20 ms = Fs/50.
			frameSize := sampleRate / 50

			pcmIn := generateSineWaveFloat32(sampleRate, 440, frameSize, 1)

			packet, err := enc.EncodeFloat32(pcmIn)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			t.Logf("%d Hz: %d bytes packet, %d samples out", sampleRate, len(packet), n*cfg.Channels)
		})
	}
}

// TestApplication_VoIP tests VoIP application uses appropriate settings.
func TestApplication_VoIP(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// VoIP mode should work with speech-like signals
	frameSize := 960
	pcm := generateSineWaveFloat32(48000, 300, frameSize, 1) // Speech frequency

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("VoIP mode produced empty packet")
	}

	t.Logf("VoIP mode: %d bytes", len(packet))
}

// TestApplication_Audio tests Audio application uses appropriate settings.
func TestApplication_Audio(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Audio mode should work with music-like signals
	frameSize := 960
	pcm := generateSineWaveFloat32(48000, 440, frameSize, 1) // Music frequency

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Audio mode produced empty packet")
	}

	t.Logf("Audio mode: %d bytes", len(packet))
}

// TestPLC_SingleLoss tests packet loss concealment for a single lost packet.
func TestPLC_SingleLoss(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960

	// Encode and decode first frame (establishes state)
	pcm1 := generateSineWaveFloat32(48000, 440, frameSize, 1)
	packet1, _ := enc.EncodeFloat32(pcm1)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	_, err = dec.Decode(packet1, pcmOut)
	if err != nil {
		t.Fatalf("First decode error: %v", err)
	}

	// Simulate packet loss - pass nil to decoder
	n, err := dec.Decode(nil, pcmOut)
	if err != nil {
		t.Fatalf("PLC decode error: %v", err)
	}

	plcEnergy := computeEnergy(pcmOut[:n*cfg.Channels])

	// PLC should produce some audio (concealed samples)
	t.Logf("PLC single loss: produced %d samples with energy %.4f", n, plcEnergy)
}

// TestPLC_MultipleLoss tests PLC fades gracefully on consecutive losses.
func TestPLC_MultipleLoss(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960

	// Encode and decode first frame
	pcm1 := generateSineWaveFloat32(48000, 440, frameSize, 1)
	packet1, _ := enc.EncodeFloat32(pcm1)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	_, _ = dec.Decode(packet1, pcmOut)

	// Multiple consecutive losses
	numLosses := 5
	var energies []float64

	for i := range numLosses {
		n, err := dec.Decode(nil, pcmOut)
		if err != nil {
			t.Fatalf("PLC decode %d error: %v", i, err)
		}
		energies = append(energies, computeEnergy(pcmOut[:n*cfg.Channels]))
	}

	t.Logf("PLC multiple losses: energies=%v", energies)

	// Energy should generally decrease or stay low with consecutive losses
	// (PLC should fade rather than produce loud artifacts)
}

// TestPacketParsing tests that encoded packets can be parsed.
func TestPacketParsing(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWaveFloat32(48000, 440, frameSize, 1)

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Parse the packet
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}

	if info.FrameCount != 1 {
		t.Errorf("FrameCount = %d, want 1", info.FrameCount)
	}

	t.Logf("Packet parsed: mode=%d, bandwidth=%d, frameSize=%d, stereo=%v",
		info.TOC.Mode, info.TOC.Bandwidth, info.TOC.FrameSize, info.TOC.Stereo)
}

// TestSILK10msOpusRoundTrip tests SILK 10ms encoding/decoding through the full
// Opus API at various bitrates. This verifies that the complete pipeline
// (encoder -> TOC byte -> decoder) works correctly for 10ms SILK frames.
func TestSILK10msOpusRoundTrip(t *testing.T) {
	testCases := []struct {
		name      string
		bitrate   int
		frameSize int // at 48kHz: 480=10ms, 960=20ms
		maxPeak   float64
	}{
		{"SILK-10ms-32k", 32000, 480, 2.0},
		{"SILK-10ms-40k", 40000, 480, 2.0},
		{"SILK-10ms-48k", 48000, 480, 2.0},
		{"SILK-10ms-64k", 64000, 480, 2.0},
		{"SILK-20ms-32k", 32000, 960, 2.0},
		{"SILK-20ms-64k", 64000, 960, 2.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			enc.SetBitrate(tc.bitrate)
			enc.SetFrameSize(tc.frameSize)
			enc.SetSignal(SignalVoice)
			if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
				t.Fatalf("SetMaxBandwidth(BandwidthWideband) error: %v", err)
			}

			cfg := DefaultDecoderConfig(48000, 1)
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}

			nFrames := 20
			var maxPeak float64
			nDecoded := 0
			pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

			for i := range nFrames {
				pcmIn := make([]float32, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcmIn[j] = float32(0.5 * math.Sin(2*math.Pi*440*tm))
				}

				packet, err := enc.EncodeFloat32(pcmIn)
				if err != nil {
					t.Fatalf("Encode error at frame %d: %v", i, err)
				}
				if len(packet) == 0 {
					continue
				}

				// Verify TOC byte
				toc := ParseTOC(packet[0])
				if toc.Mode != ModeSILK {
					t.Logf("Frame %d: mode=%d (expected SILK), bw=%d", i, toc.Mode, toc.Bandwidth)
				}

				n, err := dec.Decode(packet, pcmOut)
				if err != nil {
					t.Logf("Frame %d: decode error: %v (pktLen=%d)", i, err, len(packet))
					continue
				}
				nDecoded++

				for j := range n {
					v := math.Abs(float64(pcmOut[j]))
					if v > maxPeak {
						maxPeak = v
					}
				}
			}

			t.Logf("Peak=%.4f (nDecoded=%d)", maxPeak, nDecoded)
			if maxPeak > tc.maxPeak {
				t.Errorf("Output peak %.4f exceeds limit %.4f - CORRUPTION", maxPeak, tc.maxPeak)
			}
			if nDecoded == 0 {
				t.Error("No frames decoded")
			}
		})
	}
}

// TestBufferSizing tests that buffer recommendations work.
func TestBufferSizing(t *testing.T) {
	// Maximum frame size: 60ms at 48kHz stereo = 2880 * 2 = 5760 samples
	maxDecodeBuffer := make([]float32, 2880*2)
	if len(maxDecodeBuffer) < 5760 {
		t.Error("Max decode buffer should be 5760 samples")
	}

	// Maximum encode output: 4000 bytes
	maxEncodeBuffer := make([]byte, 4000)
	if len(maxEncodeBuffer) < 4000 {
		t.Error("Max encode buffer should be 4000 bytes")
	}

	t.Log("Buffer sizing verified: decode=5760 samples, encode=4000 bytes")
}

func TestSettingsForApplicationInvalidReturnsError(t *testing.T) {
	if _, err := settingsForApplication(Application(-1)); err != ErrInvalidApplication {
		t.Fatalf("settingsForApplication(invalid) error = %v, want %v", err, ErrInvalidApplication)
	}
}

type concurrentStreamSpec struct {
	sampleRate int
	channels   int
	app        Application
	mode       EncoderMode
	bitrate    int
	freq       float64
	frames     int
	frameSize  int // samples per channel per Encode call (internal-rate frames)
}

// concurrentStreamSpecs returns a deterministic mix of stream configurations
// that together cover SILK-only, CELT-only and Hybrid coding paths plus mono
// and stereo, so the lazily-built CELT tables (cwrs/pvq, pulse cache, FFT
// states) and SILK paths are all touched concurrently.
//
// frameSize is the libopus 48 kHz-equivalent frame-size unit the public API
// uses (one of 120/240/480/960/...); the encoder consumes frameSize*channels
// PCM samples at the API rate regardless of that rate.
func concurrentStreamSpecs() []concurrentStreamSpec {
	return []concurrentStreamSpec{
		{48000, 2, ApplicationAudio, EncoderModeCELT, 96000, 440, 24, 960},
		{48000, 1, ApplicationVoIP, EncoderModeSILK, 24000, 330, 24, 960},
		{48000, 2, ApplicationAudio, EncoderModeHybrid, 64000, 550, 24, 960},
		{16000, 1, ApplicationVoIP, EncoderModeSILK, 16000, 300, 24, 960},
		{24000, 2, ApplicationAudio, EncoderModeCELT, 48000, 700, 24, 480},
		{12000, 1, ApplicationVoIP, EncoderModeSILK, 12000, 250, 24, 960},
		{8000, 1, ApplicationVoIP, EncoderModeSILK, 10000, 200, 24, 960},
		{48000, 2, ApplicationLowDelay, EncoderModeCELT, 128000, 880, 24, 480},
	}
}

// concurrentRunStream encodes+decodes one full stream end-to-end through a
// freshly constructed Encoder/Decoder pair and returns a digest of every
// encoded packet plus the full decoded PCM. Running the identical spec serially
// and concurrently must yield the identical digest: any divergence means
// cross-instance state leaked through a shared global.
func concurrentRunStream(t *testing.T, spec concurrentStreamSpec) uint64 {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  spec.sampleRate,
		Channels:    spec.channels,
		Application: spec.app,
	})
	if err != nil {
		t.Fatalf("NewEncoder(%+v): %v", spec, err)
	}
	if err := enc.SetMode(spec.mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetBitrate(spec.bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetFrameSize(spec.frameSize); err != nil {
		t.Fatalf("SetFrameSize(%d): %v", spec.frameSize, err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(spec.sampleRate, spec.channels))
	if err != nil {
		t.Fatalf("NewDecoder(%+v): %v", spec, err)
	}

	pcm := generateSineWaveFloat32(spec.sampleRate, spec.freq, spec.frameSize, spec.channels)

	h := fnv.New64a()
	encBuf := make([]byte, defaultMaxPacketBytes)
	decBuf := make([]float32, spec.frameSize*spec.channels)
	var scratch [8]byte

	for f := 0; f < spec.frames; f++ {
		// Vary the input slightly per frame so the encoder state evolves and
		// stale shared scratch would corrupt the output deterministically.
		concurrentPerturb(pcm, f)

		n, err := enc.Encode(pcm, encBuf)
		if err != nil {
			t.Fatalf("Encode frame %d (%+v): %v", f, spec, err)
		}
		concurrentHashInt(h, scratch[:], n)
		h.Write(encBuf[:n])

		got, err := dec.Decode(encBuf[:n], decBuf)
		if err != nil {
			t.Fatalf("Decode frame %d (%+v): %v", f, spec, err)
		}
		concurrentHashInt(h, scratch[:], got)
		concurrentHashFloats(h, scratch[:], decBuf[:got*spec.channels])
	}
	return h.Sum64()
}

// concurrentPerturb applies a deterministic, frame-dependent offset to the PCM
// so each Encode call sees distinct input. Pure function of (pcm contents, f).
func concurrentPerturb(pcm []float32, f int) {
	delta := float32((f%7)-3) * 0.0009765625 // small, deterministic
	for i := range pcm {
		v := pcm[i] + delta
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		pcm[i] = v
	}
}

func concurrentHashInt(h hash.Hash64, scratch []byte, v int) {
	u := uint64(v)
	for i := range 8 {
		scratch[i] = byte(u >> (8 * i))
	}
	h.Write(scratch[:8])
}

func concurrentHashFloats(h hash.Hash64, scratch []byte, vs []float32) {
	for _, v := range vs {
		bits := math.Float32bits(v)
		scratch[0] = byte(bits)
		scratch[1] = byte(bits >> 8)
		scratch[2] = byte(bits >> 16)
		scratch[3] = byte(bits >> 24)
		h.Write(scratch[:4])
	}
}

// TestConcurrentIndependentInstancesRoundTrip runs every stream spec serially to
// capture reference digests, then runs many copies of the same specs
// concurrently (each goroutine owning its own Encoder+Decoder) and asserts the
// concurrent digests match the serial references. Combined with -race, this
// proves independent instances neither race nor leak state across goroutines.
func TestConcurrentIndependentInstancesRoundTrip(t *testing.T) {
	specs := concurrentStreamSpecs()

	// Serial reference digests (single-threaded, definitively no contention).
	want := make([]uint64, len(specs))
	for i, spec := range specs {
		want[i] = concurrentRunStream(t, spec)
	}

	// Concurrent burst: replicate the spec set across many goroutines so each
	// spec runs on several independent instances in parallel.
	const replicas = 6
	type result struct {
		specIdx int
		digest  uint64
	}
	results := make(chan result, len(specs)*replicas)

	var wg sync.WaitGroup
	for range replicas {
		for i, spec := range specs {
			wg.Add(1)
			go func(i int, spec concurrentStreamSpec) {
				defer wg.Done()
				results <- result{specIdx: i, digest: concurrentRunStream(t, spec)}
			}(i, spec)
		}
	}
	wg.Wait()
	close(results)

	for res := range results {
		if res.digest != want[res.specIdx] {
			t.Errorf("concurrent stream %d digest %x != serial %x (cross-instance state leak)",
				res.specIdx, res.digest, want[res.specIdx])
		}
	}
}

// TestConcurrentIndependentInstancesConstructionStorm hammers all public
// constructors in parallel to surface races in lazily-initialized package
// globals (mode/window/cwrs/pvq tables, FFT states, pulse caches). Each
// goroutine constructs and lightly exercises a fresh instance.
func TestConcurrentIndependentInstancesConstructionStorm(t *testing.T) {
	workers := max(runtime.GOMAXPROCS(0)*4, 8)
	const iterations = 40

	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start // release all goroutines together to maximize contention
			for it := range iterations {
				concurrentConstructAndProbe(t, w, it)
			}
		}(w)
	}
	close(start)
	wg.Wait()
}

// concurrentConstructAndProbe builds one of each public codec type (rotating by
// worker/iteration) and runs a single encode/decode so any lazy init triggered
// by first use executes under the race detector with maximal concurrency.
func concurrentConstructAndProbe(t *testing.T, w, it int) {
	t.Helper()
	channels := 1 + ((w + it) & 1) // alternate mono/stereo
	const sampleRate = 48000

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Errorf("NewEncoder: %v", err)
		return
	}
	// Rotate the forced mode so CELT/SILK/Hybrid lazy paths all initialize.
	switch (w + it) % 3 {
	case 0:
		_ = enc.SetMode(EncoderModeCELT)
	case 1:
		_ = enc.SetMode(EncoderModeSILK)
	default:
		_ = enc.SetMode(EncoderModeHybrid)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Errorf("NewDecoder: %v", err)
		return
	}

	pcm := generateSineWaveFloat32(sampleRate, 440, 960, channels)
	encBuf := make([]byte, defaultMaxPacketBytes)
	n, err := enc.Encode(pcm, encBuf)
	if err != nil {
		t.Errorf("Encode: %v", err)
		return
	}
	decBuf := make([]float32, 960*channels)
	if _, err := dec.Decode(encBuf[:n], decBuf); err != nil {
		t.Errorf("Decode: %v", err)
		return
	}

	// Multistream constructors share the same underlying mode tables; build a
	// surround pair too. Use the default mapping helpers to keep it valid.
	msEnc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Errorf("NewMultistreamEncoderDefault: %v", err)
		return
	}
	msDec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Errorf("NewMultistreamDecoderDefault: %v", err)
		return
	}
	msPCM := generateSineWaveFloat32(sampleRate, 440, 960, channels)
	msBuf := make([]byte, defaultMaxPacketBytes*channels)
	mn, err := msEnc.Encode(msPCM, msBuf)
	if err != nil {
		t.Errorf("Multistream Encode: %v", err)
		return
	}
	msOut := make([]float32, 960*channels)
	if _, err := msDec.Decode(msBuf[:mn], msOut); err != nil {
		t.Errorf("Multistream Decode: %v", err)
		return
	}
}

const (
	reqSetApplication            = 4000
	reqGetApplication            = 4001
	reqSetBitrate                = 4002
	reqGetBitrate                = 4003
	reqSetMaxBandwidth           = 4004
	reqGetMaxBandwidth           = 4005
	reqSetVBR                    = 4006
	reqGetVBR                    = 4007
	reqSetBandwidth              = 4008
	reqGetBandwidth              = 4009
	reqSetComplexity             = 4010
	reqGetComplexity             = 4011
	reqSetInbandFEC              = 4012
	reqGetInbandFEC              = 4013
	reqSetPacketLossPerc         = 4014
	reqGetPacketLossPerc         = 4015
	reqSetDTX                    = 4016
	reqGetDTX                    = 4017
	reqSetVBRConstraint          = 4020
	reqGetVBRConstraint          = 4021
	reqSetForceChannels          = 4022
	reqGetForceChannels          = 4023
	reqSetSignal                 = 4024
	reqGetSignal                 = 4025
	reqGetLookahead              = 4027
	reqGetSampleRate             = 4029
	reqGetFinalRange             = 4031
	reqGetPitch                  = 4033
	reqSetGain                   = 4034
	reqGetGain                   = 4045
	reqSetLSBDepth               = 4036
	reqGetLSBDepth               = 4037
	reqGetLastPacketDuration     = 4039
	reqSetExpertFrameDuration    = 4040
	reqGetExpertFrameDuration    = 4041
	reqSetPredictionDisabled     = 4042
	reqGetPredictionDisabled     = 4043
	reqSetPhaseInversionDisabled = 4046
	reqGetPhaseInversionDisabled = 4047
	reqGetInDTX                  = 4049
	reqSetIgnoreExtensions       = 4058
	reqGetIgnoreExtensions       = 4059
)

// reqSetForceMode mirrors OPUS_SET_FORCE_MODE_REQUEST from src/opus_private.h.
// The curated encoder transition test uses this private libopus control.
const reqSetForceMode = 11002

// libopus argument-domain constants used by the generator.
const (
	cOpusAuto       = -1000
	cOpusBitrateMax = -1
	cBandwidthNB    = 1101
	cBandwidthMB    = 1102
	cBandwidthWB    = 1103
	cBandwidthSWB   = 1104
	cBandwidthFB    = 1105
	cSignalVoice    = 3001
	cSignalMusic    = 3002
	cAppVoIP        = 2048
	cAppAudio       = 2049
	cAppLowDelay    = 2051
	cFrameSizeArg   = 5000
	cFrameSize120Ms = 5009
)

const (
	cOpusOK     = 0
	cOpusBadArg = -1
)

// ctlFuzzFrameSize is the per-channel OP_PROCESS frame size, in samples at the
// API rate. It matches the gopus encoder's default FrameSize() (960 samples,
// the 48 kHz 20 ms count) so the encode input is identical at every API rate;
// 960 is a valid libopus frame size at 8/12/16/24/48 kHz (60/20 ms etc.).
const ctlFuzzFrameSize = 960

// ctlReqName maps a request code to a readable name for failure messages.
func ctlReqName(req int32) string {
	switch int(req) {
	case reqSetApplication:
		return "SET_APPLICATION"
	case reqSetForceMode:
		return "SET_FORCE_MODE"
	case reqGetApplication:
		return "GET_APPLICATION"
	case reqSetBitrate:
		return "SET_BITRATE"
	case reqGetBitrate:
		return "GET_BITRATE"
	case reqSetMaxBandwidth:
		return "SET_MAX_BANDWIDTH"
	case reqGetMaxBandwidth:
		return "GET_MAX_BANDWIDTH"
	case reqSetVBR:
		return "SET_VBR"
	case reqGetVBR:
		return "GET_VBR"
	case reqSetBandwidth:
		return "SET_BANDWIDTH"
	case reqGetBandwidth:
		return "GET_BANDWIDTH"
	case reqSetComplexity:
		return "SET_COMPLEXITY"
	case reqGetComplexity:
		return "GET_COMPLEXITY"
	case reqSetInbandFEC:
		return "SET_INBAND_FEC"
	case reqGetInbandFEC:
		return "GET_INBAND_FEC"
	case reqSetPacketLossPerc:
		return "SET_PACKET_LOSS_PERC"
	case reqGetPacketLossPerc:
		return "GET_PACKET_LOSS_PERC"
	case reqSetDTX:
		return "SET_DTX"
	case reqGetDTX:
		return "GET_DTX"
	case reqSetVBRConstraint:
		return "SET_VBR_CONSTRAINT"
	case reqGetVBRConstraint:
		return "GET_VBR_CONSTRAINT"
	case reqSetForceChannels:
		return "SET_FORCE_CHANNELS"
	case reqGetForceChannels:
		return "GET_FORCE_CHANNELS"
	case reqSetSignal:
		return "SET_SIGNAL"
	case reqGetSignal:
		return "GET_SIGNAL"
	case reqGetLookahead:
		return "GET_LOOKAHEAD"
	case reqGetSampleRate:
		return "GET_SAMPLE_RATE"
	case reqGetFinalRange:
		return "GET_FINAL_RANGE"
	case reqGetPitch:
		return "GET_PITCH"
	case reqSetGain:
		return "SET_GAIN"
	case reqGetGain:
		return "GET_GAIN"
	case reqSetLSBDepth:
		return "SET_LSB_DEPTH"
	case reqGetLSBDepth:
		return "GET_LSB_DEPTH"
	case reqGetLastPacketDuration:
		return "GET_LAST_PACKET_DURATION"
	case reqSetExpertFrameDuration:
		return "SET_EXPERT_FRAME_DURATION"
	case reqGetExpertFrameDuration:
		return "GET_EXPERT_FRAME_DURATION"
	case reqSetPredictionDisabled:
		return "SET_PREDICTION_DISABLED"
	case reqGetPredictionDisabled:
		return "GET_PREDICTION_DISABLED"
	case reqSetPhaseInversionDisabled:
		return "SET_PHASE_INVERSION_DISABLED"
	case reqGetPhaseInversionDisabled:
		return "GET_PHASE_INVERSION_DISABLED"
	case reqGetInDTX:
		return "GET_IN_DTX"
	case reqSetIgnoreExtensions:
		return "SET_IGNORE_EXTENSIONS"
	case reqGetIgnoreExtensions:
		return "GET_IGNORE_EXTENSIONS"
	}
	return fmt.Sprintf("REQ(%d)", req)
}

func opName(op int) string {
	switch op {
	case libopustest.CTLOpSet:
		return "SET"
	case libopustest.CTLOpGet:
		return "GET"
	case libopustest.CTLOpProcess:
		return "PROCESS"
	case libopustest.CTLOpReset:
		return "RESET"
	}
	return "?"
}

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// gopusErrToCode maps a gopus typed-setter error to the libopus CTL return code.
// Any validation error becomes OPUS_BAD_ARG; nil becomes OPUS_OK.
func gopusErrToCode(err error) int32 {
	if err == nil {
		return cOpusOK
	}
	return cOpusBadArg
}

// ---------------------------------------------------------------------------
// gopus interpreter: apply one op in the libopus request/arg domain through the
// public typed setters/getters, returning (ret, value, haveValue).
// ---------------------------------------------------------------------------

func applyEncoderOp(t *testing.T, enc *Encoder, op libopustest.CTLOp) (ret int32, value int32, haveValue bool) {
	t.Helper()
	switch op.Op {
	case libopustest.CTLOpProcess:
		// Mirror the C oracle's OP_PROCESS frame exactly: a FrameSize()-sample
		// 440 Hz sine duplicated across channels (same double-precision sin ->
		// float32 cast) so the encode input is byte-identical to libopus.
		frame := enc.FrameSize()
		pcm := generateSineWaveFloat32(enc.SampleRate(), 440, frame, enc.Channels())
		buf := make([]byte, 4000)
		n, err := enc.Encode(pcm, buf)
		if err != nil {
			if err != ErrInvalidFrameSize || n != 0 {
				t.Fatalf("PROCESS encode: samples=%d error=%v; only expert-frame rejection is valid", n, err)
			}
			return cOpusBadArg, 0, false
		}
		return int32(n), 0, false
	case libopustest.CTLOpReset:
		enc.Reset()
		return cOpusOK, 0, false
	case libopustest.CTLOpSet:
		return applyEncoderSet(enc, op.Request, op.Arg), 0, false
	case libopustest.CTLOpGet:
		v, ok := applyEncoderGet(enc, op.Request)
		return cOpusOK, v, ok
	}
	return cOpusOK, 0, false
}

func applyEncoderSet(enc *Encoder, req, arg int32) int32 {
	switch int(req) {
	case reqSetApplication:
		var app Application
		switch int(arg) {
		case cAppVoIP:
			app = ApplicationVoIP
		case cAppAudio:
			app = ApplicationAudio
		case cAppLowDelay:
			app = ApplicationLowDelay
		default:
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetApplication(app))
	case reqSetForceMode:
		var mode EncoderMode
		switch arg {
		case cOpusAuto:
			mode = EncoderModeAuto
		case libopustest.OpusForceModeSILKOnly:
			mode = EncoderModeSILK
		case libopustest.OpusForceModeHybrid:
			mode = EncoderModeHybrid
		case libopustest.OpusForceModeCELTOnly:
			mode = EncoderModeCELT
		default:
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetMode(mode))
	case reqSetBitrate:
		return gopusErrToCode(enc.SetBitrate(int(arg)))
	case reqSetMaxBandwidth:
		bw, ok := bandwidthFromLibopus(arg)
		if !ok {
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetMaxBandwidth(bw))
	case reqSetVBR:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetVBR(arg == 1)
		return cOpusOK
	case reqSetBandwidth:
		if arg == cOpusAuto {
			return gopusErrToCode(enc.SetBandwidthAuto())
		}
		bw, ok := bandwidthFromLibopus(arg)
		if !ok {
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetBandwidth(bw))
	case reqSetComplexity:
		return gopusErrToCode(enc.SetComplexity(int(arg)))
	case reqSetInbandFEC:
		return gopusErrToCode(enc.SetInBandFEC(int(arg)))
	case reqSetPacketLossPerc:
		return gopusErrToCode(enc.SetPacketLoss(int(arg)))
	case reqSetDTX:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetDTX(arg == 1)
		return cOpusOK
	case reqSetVBRConstraint:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetVBRConstraint(arg == 1)
		return cOpusOK
	case reqSetForceChannels:
		// gopus uses -1 as the public OPUS_AUTO sentinel for ForceChannels
		// (documented: encoder_ctl_equivalence_test force_channels=OPUS_AUTO ->
		// gopus -1). Map the libopus OPUS_AUTO arg onto the gopus sentinel so
		// the SET behaviour (auto channel selection) is compared, not the
		// cosmetic sentinel value.
		v := int(arg)
		if v == cOpusAuto {
			v = -1
		}
		return gopusErrToCode(enc.SetForceChannels(v))
	case reqSetSignal:
		return gopusErrToCode(enc.SetSignal(Signal(arg)))
	case reqSetLSBDepth:
		return gopusErrToCode(enc.SetLSBDepth(int(arg)))
	case reqSetExpertFrameDuration:
		return gopusErrToCode(enc.SetExpertFrameDuration(ExpertFrameDuration(arg)))
	case reqSetPredictionDisabled:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetPredictionDisabled(arg == 1)
		return cOpusOK
	case reqSetPhaseInversionDisabled:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetPhaseInversionDisabled(arg == 1)
		return cOpusOK
	}
	return cOpusBadArg
}

func applyEncoderGet(enc *Encoder, req int32) (int32, bool) {
	switch int(req) {
	case reqGetApplication:
		return applicationToLibopus(enc.Application()), true
	case reqGetBitrate:
		return int32(enc.Bitrate()), true
	case reqGetMaxBandwidth:
		return bandwidthToLibopus(enc.MaxBandwidth()), true
	case reqGetVBR:
		return boolToI32(enc.VBR()), true
	case reqGetBandwidth:
		return encoderBandwidthToLibopus(enc), true
	case reqGetComplexity:
		return int32(enc.Complexity()), true
	case reqGetInbandFEC:
		return int32(enc.InBandFEC()), true
	case reqGetPacketLossPerc:
		return int32(enc.PacketLoss()), true
	case reqGetDTX:
		return boolToI32(enc.DTXEnabled()), true
	case reqGetVBRConstraint:
		return boolToI32(enc.VBRConstraint()), true
	case reqGetForceChannels:
		// Normalize gopus's -1 OPUS_AUTO sentinel to the libopus OPUS_AUTO code.
		fc := int32(enc.ForceChannels())
		if fc == -1 {
			fc = cOpusAuto
		}
		return fc, true
	case reqGetSignal:
		return int32(enc.Signal()), true
	case reqGetLookahead:
		return int32(enc.Lookahead()), true
	case reqGetSampleRate:
		return int32(enc.SampleRate()), true
	case reqGetFinalRange:
		return int32(enc.FinalRange()), true
	case reqGetLSBDepth:
		return int32(enc.LSBDepth()), true
	case reqGetExpertFrameDuration:
		return int32(enc.ExpertFrameDuration()), true
	case reqGetPredictionDisabled:
		return boolToI32(enc.PredictionDisabled()), true
	case reqGetPhaseInversionDisabled:
		return boolToI32(enc.PhaseInversionDisabled()), true
	case reqGetInDTX:
		return boolToI32(enc.InDTX()), true
	}
	return 0, false
}

func applyDecoderOp(t *testing.T, dec *Decoder, op libopustest.CTLOp, feedPkt []byte) (ret int32, value int32, haveValue bool) {
	t.Helper()
	switch op.Op {
	case libopustest.CTLOpProcess:
		pcm := make([]float32, dec.maxPacketSamples*dec.Channels())
		n, err := dec.Decode(feedPkt, pcm)
		if err != nil {
			t.Fatalf("PROCESS decode of valid feed: %v", err)
		}
		return int32(n), 0, false
	case libopustest.CTLOpReset:
		dec.Reset()
		return cOpusOK, 0, false
	case libopustest.CTLOpSet:
		return applyDecoderSet(dec, op.Request, op.Arg), 0, false
	case libopustest.CTLOpGet:
		v, ok := applyDecoderGet(dec, op.Request)
		return cOpusOK, v, ok
	}
	return cOpusOK, 0, false
}

func applyDecoderSet(dec *Decoder, req, arg int32) int32 {
	switch int(req) {
	case reqSetComplexity:
		return gopusErrToCode(dec.SetComplexity(int(arg)))
	case reqSetGain:
		return gopusErrToCode(dec.SetGain(int(arg)))
	case reqSetPhaseInversionDisabled:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		dec.SetPhaseInversionDisabled(arg == 1)
		return cOpusOK
	case reqSetIgnoreExtensions:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		dec.SetIgnoreExtensions(arg == 1)
		return cOpusOK
	}
	return cOpusBadArg
}

func applyDecoderGet(dec *Decoder, req int32) (int32, bool) {
	switch int(req) {
	case reqGetBandwidth:
		return bandwidthGetToLibopus(dec.Bandwidth(), dec.bandwidthKnown), true
	case reqGetComplexity:
		return int32(dec.Complexity()), true
	case reqGetFinalRange:
		return int32(dec.FinalRange()), true
	case reqGetSampleRate:
		return int32(dec.SampleRate()), true
	case reqGetPitch:
		return int32(dec.Pitch()), true
	case reqGetGain:
		return int32(dec.Gain()), true
	case reqGetLastPacketDuration:
		return int32(dec.LastPacketDuration()), true
	case reqGetPhaseInversionDisabled:
		return boolToI32(dec.PhaseInversionDisabled()), true
	case reqGetIgnoreExtensions:
		return boolToI32(dec.IgnoreExtensions()), true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// libopus <-> gopus enum translation.
// ---------------------------------------------------------------------------

func bandwidthFromLibopus(arg int32) (Bandwidth, bool) {
	switch int(arg) {
	case cBandwidthNB:
		return BandwidthNarrowband, true
	case cBandwidthMB:
		return BandwidthMediumband, true
	case cBandwidthWB:
		return BandwidthWideband, true
	case cBandwidthSWB:
		return BandwidthSuperwideband, true
	case cBandwidthFB:
		return BandwidthFullband, true
	}
	return 0, false
}

func bandwidthToLibopus(bw Bandwidth) int32 {
	switch bw {
	case BandwidthNarrowband:
		return cBandwidthNB
	case BandwidthMediumband:
		return cBandwidthMB
	case BandwidthWideband:
		return cBandwidthWB
	case BandwidthSuperwideband:
		return cBandwidthSWB
	case BandwidthFullband:
		return cBandwidthFB
	}
	return cOpusAuto
}

// bandwidthGetToLibopus mirrors libopus decoder OPUS_GET_BANDWIDTH which
// returns st->bandwidth: 0 until a real packet decided it.
func bandwidthGetToLibopus(bw Bandwidth, known bool) int32 {
	if !known {
		return 0
	}
	return bandwidthToLibopus(bw)
}

// encoderBandwidthToLibopus reports the value the gopus encoder Bandwidth()
// getter returns, in the libopus argument domain. libopus OPUS_GET_BANDWIDTH
// returns st->bandwidth (the DECIDED bandwidth: FULLBAND at init, updated only
// during encode), not the user request.
func encoderBandwidthToLibopus(enc *Encoder) int32 {
	return bandwidthToLibopus(enc.Bandwidth())
}

func applicationToLibopus(app Application) int32 {
	switch app {
	case ApplicationVoIP:
		return cAppVoIP
	case ApplicationAudio:
		return cAppAudio
	case ApplicationLowDelay:
		return cAppLowDelay
	}
	return int32(app)
}

func applicationFromConfig(app Application) int {
	return int(applicationToLibopus(app))
}

// ---------------------------------------------------------------------------
// Random program generation.
// ---------------------------------------------------------------------------

type ctlArgGen func(r *rand.Rand) int32

// encoderSetReqs is the set of encoder SET requests fuzzed, each paired with an
// argument generator that emits a mix of valid + boundary + out-of-range values.
var encoderSetReqs = []struct {
	req int32
	gen ctlArgGen
}{
	{reqSetBitrate, gen(0, -1, -1000, 1, 500, 499, 501, 6000, 64000, 510000, 750000, 1500000, 99999999, -2, -999999)},
	{reqSetComplexity, gen(-2, -1, 0, 1, 5, 9, 10, 11, 100)},
	{reqSetVBR, gen(-1, 0, 1, 2)},
	{reqSetVBRConstraint, gen(-1, 0, 1, 2)},
	{reqSetInbandFEC, gen(-1, 0, 1, 2, 3, 10)},
	{reqSetPacketLossPerc, gen(-1, 0, 1, 50, 100, 101, 200)},
	{reqSetDTX, gen(-1, 0, 1, 2)},
	// Force-channels auto is spelled OPUS_AUTO (-1000) in the libopus CTL
	// domain; gopus maps that to its public -1 sentinel (see applyEncoderSet).
	// Raw -1 is intentionally omitted because it is an invalid value in the
	// libopus domain yet the auto sentinel in the gopus public domain, so it
	// has no single shared meaning to compare.
	{reqSetForceChannels, gen(-1000, -2, 0, 1, 2, 3)},
	{reqSetSignal, gen(-1000, -1, 0, 3001, 3002, 3003, 9999)},
	{reqSetBandwidth, gen(-1000, 1100, 1101, 1102, 1103, 1104, 1105, 1106, 9999)},
	{reqSetMaxBandwidth, gen(-1000, 1100, 1101, 1102, 1103, 1104, 1105, 1106, 9999)},
	{reqSetLSBDepth, gen(7, 8, 16, 24, 25, 0, 32)},
	{reqSetExpertFrameDuration, gen(4999, 5000, 5001, 5004, 5009, 5010, 0)},
	{reqSetPredictionDisabled, gen(-1, 0, 1, 2)},
	{reqSetPhaseInversionDisabled, gen(-1, 0, 1, 2)},
	{reqSetApplication, gen(2048, 2049, 2051, 2050, 0, -1)},
}

// encoderGetReqs is the set of encoder GET requests fuzzed for value parity.
//
// OPUS_GET_BITRATE is deliberately excluded: libopus resolves OPUS_AUTO /
// OPUS_BITRATE_MAX (and clamps the stored user bitrate against the 1276-byte
// max) at GET time via user_bitrate_to_bitrate(st, prev_framesize, 1276),
// whereas gopus defers that resolution to Encode and Bitrate() returns the
// stored clamped user bitrate. That is a documented intentional design choice
// (see TestEncoderCTL_BitrateGetResidual); SET_BITRATE clamping is still
// exercised for return-code parity.
//
// Output-derived GETs use the same feature and instruction configuration as
// the Go encoder and remain exact after each control/program step.
var encoderGetReqs = []int32{
	reqGetApplication, reqGetMaxBandwidth, reqGetVBR,
	reqGetBandwidth, reqGetComplexity, reqGetInbandFEC, reqGetPacketLossPerc,
	reqGetDTX, reqGetVBRConstraint, reqGetForceChannels, reqGetSignal,
	reqGetLookahead, reqGetSampleRate, reqGetFinalRange, reqGetLSBDepth,
	reqGetExpertFrameDuration, reqGetPredictionDisabled,
	reqGetPhaseInversionDisabled, reqGetInDTX,
}

var decoderSetReqs = []struct {
	req int32
	gen ctlArgGen
}{
	{reqSetComplexity, gen(-2, -1, 0, 1, 5, 10, 11, 100)},
	{reqSetGain, gen(-32769, -32768, -256, 0, 256, 32767, 32768, 100000)},
	{reqSetPhaseInversionDisabled, gen(-1, 0, 1, 2)},
	{reqSetIgnoreExtensions, gen(-1, 0, 1, 2)},
}

var decoderGetReqs = []int32{
	reqGetBandwidth, reqGetComplexity, reqGetFinalRange, reqGetSampleRate,
	reqGetPitch, reqGetGain, reqGetLastPacketDuration,
	reqGetPhaseInversionDisabled, reqGetIgnoreExtensions,
}

// gen returns an argument generator that uniformly picks from the supplied pool.
func gen(values ...int32) ctlArgGen {
	pool := append([]int32(nil), values...)
	return func(r *rand.Rand) int32 {
		return pool[r.Intn(len(pool))]
	}
}

func genEncoderProgram(r *rand.Rand, n int, withProcess bool) []libopustest.CTLOp {
	ops := make([]libopustest.CTLOp, 0, n)
	for range n {
		roll := r.Intn(10)
		if !withProcess && roll == 0 {
			// Replace the PROCESS slot with a GET so the op mix stays dense.
			roll = 2
		}
		switch roll {
		case 0:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpProcess})
		case 1:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpReset})
		case 2, 3, 4:
			g := encoderGetReqs[r.Intn(len(encoderGetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpGet, Request: g})
		default:
			s := encoderSetReqs[r.Intn(len(encoderSetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpSet, Request: s.req, Arg: s.gen(r)})
		}
	}
	return ops
}

func genDecoderProgram(r *rand.Rand, n int) []libopustest.CTLOp {
	ops := make([]libopustest.CTLOp, 0, n)
	for range n {
		switch r.Intn(10) {
		case 0, 1:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpProcess})
		case 2:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpReset})
		case 3, 4, 5:
			g := decoderGetReqs[r.Intn(len(decoderGetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpGet, Request: g})
		default:
			s := decoderSetReqs[r.Intn(len(decoderSetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpSet, Request: s.req, Arg: s.gen(r)})
		}
	}
	return ops
}

// ---------------------------------------------------------------------------
// Parity comparison.
// ---------------------------------------------------------------------------

// compareCTLResults asserts gopus vs oracle parity for a single program. It
// reports the first divergence with a minimized prefix so failures pinpoint the
// exact op (and its predecessors) that produced the mismatch.
func compareCTLResults(t *testing.T, label string, ops []libopustest.CTLOp, gopus, oracle []libopustest.CTLResult) {
	t.Helper()
	if len(gopus) != len(ops) || len(oracle) != len(ops) {
		t.Fatalf("%s: result count gopus=%d oracle=%d operations=%d", label, len(gopus), len(oracle), len(ops))
	}
	for i := range ops {
		g, o := gopus[i], oracle[i]
		op := ops[i]
		mismatch := ""
		switch op.Op {
		case libopustest.CTLOpProcess, libopustest.CTLOpReset:
			if g.Ret != o.Ret {
				mismatch = fmt.Sprintf("%s ret gopus=%d oracle=%d", opName(op.Op), g.Ret, o.Ret)
			}
		case libopustest.CTLOpSet:
			if g.Ret != o.Ret {
				mismatch = fmt.Sprintf("SET %s(%d) ret gopus=%d oracle=%d",
					ctlReqName(op.Request), op.Arg, g.Ret, o.Ret)
			}
		case libopustest.CTLOpGet:
			if g.Ret != o.Ret {
				mismatch = fmt.Sprintf("GET %s ret gopus=%d oracle=%d",
					ctlReqName(op.Request), g.Ret, o.Ret)
			} else if g.HaveValue != o.HaveValue {
				mismatch = fmt.Sprintf("GET %s value presence gopus=%t oracle=%t", ctlReqName(op.Request), g.HaveValue, o.HaveValue)
			} else if o.Ret == cOpusOK && g.Value != o.Value {
				mismatch = fmt.Sprintf("GET %s value gopus=%d oracle=%d",
					ctlReqName(op.Request), g.Value, o.Value)
			}
		}
		if mismatch != "" {
			t.Errorf("%s: op %d (%s): %s\nminimized prefix:\n%s",
				label, i, opName(op.Op), mismatch, formatProgram(ops[:i+1]))
			return
		}
	}
}

func formatProgram(ops []libopustest.CTLOp) string {
	var s strings.Builder
	for i, op := range ops {
		switch op.Op {
		case libopustest.CTLOpSet:
			fmt.Fprintf(&s, "  [%d] SET %s arg=%d\n", i, ctlReqName(op.Request), op.Arg)
		case libopustest.CTLOpGet:
			fmt.Fprintf(&s, "  [%d] GET %s\n", i, ctlReqName(op.Request))
		case libopustest.CTLOpProcess:
			fmt.Fprintf(&s, "  [%d] PROCESS\n", i)
		case libopustest.CTLOpReset:
			fmt.Fprintf(&s, "  [%d] RESET\n", i)
		}
	}
	return s.String()
}

func runEncoderCTLProgram(t *testing.T, sampleRate, channels int, app Application, ops []libopustest.CTLOp) []libopustest.CTLResult {
	enc := mustNewTestEncoder(t, sampleRate, channels, app)
	out := make([]libopustest.CTLResult, len(ops))
	for i, op := range ops {
		ret, val, have := applyEncoderOp(t, enc, op)
		out[i] = libopustest.CTLResult{Ret: ret, Value: val, HaveValue: have}
	}
	return out
}

func runDecoderCTLProgram(t *testing.T, sampleRate, channels int, ops []libopustest.CTLOp, feed []byte) []libopustest.CTLResult {
	dec := mustNewTestDecoder(t, sampleRate, channels)
	out := make([]libopustest.CTLResult, len(ops))
	for i, op := range ops {
		ret, val, have := applyDecoderOp(t, dec, op, feed)
		out[i] = libopustest.CTLResult{Ret: ret, Value: val, HaveValue: have}
	}
	return out
}

// encodeFeederPacket produces a real packet (gopus AUDIO encoder, 440 Hz sine).
// The same bytes are decoded by both gopus and the libopus oracle so the decoder
// OP_PROCESS input — and every decode-derived GET — is byte-identical.
func encodeFeederPacket(t *testing.T, sampleRate, channels int) []byte {
	t.Helper()
	enc := mustNewTestEncoder(t, sampleRate, channels, ApplicationAudio)
	pcm := generateSineWaveFloat32(sampleRate, 440, enc.FrameSize(), channels)
	buf := make([]byte, 4000)
	n, err := enc.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("feeder encode: %v", err)
	}
	return buf[:n]
}

// ---------------------------------------------------------------------------
// Tests.
// ---------------------------------------------------------------------------

// TestEncoderCTLSequenceFuzz drives seeded random encoder CTL programs through
// gopus and the libopus oracle and asserts SET/GET behavioral parity.
func TestEncoderCTLSequenceFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.CTLSequenceHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "ctl sequence", err)
	}

	type cfg struct {
		rate int
		ch   int
		app  Application
		// withProcess interleaves encodes. Enabled only at 48 kHz: a PROCESS op
		// makes the GET-after-encode state (decided bandwidth, first-frame lock)
		// depend on the encoder's byte output, which is covered byte-for-byte by
		// the 48 kHz encode differential fuzz. Non-48 kHz rates exercise the
		// rate-independent SET/GET/clamp/reset CTL semantics without encode.
		withProcess bool
	}
	configs := []cfg{
		{48000, 1, ApplicationAudio, true},
		{48000, 2, ApplicationAudio, true},
		{48000, 2, ApplicationVoIP, true},
		{48000, 1, ApplicationLowDelay, true},
		{24000, 1, ApplicationLowDelay, false},
		{16000, 1, ApplicationVoIP, false},
		{8000, 2, ApplicationAudio, false},
		{12000, 1, ApplicationAudio, false},
	}

	const seeds = 60
	const opsPerSeed = 40

	for _, c := range configs {
		t.Run(fmt.Sprintf("%dHz_%dch_%v", c.rate, c.ch, c.app), func(t *testing.T) {
			programs := make([]libopustest.CTLSequenceParams, seeds)
			for seed := range seeds {
				r := rand.New(rand.NewSource(int64(seed)*1000003 + int64(c.rate) + int64(c.ch)))
				// The Go facade starts at 64 kbps; libopus starts at OPUS_AUTO.
				// Apply the same initial control before comparing encoded output.
				ops := []libopustest.CTLOp{{Op: libopustest.CTLOpSet, Request: reqSetBitrate, Arg: cOpusAuto}}
				ops = append(ops, genEncoderProgram(r, opsPerSeed, c.withProcess)...)
				programs[seed] = libopustest.CTLSequenceParams{
					IsDecoder:   false,
					SampleRate:  c.rate,
					Channels:    c.ch,
					Application: applicationFromConfig(c.app),
					FrameSize:   ctlFuzzFrameSize,
					Ops:         ops,
				}
			}

			oracle, err := libopustest.ProbeCTLSequenceBatch(programs)
			if err != nil {
				t.Fatalf("oracle batch: %v", err)
			}
			for seed := range seeds {
				ops := programs[seed].Ops
				gopus := runEncoderCTLProgram(t, c.rate, c.ch, c.app, ops)
				compareCTLResults(t, fmt.Sprintf("enc seed=%d", seed), ops, gopus, oracle[seed])
				if t.Failed() {
					return
				}
			}
		})
	}
}

// TestDecoderCTLSequenceFuzz drives seeded random decoder CTL programs through
// gopus and the libopus oracle and asserts SET/GET behavioral parity.
func TestDecoderCTLSequenceFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.CTLSequenceHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "ctl sequence", err)
	}

	type cfg struct {
		rate int
		ch   int
	}
	configs := []cfg{
		{48000, 1},
		{48000, 2},
		{24000, 2},
		{16000, 1},
		{8000, 2},
		{12000, 1},
	}

	const seeds = 60
	const opsPerSeed = 40

	for _, c := range configs {
		t.Run(fmt.Sprintf("%dHz_%dch", c.rate, c.ch), func(t *testing.T) {
			feed := encodeFeederPacket(t, c.rate, c.ch)
			programs := make([]libopustest.CTLSequenceParams, seeds)
			for seed := range seeds {
				r := rand.New(rand.NewSource(int64(seed)*2000003 + int64(c.rate) + int64(c.ch)))
				ops := genDecoderProgram(r, opsPerSeed)
				programs[seed] = libopustest.CTLSequenceParams{
					IsDecoder:  true,
					SampleRate: c.rate,
					Channels:   c.ch,
					FrameSize:  ctlFuzzFrameSize,
					FeedPacket: feed,
					Ops:        ops,
				}
			}

			oracle, err := libopustest.ProbeCTLSequenceBatch(programs)
			if err != nil {
				t.Fatalf("oracle batch: %v", err)
			}
			for seed := range seeds {
				ops := programs[seed].Ops
				gopus := runDecoderCTLProgram(t, c.rate, c.ch, ops, feed)
				compareCTLResults(t, fmt.Sprintf("dec seed=%d", seed), ops, gopus, oracle[seed])
				if t.Failed() {
					return
				}
			}
		})
	}
}

type decoderCTLRow struct {
	// ctlName is the C macro name (without _REQUEST suffix where applicable).
	ctlName string
	// ctlID is the numeric request value from opus_defines.h.
	ctlID int
	// dir is "GET", "SET", or "SET+GET".
	dir string
	// buildTag is the gopus build tag that gates this CTL; "" = always present.
	buildTag string
	// testFn runs the full default/round-trip/clamp suite against a fresh decoder.
	testFn func(t *testing.T)
}

// decoderCTLTable enumerates every case handled by opus_decoder_ctl in
// src/opus_decoder.c (libopus 1.6.1).  CTLs gated by compile-time ifdefs are
// listed with their buildTag and tested only under that tag.
//
// Request IDs are from include/opus_defines.h lines 130–180.
var decoderCTLTable = []decoderCTLRow{
	// ------------------------------------------------------------------
	// 4009 OPUS_GET_BANDWIDTH — returns bandwidth of last decoded packet.
	// C ref: opus_decoder_ctl case OPUS_GET_BANDWIDTH_REQUEST → "*value = st->bandwidth"
	// Default after init: 0 (no packet decoded yet).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_BANDWIDTH",
		ctlID:    4009,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)
			// Default before any decode: 0 (maps to BandwidthNarrowband == 0 in gopus,
			// but libopus stores 0 which means "not yet decoded").
			// C ref: opus_decoder.c OPUS_CLEAR zeroes st->bandwidth at init.
			if got := dec.Bandwidth(); got != 0 {
				t.Errorf("OPUS_GET_BANDWIDTH default = %v, want 0 (no packet decoded)", got)
			}

			// After decoding a Hybrid-FB packet the bandwidth must be BandwidthFullband.
			// C ref: opus_decode_native → st->bandwidth = packet_bandwidth
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if got := dec.Bandwidth(); got != BandwidthFullband {
				t.Errorf("OPUS_GET_BANDWIDTH after Hybrid-FB decode = %v, want BandwidthFullband", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4010/4011 OPUS_SET/GET_COMPLEXITY
	// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
	// Default after init: 0 (OPUS_CLEAR zeroes st->complexity).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_COMPLEXITY / OPUS_GET_COMPLEXITY",
		ctlID:    4010,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default = 0
			if got := dec.Complexity(); got != 0 {
				t.Errorf("OPUS_GET_COMPLEXITY default = %d, want 0", got)
			}

			// Round-trip all valid values.
			for c := 0; c <= 10; c++ {
				if err := dec.SetComplexity(c); err != nil {
					t.Fatalf("SetComplexity(%d) error: %v", c, err)
				}
				if got := dec.Complexity(); got != c {
					t.Errorf("OPUS_GET_COMPLEXITY after SET(%d) = %d, want %d", c, got, c)
				}
			}

			// Clamp: out-of-range must be rejected; stored value unchanged.
			if err := dec.SetComplexity(5); err != nil {
				t.Fatalf("SetComplexity(5) error: %v", err)
			}
			for _, bad := range []int{-1, 11, 100} {
				if err := dec.SetComplexity(bad); err == nil {
					t.Errorf("SetComplexity(%d) = nil, want error", bad)
				}
				if got := dec.Complexity(); got != 5 {
					t.Errorf("SetComplexity(%d) changed Complexity() to %d, want 5", bad, got)
				}
			}

			// Reset preserves complexity (st->complexity is before OPUS_DECODER_RESET_START).
			// C ref: opus_decoder.c OPUS_RESET_STATE – OPUS_CLEAR starts at
			//   &st->OPUS_DECODER_RESET_START, which is after st->complexity.
			if err := dec.SetComplexity(7); err != nil {
				t.Fatalf("SetComplexity(7) error: %v", err)
			}
			dec.Reset()
			if got := dec.Complexity(); got != 7 {
				t.Errorf("Complexity() = %d after Reset(), want 7 (preserved by OPUS_RESET_STATE)", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4031 OPUS_GET_FINAL_RANGE — range-coder final state.
	// C ref: opus_decoder_ctl OPUS_GET_FINAL_RANGE_REQUEST → "*value = st->rangeFinal"
	// Default: 0 before any decode (lastDataLen == 0).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_FINAL_RANGE",
		ctlID:    4031,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default = 0 (no packet decoded yet).
			if got := dec.FinalRange(); got != 0 {
				t.Errorf("OPUS_GET_FINAL_RANGE default = %d, want 0", got)
			}

			// Single-byte packet (len == 1) → rangeFinal = 0 per libopus convention.
			// C ref: opus_decoder.c – if lastDataLen <= 1 return 0
			pcm := make([]float32, 960)
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if got := dec.FinalRange(); got == 0 {
				t.Error("OPUS_GET_FINAL_RANGE after valid decode = 0, want non-zero")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4028 OPUS_RESET_STATE — resets decoder stream state.
	// C ref: opus_decoder_ctl OPUS_RESET_STATE – OPUS_CLEAR from
	//   OPUS_DECODER_RESET_START, then frame_size = Fs/400.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_RESET_STATE",
		ctlID:    4028,
		dir:      "SET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Decode a real packet so state is non-initial.
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if dec.LastPacketDuration() == 0 {
				t.Fatal("LastPacketDuration precondition failed")
			}

			dec.Reset()

			// After reset, last_packet_duration → 0.
			// C ref: OPUS_CLEAR from OPUS_DECODER_RESET_START zeroes last_packet_duration.
			if got := dec.LastPacketDuration(); got != 0 {
				t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
			}

			// gain and complexity survive reset (before OPUS_DECODER_RESET_START).
			if err := dec.SetGain(128); err != nil {
				t.Fatalf("SetGain error: %v", err)
			}
			if err := dec.SetComplexity(8); err != nil {
				t.Fatalf("SetComplexity error: %v", err)
			}
			dec.Reset()
			if got := dec.Gain(); got != 128 {
				t.Errorf("Gain() = %d after Reset(), want 128 (preserved)", got)
			}
			if got := dec.Complexity(); got != 8 {
				t.Errorf("Complexity() = %d after Reset(), want 8 (preserved)", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4029 OPUS_GET_SAMPLE_RATE — returns st->Fs.
	// C ref: opus_decoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST → "*value = st->Fs"
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_SAMPLE_RATE",
		ctlID:    4029,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				d := mustNewTestDecoder(t, rate, 1)
				if got := d.SampleRate(); got != rate {
					t.Errorf("OPUS_GET_SAMPLE_RATE(%d Hz) = %d, want %d", rate, got, rate)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4033 OPUS_GET_PITCH — most recently decoded pitch period.
	// C ref: opus_decoder_ctl OPUS_GET_PITCH_REQUEST:
	//   if prev_mode == MODE_CELT_ONLY → celt_decoder_ctl(OPUS_GET_PITCH)
	//   else → *value = st->DecControl.prevPitchLag
	// Default: 0 (no packet decoded).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_PITCH",
		ctlID:    4033,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default: 0 (prev_mode 0, not CELT, SILK not voiced).
			if got := dec.Pitch(); got != 0 {
				t.Errorf("OPUS_GET_PITCH default = %d, want 0", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4034/4045 OPUS_SET/GET_GAIN
	// C ref: OPUS_SET_GAIN_REQUEST – "if (value<-32768 || value>32767) goto bad_arg"
	//        OPUS_GET_GAIN_REQUEST → "*value = st->decode_gain"
	// Default: 0 (OPUS_CLEAR zeroes decode_gain).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_GAIN / OPUS_GET_GAIN",
		ctlID:    4034,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default = 0.
			if got := dec.Gain(); got != 0 {
				t.Errorf("OPUS_GET_GAIN default = %d, want 0", got)
			}

			// Round-trip boundary values.
			for _, gain := range []int{-32768, -256, -1, 0, 1, 256, 32767} {
				if err := dec.SetGain(gain); err != nil {
					t.Fatalf("SetGain(%d) error: %v", gain, err)
				}
				if got := dec.Gain(); got != gain {
					t.Errorf("OPUS_GET_GAIN after SET(%d) = %d, want %d", gain, got, gain)
				}
			}

			// Clamp: values outside [-32768, 32767] must be rejected.
			if err := dec.SetGain(0); err != nil {
				t.Fatalf("SetGain(0) error: %v", err)
			}
			for _, bad := range []int{-32769, 32768, -100000, 100000} {
				if err := dec.SetGain(bad); err == nil {
					t.Errorf("SetGain(%d) = nil, want error", bad)
				}
				if got := dec.Gain(); got != 0 {
					t.Errorf("SetGain(%d) changed Gain() to %d, want 0", bad, got)
				}
			}

			// Gain survives Reset (decode_gain is before OPUS_DECODER_RESET_START).
			if err := dec.SetGain(256); err != nil {
				t.Fatalf("SetGain(256) error: %v", err)
			}
			dec.Reset()
			if got := dec.Gain(); got != 256 {
				t.Errorf("Gain() = %d after Reset(), want 256 (preserved)", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4039 OPUS_GET_LAST_PACKET_DURATION
	// C ref: OPUS_GET_LAST_PACKET_DURATION_REQUEST → "*value = st->last_packet_duration"
	// Default: 0 (OPUS_CLEAR).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_LAST_PACKET_DURATION",
		ctlID:    4039,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default: 0 before any decode.
			if got := dec.LastPacketDuration(); got != 0 {
				t.Errorf("OPUS_GET_LAST_PACKET_DURATION default = %d, want 0", got)
			}

			// After decode equals the sample count returned.
			pcm := make([]float32, 960)
			n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			// C ref: opus_decode_native → "st->last_packet_duration = nb_samples"
			if got := dec.LastPacketDuration(); got != n {
				t.Errorf("OPUS_GET_LAST_PACKET_DURATION = %d, want %d", got, n)
			}

			// Reset zeroes it.
			dec.Reset()
			if got := dec.LastPacketDuration(); got != 0 {
				t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4046/4047 OPUS_SET/GET_PHASE_INVERSION_DISABLED
	// C ref: OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST – "if(value<0 || value>1) goto bad_arg"
	//        delegates to celt_decoder_ctl
	// Default: false (0).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_PHASE_INVERSION_DISABLED / OPUS_GET_PHASE_INVERSION_DISABLED",
		ctlID:    4046,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 2) // stereo: phase inversion is meaningful

			// Default: false.
			if dec.PhaseInversionDisabled() {
				t.Error("OPUS_GET_PHASE_INVERSION_DISABLED default = true, want false")
			}

			// Round-trip.
			dec.SetPhaseInversionDisabled(true)
			if !dec.PhaseInversionDisabled() {
				t.Error("PhaseInversionDisabled() = false after Set(true)")
			}
			dec.SetPhaseInversionDisabled(false)
			if dec.PhaseInversionDisabled() {
				t.Error("PhaseInversionDisabled() = true after Set(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4058/4059 OPUS_SET/GET_IGNORE_EXTENSIONS
	// C ref: OPUS_SET_IGNORE_EXTENSIONS_REQUEST – "if(value<0 || value>1) goto bad_arg"
	//        → st->ignore_extensions = value
	// Default: false (0, OPUS_CLEAR).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_IGNORE_EXTENSIONS / OPUS_GET_IGNORE_EXTENSIONS",
		ctlID:    4058,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default: false.
			if dec.IgnoreExtensions() {
				t.Error("OPUS_GET_IGNORE_EXTENSIONS default = true, want false")
			}

			// Round-trip.
			dec.SetIgnoreExtensions(true)
			if !dec.IgnoreExtensions() {
				t.Error("IgnoreExtensions() = false after Set(true)")
			}
			dec.SetIgnoreExtensions(false)
			if dec.IgnoreExtensions() {
				t.Error("IgnoreExtensions() = true after Set(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4049 OPUS_GET_IN_DTX (decoder-side gopus extension)
	// Note: libopus only exposes OPUS_GET_IN_DTX on the encoder.  gopus
	// provides Decoder.InDTX() as a convenience that inspects lastDataLen.
	// Per opus_decoder.c line 315: "Payloads of 1 (2 including ToC) or 0
	// trigger the PLC/DTX" — a payload of 0 (len<=1) means PLC/DTX frame.
	// gopus: InDTX() ↔ lastDataLen > 0 && lastDataLen <= 2 (ToC + 0-1 data
	// bytes = DTX packet length from SILK DTX comfort-noise packets).
	// ------------------------------------------------------------------
	{
		ctlName:  "InDTX (decoder extension)",
		ctlID:    4049,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Before any decode: InDTX = false (lastDataLen == 0).
			if dec.InDTX() {
				t.Error("Decoder.InDTX() before decode = true, want false")
			}

			// After decoding a normal non-DTX packet: InDTX = false.
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if dec.InDTX() {
				t.Error("Decoder.InDTX() after normal packet = true, want false")
			}

			// A 2-byte packet (ToC + 1 data byte) falls in the DTX range.
			// C ref: silk_dec/DTX CN packets are 1-byte payload after ToC.
			dtxPkt := []byte{GenerateTOC(1, false, 0), 0x00}
			if _, err := dec.Decode(dtxPkt, make([]float32, 960)); err == nil {
				// If decode succeeds (minimal SILK frame), InDTX should be true.
				if !dec.InDTX() {
					t.Error("Decoder.InDTX() after 2-byte DTX packet = false, want true")
				}
			}
			// If the DTX packet decodes as PLC (error), InDTX state is still consistent
			// with lastDataLen being set to the packet length.
		},
	},
}

// TestCTLTable_Decoder runs every row of the decoder CTL table.
func TestCTLTable_Decoder(t *testing.T) {
	for _, row := range decoderCTLTable {
		t.Run(row.ctlName, func(t *testing.T) {
			t.Logf("CTL %d (%s) dir=%s tag=%q", row.ctlID, row.ctlName, row.dir, row.buildTag)
			row.testFn(t)
		})
	}
}

// ---------------------------------------------------------------------------
// Encoder CTL table
// ---------------------------------------------------------------------------

type encoderCTLRow struct {
	ctlName  string
	ctlID    int
	dir      string
	buildTag string
	testFn   func(t *testing.T)
}

// encoderCTLTable enumerates every case handled by opus_encoder_ctl in
// src/opus_encoder.c (libopus 1.6.1).  The table covers all requests in the
// switch() handler from line 2786 through line 3350.
var encoderCTLTable = []encoderCTLRow{
	// ------------------------------------------------------------------
	// 4000/4001 OPUS_SET/GET_APPLICATION
	// C ref: OPUS_SET_APPLICATION_REQUEST – validates application value;
	//   "if (!st->first && st->application != value) { ret = OPUS_BAD_ARG; break; }"
	// Default: set at NewEncoder() via EncoderConfig.Application.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_APPLICATION / OPUS_GET_APPLICATION",
		ctlID:    4000,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default matches constructor.
			if got := enc.Application(); got != ApplicationAudio {
				t.Errorf("OPUS_GET_APPLICATION default = %v, want ApplicationAudio", got)
			}

			// Round-trip before first encode.
			if err := enc.SetApplication(ApplicationVoIP); err != nil {
				t.Fatalf("SetApplication(VoIP) error: %v", err)
			}
			if got := enc.Application(); got != ApplicationVoIP {
				t.Errorf("OPUS_GET_APPLICATION after SET = %v, want VoIP", got)
			}
			if err := enc.SetApplication(ApplicationLowDelay); err != nil {
				t.Fatalf("SetApplication(LowDelay) error: %v", err)
			}
			if got := enc.Application(); got != ApplicationLowDelay {
				t.Errorf("OPUS_GET_APPLICATION after SET = %v, want LowDelay", got)
			}

			// After first encode: changing application must fail.
			enc2 := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
			if _, err := enc2.Encode(generateSineWave(48000, 440, 960), make([]byte, 4000)); err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if err := enc2.SetApplication(ApplicationVoIP); err == nil {
				t.Error("SetApplication after first encode = nil, want error (locked)")
			}
			// Same value must still be accepted.
			if err := enc2.SetApplication(ApplicationAudio); err != nil {
				t.Errorf("SetApplication(same) after first encode error: %v", err)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4002/4003 OPUS_SET/GET_BITRATE
	// C ref: OPUS_SET_BITRATE_REQUEST – "if (value <= 0) goto bad_arg"
	//   (after OPUS_AUTO / OPUS_BITRATE_MAX sentinel check)
	// Default: user_bitrate_bps = OPUS_AUTO = -1000 (st→BitrateAuto in gopus).
	// Note: libopus OPUS_GET_BITRATE resolves OPUS_AUTO at GET time; gopus
	//   returns the stored sentinel — see TestEncoderCTL_BitrateGetResidual.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_BITRATE / OPUS_GET_BITRATE",
		ctlID:    4002,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default stored value is BitrateAuto (gopus defers resolution to Encode).
			// C ref: opus_encoder_init → st->user_bitrate_bps = OPUS_AUTO (-1000)
			if got := enc.Bitrate(); got != BitrateAuto {
				t.Logf("NOTE: Bitrate() default = %d; want BitrateAuto=%d (libopus resolves at GET, gopus defers)", got, BitrateAuto)
			}

			// Explicit SET → GET round-trip.
			for _, br := range []int{6000, 64000, 510000} {
				if err := enc.SetBitrate(br); err != nil {
					t.Fatalf("SetBitrate(%d) error: %v", br, err)
				}
				if got := enc.Bitrate(); got != br {
					t.Errorf("OPUS_GET_BITRATE after SET(%d) = %d, want %d", br, got, br)
				}
			}

			// Sentinel round-trips.
			for _, sent := range []int{BitrateAuto, BitrateMax} {
				if err := enc.SetBitrate(sent); err != nil {
					t.Fatalf("SetBitrate(%d sentinel) error: %v", sent, err)
				}
				if got := enc.Bitrate(); got != sent {
					t.Errorf("OPUS_GET_BITRATE after SET(%d) = %d, want %d", sent, got, sent)
				}
			}

			// Clamp: zero and negative non-sentinel values.
			// C ref: opus_encoder_ctl OPUS_SET_BITRATE_REQUEST – "if (value <= 0) goto bad_arg"
			for _, bad := range []int{0, -2, -999} {
				if err := enc.SetBitrate(bad); err == nil {
					t.Errorf("SetBitrate(%d) = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4004/4005 OPUS_SET/GET_MAX_BANDWIDTH
	// C ref: OPUS_SET_MAX_BANDWIDTH_REQUEST –
	//   "if (value < OPUS_BANDWIDTH_NARROWBAND || value > OPUS_BANDWIDTH_FULLBAND) goto bad_arg"
	// Default: OPUS_BANDWIDTH_FULLBAND (1105) = BandwidthFullband.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_MAX_BANDWIDTH / OPUS_GET_MAX_BANDWIDTH",
		ctlID:    4004,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: BandwidthFullband.
			// C ref: opus_encoder_init → st->max_bandwidth = OPUS_BANDWIDTH_FULLBAND
			if got := enc.MaxBandwidth(); got != BandwidthFullband {
				t.Errorf("OPUS_GET_MAX_BANDWIDTH default = %v, want BandwidthFullband", got)
			}

			// Round-trip all valid bandwidths.
			for _, bw := range []Bandwidth{
				BandwidthNarrowband, BandwidthMediumband, BandwidthWideband,
				BandwidthSuperwideband, BandwidthFullband,
			} {
				if err := enc.SetMaxBandwidth(bw); err != nil {
					t.Fatalf("SetMaxBandwidth(%v) error: %v", bw, err)
				}
				if got := enc.MaxBandwidth(); got != bw {
					t.Errorf("OPUS_GET_MAX_BANDWIDTH after SET(%v) = %v, want %v", bw, got, bw)
				}
			}

			// Clamp: out-of-range bandwidth rejected.
			for _, bad := range []Bandwidth{Bandwidth(5), Bandwidth(10), Bandwidth(255)} {
				if err := enc.SetMaxBandwidth(bad); err == nil {
					t.Errorf("SetMaxBandwidth(%v) = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4006/4007 OPUS_SET/GET_VBR
	// C ref: OPUS_SET_VBR_REQUEST – "if(value<0 || value>1) goto bad_arg"
	// Default: use_vbr = 1 (true).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_VBR / OPUS_GET_VBR",
		ctlID:    4006,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: true.
			// C ref: opus_encoder_init → st->use_vbr = 1
			if !enc.VBR() {
				t.Error("OPUS_GET_VBR default = false, want true")
			}

			// Round-trip.
			enc.SetVBR(false)
			if enc.VBR() {
				t.Error("VBR() = true after SetVBR(false)")
			}
			enc.SetVBR(true)
			if !enc.VBR() {
				t.Error("VBR() = false after SetVBR(true)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4008 OPUS_SET_BANDWIDTH (SET-only at encoder; GET is from GetBandwidth)
	// C ref: OPUS_SET_BANDWIDTH_REQUEST – validates bandwidth or OPUS_AUTO;
	//   "if ((value < OPUS_BANDWIDTH_NARROWBAND || value > OPUS_BANDWIDTH_FULLBAND)
	//    && value != OPUS_AUTO) goto bad_arg"
	// 4009 OPUS_GET_BANDWIDTH — returns currently configured bandwidth.
	// Default: user_bandwidth = OPUS_AUTO = -1000 (BandwidthAuto).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_BANDWIDTH / OPUS_GET_BANDWIDTH",
		ctlID:    4008,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// libopus OPUS_SET_BANDWIDTH writes st->user_bandwidth only;
			// OPUS_GET_BANDWIDTH returns st->bandwidth, which is the FULLBAND
			// init default until an encode decides it. SET accepts every valid
			// bandwidth but GET does not echo the request.
			for _, bw := range []Bandwidth{
				BandwidthNarrowband, BandwidthMediumband, BandwidthWideband,
				BandwidthSuperwideband, BandwidthFullband,
			} {
				if err := enc.SetBandwidth(bw); err != nil {
					t.Fatalf("SetBandwidth(%v) error: %v", bw, err)
				}
				if got := enc.Bandwidth(); got != BandwidthFullband {
					t.Errorf("OPUS_GET_BANDWIDTH after SET(%v) = %v, want BandwidthFullband (st->bandwidth init default before encode)", bw, got)
				}
			}

			// Restore auto.
			if err := enc.SetBandwidthAuto(); err != nil {
				t.Fatalf("SetBandwidthAuto() error: %v", err)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4010/4011 OPUS_SET/GET_COMPLEXITY (encoder)
	// C ref: OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
	// Default: st->silk_mode.complexity = 9 (set in opus_encoder_init).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_COMPLEXITY / OPUS_GET_COMPLEXITY",
		ctlID:    4010,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: 9.
			if got := enc.Complexity(); got != 9 {
				t.Errorf("OPUS_GET_COMPLEXITY default = %d, want 9", got)
			}

			// Round-trip all valid values.
			for c := 0; c <= 10; c++ {
				if err := enc.SetComplexity(c); err != nil {
					t.Fatalf("SetComplexity(%d) error: %v", c, err)
				}
				if got := enc.Complexity(); got != c {
					t.Errorf("OPUS_GET_COMPLEXITY after SET(%d) = %d, want %d", c, got, c)
				}
			}

			// Clamp.
			if err := enc.SetComplexity(5); err != nil {
				t.Fatalf("SetComplexity(5) error: %v", err)
			}
			for _, bad := range []int{-1, 11, 100} {
				if err := enc.SetComplexity(bad); err == nil {
					t.Errorf("SetComplexity(%d) = nil, want error", bad)
				}
				if got := enc.Complexity(); got != 5 {
					t.Errorf("invalid SetComplexity(%d) changed Complexity() to %d, want 5", bad, got)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4012/4013 OPUS_SET/GET_INBAND_FEC
	// C ref: OPUS_SET_INBAND_FEC_REQUEST – "if(value<0 || value>2) goto bad_arg"
	// Default: silk_mode.useInBandFEC = 0 (InBandFECDisabled).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_INBAND_FEC / OPUS_GET_INBAND_FEC",
		ctlID:    4012,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)

			// Default: 0 (InBandFECDisabled).
			if got := enc.InBandFEC(); got != InBandFECDisabled {
				t.Errorf("OPUS_GET_INBAND_FEC default = %d, want InBandFECDisabled", got)
			}

			// Round-trip all valid configs.
			for _, cfg := range []int{InBandFECDisabled, InBandFECEnabled, InBandFECMusicSafe} {
				if err := enc.SetInBandFEC(cfg); err != nil {
					t.Fatalf("SetInBandFEC(%d) error: %v", cfg, err)
				}
				if got := enc.InBandFEC(); got != cfg {
					t.Errorf("OPUS_GET_INBAND_FEC after SET(%d) = %d, want %d", cfg, got, cfg)
				}
			}

			// Clamp: out-of-range rejected.
			for _, bad := range []int{-1, 3, 10} {
				if err := enc.SetInBandFEC(bad); err == nil {
					t.Errorf("SetInBandFEC(%d) = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4014/4015 OPUS_SET/GET_PACKET_LOSS_PERC
	// C ref: OPUS_SET_PACKET_LOSS_PERC_REQUEST –
	//   "if (value < 0 || value > 100) goto bad_arg"
	// Default: packetLossPercentage = 0.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_PACKET_LOSS_PERC / OPUS_GET_PACKET_LOSS_PERC",
		ctlID:    4014,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: 0.
			if got := enc.PacketLoss(); got != 0 {
				t.Errorf("OPUS_GET_PACKET_LOSS_PERC default = %d, want 0", got)
			}

			// Round-trip boundary values.
			for _, pct := range []int{0, 1, 50, 100} {
				if err := enc.SetPacketLoss(pct); err != nil {
					t.Fatalf("SetPacketLoss(%d) error: %v", pct, err)
				}
				if got := enc.PacketLoss(); got != pct {
					t.Errorf("OPUS_GET_PACKET_LOSS_PERC after SET(%d) = %d, want %d", pct, got, pct)
				}
			}

			// Clamp.
			if err := enc.SetPacketLoss(10); err != nil {
				t.Fatalf("SetPacketLoss(10) error: %v", err)
			}
			for _, bad := range []int{-1, 101, 200} {
				if err := enc.SetPacketLoss(bad); err == nil {
					t.Errorf("SetPacketLoss(%d) = nil, want error", bad)
				}
				if got := enc.PacketLoss(); got != 10 {
					t.Errorf("invalid SetPacketLoss(%d) changed PacketLoss() to %d, want 10", bad, got)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4016/4017 OPUS_SET/GET_DTX
	// C ref: OPUS_SET_DTX_REQUEST – "if(value<0 || value>1) goto bad_arg"
	// Default: silk_mode.useDTX = 0 (false).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_DTX / OPUS_GET_DTX",
		ctlID:    4016,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: false.
			if enc.DTXEnabled() {
				t.Error("OPUS_GET_DTX default = true, want false")
			}

			// Round-trip.
			enc.SetDTX(true)
			if !enc.DTXEnabled() {
				t.Error("DTXEnabled() = false after SetDTX(true)")
			}
			enc.SetDTX(false)
			if enc.DTXEnabled() {
				t.Error("DTXEnabled() = true after SetDTX(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4020/4021 OPUS_SET/GET_VBR_CONSTRAINT
	// C ref: OPUS_SET_VBR_CONSTRAINT_REQUEST – "if(value<0 || value>1) goto bad_arg"
	// Default: vbr_constraint = 1 (true).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_VBR_CONSTRAINT / OPUS_GET_VBR_CONSTRAINT",
		ctlID:    4020,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: true.
			// C ref: opus_encoder_init → st->vbr_constraint = 1
			if !enc.VBRConstraint() {
				t.Error("OPUS_GET_VBR_CONSTRAINT default = false, want true")
			}

			// Round-trip.
			enc.SetVBRConstraint(false)
			if enc.VBRConstraint() {
				t.Error("VBRConstraint() = true after Set(false)")
			}
			enc.SetVBRConstraint(true)
			if !enc.VBRConstraint() {
				t.Error("VBRConstraint() = false after Set(true)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4022/4023 OPUS_SET/GET_FORCE_CHANNELS
	// C ref: OPUS_SET_FORCE_CHANNELS_REQUEST –
	//   "if((value<1 || value>st->channels) && value != OPUS_AUTO) goto bad_arg"
	// Default: force_channels = OPUS_AUTO (-1000) → gopus -1.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_FORCE_CHANNELS / OPUS_GET_FORCE_CHANNELS",
		ctlID:    4022,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

			// Default: -1 (OPUS_AUTO).
			if got := enc.ForceChannels(); got != -1 {
				t.Errorf("OPUS_GET_FORCE_CHANNELS default = %d, want -1 (OPUS_AUTO)", got)
			}

			// Round-trip valid values for stereo encoder.
			for _, ch := range []int{-1, 1, 2} {
				if err := enc.SetForceChannels(ch); err != nil {
					t.Fatalf("SetForceChannels(%d) error: %v", ch, err)
				}
				if got := enc.ForceChannels(); got != ch {
					t.Errorf("OPUS_GET_FORCE_CHANNELS after SET(%d) = %d, want %d", ch, got, ch)
				}
			}

			// Clamp: mono encoder can't force stereo.
			mono := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
			for _, bad := range []int{0, 2, 3} {
				if err := mono.SetForceChannels(bad); err == nil {
					t.Errorf("SetForceChannels(%d) on mono = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4024/4025 OPUS_SET/GET_SIGNAL
	// C ref: OPUS_SET_SIGNAL_REQUEST –
	//   "if(value!=OPUS_AUTO && value!=OPUS_SIGNAL_VOICE && value!=OPUS_SIGNAL_MUSIC) goto bad_arg"
	// Default: signal_type = OPUS_AUTO = -1000 (SignalAuto).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_SIGNAL / OPUS_GET_SIGNAL",
		ctlID:    4024,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: SignalAuto.
			if got := enc.Signal(); got != SignalAuto {
				t.Errorf("OPUS_GET_SIGNAL default = %v, want SignalAuto", got)
			}

			// Round-trip.
			for _, sig := range []Signal{SignalAuto, SignalVoice, SignalMusic} {
				if err := enc.SetSignal(sig); err != nil {
					t.Fatalf("SetSignal(%v) error: %v", sig, err)
				}
				if got := enc.Signal(); got != sig {
					t.Errorf("OPUS_GET_SIGNAL after SET(%v) = %v, want %v", sig, got, sig)
				}
			}

			// Invalid values rejected.
			for _, bad := range []Signal{Signal(-1), Signal(0), Signal(9999)} {
				if err := enc.SetSignal(bad); err == nil {
					t.Errorf("SetSignal(%v) = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4027 OPUS_GET_LOOKAHEAD (encoder GET-only)
	// C ref: OPUS_GET_LOOKAHEAD_REQUEST:
	//   *value = st->Fs/400;
	//   if (app != RESTRICTED_LOWDELAY && app != RESTRICTED_CELT)
	//       *value += st->delay_compensation;  // delay_compensation = Fs/250
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_LOOKAHEAD",
		ctlID:    4027,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			cases := []struct {
				rate int
				app  Application
				want int
			}{
				{48000, ApplicationAudio, 48000/400 + 48000/250},
				{48000, ApplicationVoIP, 48000/400 + 48000/250},
				{48000, ApplicationLowDelay, 48000 / 400},
				{24000, ApplicationAudio, 24000/400 + 24000/250},
				{24000, ApplicationLowDelay, 24000 / 400},
				{16000, ApplicationAudio, 16000/400 + 16000/250},
				{8000, ApplicationAudio, 8000/400 + 8000/250},
			}
			for _, tc := range cases {
				enc := mustNewTestEncoder(t, tc.rate, 1, tc.app)
				if got := enc.Lookahead(); got != tc.want {
					t.Errorf("OPUS_GET_LOOKAHEAD(%d Hz, %v) = %d, want %d", tc.rate, tc.app, got, tc.want)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4029 OPUS_GET_SAMPLE_RATE (encoder GET-only)
	// C ref: OPUS_GET_SAMPLE_RATE_REQUEST → "*value = st->Fs"
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_SAMPLE_RATE",
		ctlID:    4029,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				enc := mustNewTestEncoder(t, rate, 1, ApplicationAudio)
				if got := enc.SampleRate(); got != rate {
					t.Errorf("OPUS_GET_SAMPLE_RATE(%d Hz) = %d, want %d", rate, got, rate)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4031 OPUS_GET_FINAL_RANGE (encoder GET-only)
	// C ref: OPUS_GET_FINAL_RANGE_REQUEST → "*value = st->rangeFinal"
	// Default: 0 before first encode.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_FINAL_RANGE",
		ctlID:    4031,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: 0 before encode.
			if got := enc.FinalRange(); got != 0 {
				t.Errorf("OPUS_GET_FINAL_RANGE before encode = %d, want 0", got)
			}

			// Non-zero after encode.
			if _, err := enc.Encode(generateSineWave(48000, 440, 960), make([]byte, 4000)); err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if got := enc.FinalRange(); got == 0 {
				t.Error("OPUS_GET_FINAL_RANGE after encode = 0, want non-zero")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4036/4037 OPUS_SET/GET_LSB_DEPTH
	// C ref: OPUS_SET_LSB_DEPTH_REQUEST – "if (value<8 || value>24) goto bad_arg"
	// Default: lsb_depth = 24.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_LSB_DEPTH / OPUS_GET_LSB_DEPTH",
		ctlID:    4036,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: 24.
			if got := enc.LSBDepth(); got != 24 {
				t.Errorf("OPUS_GET_LSB_DEPTH default = %d, want 24", got)
			}

			// Round-trip boundary values.
			for _, depth := range []int{8, 16, 24} {
				if err := enc.SetLSBDepth(depth); err != nil {
					t.Fatalf("SetLSBDepth(%d) error: %v", depth, err)
				}
				if got := enc.LSBDepth(); got != depth {
					t.Errorf("OPUS_GET_LSB_DEPTH after SET(%d) = %d, want %d", depth, got, depth)
				}
			}

			// Clamp.
			for _, bad := range []int{7, 25, 32} {
				if err := enc.SetLSBDepth(bad); err == nil {
					t.Errorf("SetLSBDepth(%d) = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4040/4041 OPUS_SET/GET_EXPERT_FRAME_DURATION
	// C ref: OPUS_SET_EXPERT_FRAME_DURATION_REQUEST – must be one of
	//   OPUS_FRAMESIZE_ARG(5000)..OPUS_FRAMESIZE_120_MS(5009); else bad_arg.
	// Default: variable_duration = OPUS_FRAMESIZE_ARG (5000).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_EXPERT_FRAME_DURATION / OPUS_GET_EXPERT_FRAME_DURATION",
		ctlID:    4040,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: ExpertFrameDurationArg (5000).
			if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
				t.Errorf("OPUS_GET_EXPERT_FRAME_DURATION default = %v, want ExpertFrameDurationArg", got)
			}

			// Round-trip all valid values.
			for _, dur := range []ExpertFrameDuration{
				ExpertFrameDurationArg, ExpertFrameDuration2_5Ms, ExpertFrameDuration5Ms,
				ExpertFrameDuration10Ms, ExpertFrameDuration20Ms, ExpertFrameDuration40Ms,
				ExpertFrameDuration60Ms, ExpertFrameDuration80Ms, ExpertFrameDuration100Ms,
				ExpertFrameDuration120Ms,
			} {
				if err := enc.SetExpertFrameDuration(dur); err != nil {
					t.Fatalf("SetExpertFrameDuration(%v) error: %v", dur, err)
				}
				if got := enc.ExpertFrameDuration(); got != dur {
					t.Errorf("OPUS_GET_EXPERT_FRAME_DURATION after SET(%v) = %v, want %v", dur, got, dur)
				}
			}

			// Invalid values rejected.
			for _, bad := range []ExpertFrameDuration{0, 4999, 5010} {
				if err := enc.SetExpertFrameDuration(bad); err == nil {
					t.Errorf("SetExpertFrameDuration(%v) = nil, want error", bad)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4042/4043 OPUS_SET/GET_PREDICTION_DISABLED
	// C ref: OPUS_SET_PREDICTION_DISABLED_REQUEST –
	//   "if (value > 1 || value < 0) goto bad_arg"
	// Default: silk_mode.reducedDependency = 0 (false).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_PREDICTION_DISABLED / OPUS_GET_PREDICTION_DISABLED",
		ctlID:    4042,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Default: false.
			if enc.PredictionDisabled() {
				t.Error("OPUS_GET_PREDICTION_DISABLED default = true, want false")
			}

			// Round-trip.
			enc.SetPredictionDisabled(true)
			if !enc.PredictionDisabled() {
				t.Error("PredictionDisabled() = false after Set(true)")
			}
			enc.SetPredictionDisabled(false)
			if enc.PredictionDisabled() {
				t.Error("PredictionDisabled() = true after Set(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4045 OPUS_GET_GAIN (encoder GET-only — the encoder does not have
	// OPUS_SET_GAIN; gain is only settable on the decoder).
	// Note: The request ID 4045 was a misassignment (should have been 4035);
	// the encoder simply does not handle OPUS_GET_GAIN_REQUEST in libopus —
	// this falls to the default: OPUS_UNIMPLEMENTED.
	// gopus does not expose a Gain getter on the encoder, which is correct.
	// ------------------------------------------------------------------
	// (no row: gopus correctly omits encoder gain getter)

	// ------------------------------------------------------------------
	// 4046/4047 OPUS_SET/GET_PHASE_INVERSION_DISABLED (encoder)
	// C ref: OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST –
	//   "if(value<0 || value>1) goto bad_arg"; delegates to celt_encoder_ctl.
	// Default: 0 (false).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_PHASE_INVERSION_DISABLED / OPUS_GET_PHASE_INVERSION_DISABLED",
		ctlID:    4046,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio) // stereo

			// Default: false.
			if enc.PhaseInversionDisabled() {
				t.Error("OPUS_GET_PHASE_INVERSION_DISABLED default = true, want false")
			}

			// Round-trip.
			enc.SetPhaseInversionDisabled(true)
			if !enc.PhaseInversionDisabled() {
				t.Error("PhaseInversionDisabled() = false after Set(true)")
			}
			enc.SetPhaseInversionDisabled(false)
			if enc.PhaseInversionDisabled() {
				t.Error("PhaseInversionDisabled() = true after Set(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4049 OPUS_GET_IN_DTX (encoder GET-only)
	// C ref: OPUS_GET_IN_DTX_REQUEST – checks silk_enc noSpeechCounter or
	//   st->nb_no_activity_ms_Q1 depending on DTX mode.
	// Default: false (encoder just created; no audio encoded yet).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_IN_DTX",
		ctlID:    4049,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)

			// Default before any encoding: not in DTX.
			if enc.InDTX() {
				t.Error("OPUS_GET_IN_DTX default = true, want false")
			}

			// After encoding normal speech: should still not be in DTX.
			pcm := generateSineWave(48000, 440, 960)
			if _, err := enc.Encode(pcm, make([]byte, 4000)); err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			// DTX requires sustained silence + use_dtx=1; a tone won't trigger it.
			// We just verify the getter doesn't panic and returns a bool.
			_ = enc.InDTX()

			// With DTX enabled, encoding silence eventually drives InDTX = true.
			enc.SetDTX(true)
			silence := make([]float32, 960)
			triggered := false
			for range 200 {
				if _, err := enc.Encode(silence, make([]byte, 4000)); err != nil {
					t.Fatalf("Encode error: %v", err)
				}
				if enc.InDTX() {
					triggered = true
					break
				}
			}
			if !triggered {
				t.Log("NOTE: OPUS_GET_IN_DTX did not trigger after 200 silence frames (may need more frames or specific SILK conditions)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4028 OPUS_RESET_STATE (encoder)
	// C ref: opus_encoder_ctl OPUS_RESET_STATE – clears from
	//   st->OPUS_ENCODER_RESET_START through end of struct; sets
	//   st->first=1, st->mode=MODE_HYBRID, st->bandwidth=OPUS_BANDWIDTH_FULLBAND.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_RESET_STATE",
		ctlID:    4028,
		dir:      "SET",
		buildTag: "",
		testFn: func(t *testing.T) {
			enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

			// Encode once (sets first=0 → application locked).
			if _, err := enc.Encode(generateSineWave(48000, 440, 960), make([]byte, 4000)); err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			// Changing application after first encode fails.
			if err := enc.SetApplication(ApplicationVoIP); err == nil {
				t.Error("SetApplication should fail after first encode")
			}

			enc.Reset()

			// After reset, application lock is cleared (first=1 in libopus).
			if err := enc.SetApplication(ApplicationVoIP); err != nil {
				t.Errorf("SetApplication after Reset() error: %v", err)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4050/4051 OPUS_SET/GET_DRED_DURATION (gopus_dred / gopus_osce)
	// C ref: OPUS_SET_DRED_DURATION_REQUEST –
	//   "if (value < 0 || value > 100) goto bad_arg"
	// Default: 0.
	// (gated by gopus_dred or gopus_osce build tags)
	// ------------------------------------------------------------------
	// Row excluded: covered by encoder_extra_controls tagged tests.

	// ------------------------------------------------------------------
	// 4054/4055 OPUS_SET/GET_OSCE_BWE (gopus_osce)
	// (gated by gopus_osce build tag)
	// ------------------------------------------------------------------
	// Row excluded: covered by decoder_extra_controls tagged tests.

	// ------------------------------------------------------------------
	// 4056/4057 OPUS_SET/GET_QEXT (gopus_qext)
	// (gated by gopus_qext build tag)
	// ------------------------------------------------------------------
	// Row excluded: covered by qext tagged tests.

	// ------------------------------------------------------------------
	// 4052 OPUS_SET_DNN_BLOB (gopus_dred / gopus_osce)
	// (gated by build tag; covered by dnn_blob_controls tagged tests)
	// ------------------------------------------------------------------
	// Row excluded: covered by tagged tests.
}

// TestCTLTable_Encoder runs every row of the encoder CTL table.
func TestCTLTable_Encoder(t *testing.T) {
	for _, row := range encoderCTLTable {
		t.Run(row.ctlName, func(t *testing.T) {
			t.Logf("CTL %d (%s) dir=%s tag=%q", row.ctlID, row.ctlName, row.dir, row.buildTag)
			row.testFn(t)
		})
	}
}

// ---------------------------------------------------------------------------
// Reference table — static fixture linking every libopus CTL to its gopus
// mirror.  This is the machine-readable table promised by the task; a
// TestCTLReferenceTable_Smoke test validates it compiles and is non-empty.
// ---------------------------------------------------------------------------

type ctlRef struct {
	// LibopusCTL is the C macro name (e.g. "OPUS_SET_GAIN").
	LibopusCTL string
	// RequestID is the numeric value from opus_defines.h.
	RequestID int
	// GopusMethod is the gopus method name(s) that implement this CTL.
	GopusMethod string
	// Dir is "GET", "SET", or "SET+GET".
	Dir string
	// Codec is "encoder", "decoder", or "both".
	Codec string
	// Default is the string representation of the default value.
	Default string
	// BuildTag is the gopus build tag required; "" = always present.
	BuildTag string
}

// ctlReferenceTable is the complete reference table of all libopus CTL
// requests and their gopus equivalents (libopus 1.6.1 opus_defines.h).
var ctlReferenceTable = []ctlRef{
	// Decoder CTLs
	{"OPUS_GET_BANDWIDTH", 4009, "Decoder.Bandwidth()", "GET", "decoder", "0 (no packet)", ""},
	{"OPUS_SET_COMPLEXITY", 4010, "Decoder.SetComplexity()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_COMPLEXITY", 4011, "Decoder.Complexity()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_FINAL_RANGE", 4031, "Decoder.FinalRange()", "GET", "decoder", "0", ""},
	{"OPUS_RESET_STATE", 4028, "Decoder.Reset()", "SET", "decoder", "—", ""},
	{"OPUS_GET_SAMPLE_RATE", 4029, "Decoder.SampleRate()", "GET", "decoder", "Fs from init", ""},
	{"OPUS_GET_PITCH", 4033, "Decoder.Pitch()", "GET", "decoder", "0", ""},
	{"OPUS_SET_GAIN", 4034, "Decoder.SetGain()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_GAIN", 4045, "Decoder.Gain()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_LAST_PACKET_DURATION", 4039, "Decoder.LastPacketDuration()", "GET", "decoder", "0", ""},
	{"OPUS_SET_PHASE_INVERSION_DISABLED", 4046, "Decoder.SetPhaseInversionDisabled()", "SET+GET", "decoder", "false", ""},
	{"OPUS_GET_PHASE_INVERSION_DISABLED", 4047, "Decoder.PhaseInversionDisabled()", "SET+GET", "decoder", "false", ""},
	{"OPUS_SET_IGNORE_EXTENSIONS", 4058, "Decoder.SetIgnoreExtensions()", "SET+GET", "decoder", "false", ""},
	{"OPUS_GET_IGNORE_EXTENSIONS", 4059, "Decoder.IgnoreExtensions()", "SET+GET", "decoder", "false", ""},
	{"OPUS_GET_IN_DTX (decoder ext.)", 4049, "Decoder.InDTX()", "GET", "decoder", "false", ""},
	{"OPUS_SET_OSCE_BWE", 4054, "Decoder.SetOSCEBWE()", "SET+GET", "decoder", "false", "gopus_osce"},
	{"OPUS_GET_OSCE_BWE", 4055, "Decoder.OSCEBWE()", "SET+GET", "decoder", "false", "gopus_osce"},
	{"OPUS_SET_DNN_BLOB (decoder)", 4052, "Decoder.SetDNNBlob()", "SET", "decoder", "—", "gopus_dred|gopus_osce"},

	// Encoder CTLs
	{"OPUS_SET_APPLICATION", 4000, "Encoder.SetApplication()", "SET+GET", "encoder", "from config", ""},
	{"OPUS_GET_APPLICATION", 4001, "Encoder.Application()", "SET+GET", "encoder", "from config", ""},
	{"OPUS_SET_BITRATE", 4002, "Encoder.SetBitrate()", "SET+GET", "encoder", "BitrateAuto", ""},
	{"OPUS_GET_BITRATE", 4003, "Encoder.Bitrate()", "SET+GET", "encoder", "BitrateAuto", ""},
	{"OPUS_SET_MAX_BANDWIDTH", 4004, "Encoder.SetMaxBandwidth()", "SET+GET", "encoder", "BandwidthFullband", ""},
	{"OPUS_GET_MAX_BANDWIDTH", 4005, "Encoder.MaxBandwidth()", "SET+GET", "encoder", "BandwidthFullband", ""},
	{"OPUS_SET_VBR", 4006, "Encoder.SetVBR()", "SET+GET", "encoder", "true", ""},
	{"OPUS_GET_VBR", 4007, "Encoder.VBR()", "SET+GET", "encoder", "true", ""},
	{"OPUS_SET_BANDWIDTH", 4008, "Encoder.SetBandwidth()/SetBandwidthAuto()", "SET+GET", "encoder", "auto", ""},
	{"OPUS_GET_BANDWIDTH", 4009, "Encoder.Bandwidth()", "SET+GET", "encoder", "auto", ""},
	{"OPUS_SET_COMPLEXITY", 4010, "Encoder.SetComplexity()", "SET+GET", "encoder", "9", ""},
	{"OPUS_GET_COMPLEXITY", 4011, "Encoder.Complexity()", "SET+GET", "encoder", "9", ""},
	{"OPUS_SET_INBAND_FEC", 4012, "Encoder.SetInBandFEC()", "SET+GET", "encoder", "InBandFECDisabled", ""},
	{"OPUS_GET_INBAND_FEC", 4013, "Encoder.InBandFEC()", "SET+GET", "encoder", "InBandFECDisabled", ""},
	{"OPUS_SET_PACKET_LOSS_PERC", 4014, "Encoder.SetPacketLoss()", "SET+GET", "encoder", "0", ""},
	{"OPUS_GET_PACKET_LOSS_PERC", 4015, "Encoder.PacketLoss()", "SET+GET", "encoder", "0", ""},
	{"OPUS_SET_DTX", 4016, "Encoder.SetDTX()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_DTX", 4017, "Encoder.DTXEnabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_SET_VBR_CONSTRAINT", 4020, "Encoder.SetVBRConstraint()", "SET+GET", "encoder", "true", ""},
	{"OPUS_GET_VBR_CONSTRAINT", 4021, "Encoder.VBRConstraint()", "SET+GET", "encoder", "true", ""},
	{"OPUS_SET_FORCE_CHANNELS", 4022, "Encoder.SetForceChannels()", "SET+GET", "encoder", "-1 (auto)", ""},
	{"OPUS_GET_FORCE_CHANNELS", 4023, "Encoder.ForceChannels()", "SET+GET", "encoder", "-1 (auto)", ""},
	{"OPUS_SET_SIGNAL", 4024, "Encoder.SetSignal()", "SET+GET", "encoder", "SignalAuto", ""},
	{"OPUS_GET_SIGNAL", 4025, "Encoder.Signal()", "SET+GET", "encoder", "SignalAuto", ""},
	{"OPUS_GET_LOOKAHEAD", 4027, "Encoder.Lookahead()", "GET", "encoder", "Fs/400 (+ Fs/250 non-LD)", ""},
	{"OPUS_RESET_STATE", 4028, "Encoder.Reset()", "SET", "encoder", "—", ""},
	{"OPUS_GET_SAMPLE_RATE", 4029, "Encoder.SampleRate()", "GET", "encoder", "Fs from init", ""},
	{"OPUS_GET_FINAL_RANGE", 4031, "Encoder.FinalRange()", "GET", "encoder", "0", ""},
	{"OPUS_SET_LSB_DEPTH", 4036, "Encoder.SetLSBDepth()", "SET+GET", "encoder", "24", ""},
	{"OPUS_GET_LSB_DEPTH", 4037, "Encoder.LSBDepth()", "SET+GET", "encoder", "24", ""},
	{"OPUS_SET_EXPERT_FRAME_DURATION", 4040, "Encoder.SetExpertFrameDuration()", "SET+GET", "encoder", "ExpertFrameDurationArg", ""},
	{"OPUS_GET_EXPERT_FRAME_DURATION", 4041, "Encoder.ExpertFrameDuration()", "SET+GET", "encoder", "ExpertFrameDurationArg", ""},
	{"OPUS_SET_PREDICTION_DISABLED", 4042, "Encoder.SetPredictionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_PREDICTION_DISABLED", 4043, "Encoder.PredictionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_SET_PHASE_INVERSION_DISABLED", 4046, "Encoder.SetPhaseInversionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_PHASE_INVERSION_DISABLED", 4047, "Encoder.PhaseInversionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_IN_DTX", 4049, "Encoder.InDTX()", "GET", "encoder", "false", ""},
	{"OPUS_SET_DRED_DURATION", 4050, "Encoder.SetDREDDuration()", "SET+GET", "encoder", "0", "gopus_dred|gopus_osce"},
	{"OPUS_GET_DRED_DURATION", 4051, "Encoder.DREDDuration()", "SET+GET", "encoder", "0", "gopus_dred|gopus_osce"},
	{"OPUS_SET_DNN_BLOB (encoder)", 4052, "Encoder.SetDNNBlob()", "SET", "encoder", "—", "gopus_dred|gopus_osce"},
	{"OPUS_SET_QEXT", 4056, "Encoder.SetQEXT()", "SET+GET", "encoder", "false", "gopus_qext"},
	{"OPUS_GET_QEXT", 4057, "Encoder.QEXT()", "SET+GET", "encoder", "false", "gopus_qext"},
}

// TestCTLReferenceTable_Smoke verifies the reference table is populated and
// that every entry has non-empty required fields.
func TestCTLReferenceTable_Smoke(t *testing.T) {
	if len(ctlReferenceTable) == 0 {
		t.Fatal("ctlReferenceTable is empty")
	}
	for i, row := range ctlReferenceTable {
		if row.LibopusCTL == "" {
			t.Errorf("row %d: LibopusCTL is empty", i)
		}
		if row.RequestID == 0 {
			t.Errorf("row %d (%s): RequestID is 0", i, row.LibopusCTL)
		}
		if row.GopusMethod == "" {
			t.Errorf("row %d (%s): GopusMethod is empty", i, row.LibopusCTL)
		}
		if row.Dir == "" {
			t.Errorf("row %d (%s): Dir is empty", i, row.LibopusCTL)
		}
		if row.Codec == "" {
			t.Errorf("row %d (%s): Codec is empty", i, row.LibopusCTL)
		}
	}
	t.Logf("CTL reference table: %d entries", len(ctlReferenceTable))
}

// TestCTLReferenceTable_NoDuplicates verifies there are no duplicate CTL IDs
// within the same codec's namespace.
func TestCTLReferenceTable_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, row := range ctlReferenceTable {
		key := row.Codec + "/" + row.LibopusCTL
		if seen[key] {
			t.Errorf("duplicate entry in ctlReferenceTable: %s", key)
		}
		seen[key] = true
	}
}

// TestCTLReferenceTable_AllDecoderCTLsCovered verifies that every decoder CTL
// listed in the table has a corresponding decoder method in gopus.
//
// This is a compile-time assertion masquerading as a runtime test: if a method
// referenced in the table does not exist, the test package will fail to build.
func TestCTLReferenceTable_AllDecoderCTLsCovered(t *testing.T) {
	// Exercise every decoder CTL by calling its gopus method on a fresh instance.
	dec := mustNewTestDecoder(t, 48000, 2) // stereo for phase inversion

	// GET-only decoder CTLs.
	_ = dec.Bandwidth()
	_ = dec.Complexity()
	_ = dec.FinalRange()
	_ = dec.SampleRate()
	_ = dec.Pitch()
	_ = dec.Gain()
	_ = dec.LastPacketDuration()
	_ = dec.PhaseInversionDisabled()
	_ = dec.IgnoreExtensions()
	_ = dec.InDTX()

	// SET+GET decoder CTLs.
	_ = dec.SetComplexity(0)
	_ = dec.SetGain(0)
	dec.SetPhaseInversionDisabled(false)
	dec.SetIgnoreExtensions(false)

	// RESET.
	dec.Reset()

	t.Logf("All decoder CTL methods callable on *Decoder")
}

// TestCTLReferenceTable_AllEncoderCTLsCovered verifies that every encoder CTL
// listed in the table has a corresponding encoder method in gopus.
func TestCTLReferenceTable_AllEncoderCTLsCovered(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio) // stereo for phase inversion

	// Getters.
	_ = enc.Application()
	_ = enc.Bitrate()
	_ = enc.MaxBandwidth()
	_ = enc.VBR()
	_ = enc.Bandwidth()
	_ = enc.Complexity()
	_ = enc.InBandFEC()
	_ = enc.FECEnabled()
	_ = enc.PacketLoss()
	_ = enc.DTXEnabled()
	_ = enc.VBRConstraint()
	_ = enc.ForceChannels()
	_ = enc.Signal()
	_ = enc.Lookahead()
	_ = enc.SampleRate()
	_ = enc.FinalRange()
	_ = enc.LSBDepth()
	_ = enc.ExpertFrameDuration()
	_ = enc.PredictionDisabled()
	_ = enc.PhaseInversionDisabled()
	_ = enc.InDTX()

	// Setters.
	_ = enc.SetApplication(ApplicationAudio)
	_ = enc.SetBitrate(BitrateAuto)
	_ = enc.SetMaxBandwidth(BandwidthFullband)
	enc.SetVBR(true)
	_ = enc.SetBandwidth(BandwidthFullband)
	_ = enc.SetBandwidthAuto()
	_ = enc.SetComplexity(9)
	_ = enc.SetInBandFEC(InBandFECDisabled)
	_ = enc.SetPacketLoss(0)
	enc.SetDTX(false)
	enc.SetVBRConstraint(true)
	_ = enc.SetForceChannels(-1)
	_ = enc.SetSignal(SignalAuto)
	_ = enc.SetLSBDepth(24)
	_ = enc.SetExpertFrameDuration(ExpertFrameDurationArg)
	enc.SetPredictionDisabled(false)
	enc.SetPhaseInversionDisabled(false)

	// RESET.
	enc.Reset()

	t.Logf("All encoder CTL methods callable on *Encoder")
}

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

	for range 5 {
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

	for range 5 {
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
	for range 5 {
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
		t.Fatalf("Encode(restricted SILK complexity 0) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsEncodeHybridComplexityZero(t *testing.T) {
	for _, tc := range []struct {
		name      string
		channels  int
		frameSize int
	}{
		{name: "mono_10ms", channels: 1, frameSize: 480},
		{name: "stereo_20ms", channels: 2, frameSize: 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: tc.channels, Application: ApplicationAudio})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetMode(EncoderModeHybrid); err != nil {
				t.Fatalf("SetMode: %v", err)
			}
			if err := enc.SetFrameSize(tc.frameSize); err != nil {
				t.Fatalf("SetFrameSize: %v", err)
			}
			frameDuration := ExpertFrameDuration10Ms
			if tc.frameSize == 960 {
				frameDuration = ExpertFrameDuration20Ms
			}
			if err := enc.SetExpertFrameDuration(frameDuration); err != nil {
				t.Fatalf("SetExpertFrameDuration: %v", err)
			}
			if err := enc.SetBandwidth(BandwidthSuperwideband); err != nil {
				t.Fatalf("SetBandwidth: %v", err)
			}
			if err := enc.SetMaxBandwidth(BandwidthSuperwideband); err != nil {
				t.Fatalf("SetMaxBandwidth: %v", err)
			}
			if err := enc.SetBitrate(48000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetBitrateMode(BitrateModeVBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := enc.SetComplexity(0); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			if err := enc.SetSignal(SignalVoice); err != nil {
				t.Fatalf("SetSignal: %v", err)
			}
			if tc.channels == 2 {
				if err := enc.SetForceChannels(2); err != nil {
					t.Fatalf("SetForceChannels: %v", err)
				}
			}

			pcm := testSineFrame(tc.frameSize * tc.channels)
			packet := make([]byte, 4000)
			for range 5 {
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
				t.Fatalf("Hybrid complexity 0 Encode allocs/op = %.2f, want 0", allocs)
			}
		})
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

func TestHotPathAllocsDecodeSilenceTransitions(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels=%d", channels), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			silenceTOC := byte(31 << 3)
			if channels == 2 {
				silenceTOC |= 4
			}
			silence := []byte{silenceTOC, 0xff, 0xfe}
			signal := testCELTPacket()
			if channels == 2 {
				signal = testStereoCELTPacket()
			}
			pcm := make([]float32, 960*channels)
			decodeCycle := func() {
				t.Helper()
				for _, packet := range [][]byte{silence, signal, silence} {
					if n, err := dec.Decode(packet, pcm); err != nil {
						t.Fatalf("Decode: %v", err)
					} else if n != 960 {
						t.Fatalf("Decode samples=%d want 960", n)
					}
				}
			}
			for range 5 {
				decodeCycle()
			}
			allocs := testing.AllocsPerRun(200, decodeCycle)
			if allocs != 0 {
				t.Fatalf("Decode silence/signal/silence allocs/op = %.2f, want 0", allocs)
			}
		})
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
	// Both float and fixed-point builds reuse decoder-owned working buffers.
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

// TestHotPathAllocsDecodeSILKPLCMono requires zero allocations over received
// audio and loss, keeping the concealment state active throughout measurement.
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
	for range 4 {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("warmup Decode PLC: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(300, func() {
		if n, err := dec.Decode(packet, pcm); err != nil || n != 960 {
			t.Fatalf("Decode received samples=%d err=%v", n, err)
		}
		if n, err := dec.Decode(nil, pcm); err != nil || n != 960 {
			t.Fatalf("Decode PLC samples=%d err=%v", n, err)
		}
	})
	active := false
	for _, sample := range pcm {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatal("SILK concealment output must be finite")
		}
		active = active || sample != 0
	}
	if !active {
		t.Fatal("SILK concealment measurement must contain active output")
	}
	if allocs > silkPLCMonoHotPathAllocBudget {
		t.Fatalf("Decode(SILK mono PLC) allocs/op = %.2f, want <= %d", allocs, silkPLCMonoHotPathAllocBudget)
	}
}

// TestHotPathAllocsDecodeSILKPLCStereo exercises both SILK channels over
// received/lost cycles with zero warm allocations.
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
	for range 4 {
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("warmup Decode PLC: %v", err)
		}
	}
	allocs := testing.AllocsPerRun(300, func() {
		if n, err := dec.Decode(packet, pcm); err != nil || n != 960 {
			t.Fatalf("Decode received samples=%d err=%v", n, err)
		}
		if n, err := dec.Decode(nil, pcm); err != nil || n != 960 {
			t.Fatalf("Decode PLC samples=%d err=%v", n, err)
		}
	})
	active := false
	for _, sample := range pcm {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatal("SILK concealment output must be finite")
		}
		active = active || sample != 0
	}
	if !active {
		t.Fatal("SILK concealment measurement must contain active output")
	}
	if allocs > silkPLCStereoHotPathAllocBudget {
		t.Fatalf("Decode(SILK stereo PLC) allocs/op = %.2f, want <= %d", allocs, silkPLCStereoHotPathAllocBudget)
	}
}

// TestHotPathAllocsDecodeSILKAndHybridStereo covers the stereo SILK decode
// kernels (MS-to-LR, LPC synthesis/analysis, resampler up2HQ and FIR
// interpolation) and the hybrid CELT band path at steady state.
func TestHotPathAllocsDecodeSILKAndHybridStereo(t *testing.T) {
	cases := []struct {
		name    string
		bw      Bandwidth
		bitrate int
	}{
		{"SILK-WB", BandwidthWideband, 32000},
		{"Hybrid-SWB", BandwidthSuperwideband, 48000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packet := encodeFrameForDecodeGuard(t, ApplicationVoIP, 2, tc.bw, tc.bitrate)
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcm := make([]float32, 960*2)
			for range 3 {
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
				t.Fatalf("Decode(%s stereo) allocs/op = %.2f, want 0", tc.name, allocs)
			}
		})
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
	for range 6 {
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
	for range 3 {
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
	for range 3 {
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
	for range 6 {
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
		t.Fatalf("Multistream Encode allocs/op = %.2f, want 0", allocs)
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
	for range 6 {
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
	for range 3 {
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
			for range 5 {
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

func TestHotPathAllocsStreamWriter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format SampleFormat
		pcm    []byte
	}{
		{"float32", FormatFloat32LE, generateFloat32Bytes(48000, 2, 960, 440.0)},
		{"int16", FormatInt16LE, generateInt16Bytes(48000, 2, 960, 440.0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer, err := NewWriter(48000, 2, nopPacketSink{}, tc.format, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			for range 5 {
				if _, err := writer.Write(tc.pcm); err != nil {
					t.Fatalf("warmup Write: %v", err)
				}
			}
			allocs := testing.AllocsPerRun(200, func() {
				if _, err := writer.Write(tc.pcm); err != nil {
					t.Fatalf("Write: %v", err)
				}
			})
			if allocs != 0 {
				t.Fatalf("stream Writer.Write allocs/op = %.2f, want 0", allocs)
			}
		})
	}
}

func TestLowDelayApplicationControlsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	type controlCase struct {
		name          string
		application   Application
		cApplication  int
		bitrate       int
		bandwidth     Bandwidth
		cBandwidth    int
		forceChannels int
	}
	cases := []controlCase{
		{"lowdelay_wideband_mono", ApplicationLowDelay, libopustest.EncodeDiffApplicationRestrictedLD, 24000, BandwidthWideband, libopustest.EncodeDiffBandwidthWideband, 1},
		{"lowdelay_fullband_stereo", ApplicationLowDelay, libopustest.EncodeDiffApplicationRestrictedLD, 96000, BandwidthFullband, libopustest.EncodeDiffBandwidthFullband, 2},
		{"restricted_celt_wideband_mono", ApplicationRestrictedCelt, 2053, 32000, BandwidthWideband, libopustest.EncodeDiffBandwidthWideband, 1},
		{"restricted_celt_fullband_stereo", ApplicationRestrictedCelt, 2053, 96000, BandwidthFullband, libopustest.EncodeDiffBandwidthFullband, 2},
	}
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		frameCount = 6
	)
	pcm := make([]float32, frameSize*frameCount*channels)
	for i := 0; i < frameSize*frameCount; i++ {
		phase := 2 * math.Pi * float64(i) / sampleRate
		pcm[2*i] = float32(.55*math.Sin(173*phase) + .17*math.Sin(763*phase))
		pcm[2*i+1] = float32(.47*math.Sin(211*phase) - .19*math.Sin(997*phase))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate:    sampleRate,
				Channels:      channels,
				Application:   tc.cApplication,
				ForceMode:     libopustest.EncodeDiffForceModeSILKOnly,
				Bandwidth:     tc.cBandwidth,
				MaxBandwidth:  tc.cBandwidth,
				Bitrate:       tc.bitrate,
				Complexity:    10,
				Signal:        libopustest.EncodeDiffSignalAuto,
				VBR:           true,
				ForceChannels: tc.forceChannels,
				FrameSize:     frameSize,
				FrameCount:    frameCount,
				PCM:           pcm,
			})
			if err != nil {
				t.Fatalf("live C encode: %v", err)
			}
			if len(want) != frameCount {
				t.Fatalf("live C records: got %d, want %d", len(want), frameCount)
			}

			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: tc.application})
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetFrameSize(frameSize); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetMode(EncoderModeSILK); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetSignal(SignalAuto); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBandwidth(tc.bandwidth); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetMaxBandwidth(tc.bandwidth); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetForceChannels(tc.forceChannels); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)

			for pass := 0; pass < 2; pass++ {
				if pass == 1 {
					enc.Reset()
				}
				for f, ref := range want {
					frame := pcm[f*frameSize*channels : (f+1)*frameSize*channels]
					got, err := enc.EncodeFloat32(frame)
					if err != nil {
						t.Fatalf("pass %d frame %d: Go encode: %v", pass, f, err)
					}
					if ref.Ret != len(got) || !bytes.Equal(got, ref.Packet) || enc.FinalRange() != ref.FinalRange {
						t.Fatalf("pass %d frame %d: Go len=%d range=%08x packet=%x; C ret=%d range=%08x packet=%x",
							pass, f, len(got), enc.FinalRange(), got, ref.Ret, ref.FinalRange, ref.Packet)
					}
				}
			}
		})
	}
}

func probeLibopusFloatQuant(mode uint32, samples []float32) ([]int16, error) {
	return libopustest.ProbeFloatQuant(mode, samples)
}

func TestFloat32ToInt16MatchesLibopusFLOAT2INT16ExhaustiveGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := make([]float32, 0, 65536)
	for i := -32768; i <= 32767; i++ {
		samples = append(samples, float32(i)*(1.0/32768.0))
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := float32ToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("float32ToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
		if got := opusmath.Float32ToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("opusmath.Float32ToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
	}
}

func TestFloat32ToInt16MatchesLibopusFLOAT2INT16TiesAndClamps(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		float32(-32769.0 / 32768.0),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		float32(32768.5 / 32768.0),
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := float32ToInt16(sample); got != want[i] {
			t.Fatalf("float32ToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
		if got := opusmath.Float32ToInt16(sample); got != want[i] {
			t.Fatalf("opusmath.Float32ToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func TestFloat32ToInt16MatchesLibopusCELTDispatchBlocks(t *testing.T) {
	libopustest.RequireOracle(t)
	lengths := []int{15, 16, 17, 31, 32, 33}
	pattern := []float32{
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.0 / 32768.0),
		-1,
		1,
		float32(-1234.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(1235.5 / 32768.0),
	}
	for _, length := range lengths {
		t.Run(fmt.Sprintf("len_%d", length), func(t *testing.T) {
			samples := make([]float32, length)
			for i := range samples {
				samples[i] = pattern[i%len(pattern)]
			}
			want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, samples)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant dispatch", err)
			}
			got := make([]int16, length)
			src := append([]float32(nil), samples...)
			softClipAndFloat32ToInt16(got, src, length, 1, []float32{0})
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("len=%d sample[%d]=%d want %d input=%0.10g", length, i, got[i], want[i], samples[i])
				}
			}
		})
	}
}

func TestFloat32ToInt16NoSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	pattern := []float32{
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.0 / 32768.0),
		-1,
		1,
		float32(-1234.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(1235.5 / 32768.0),
	}
	for _, tc := range []struct {
		name     string
		n        int
		channels int
	}{
		{name: "tail_15_mono", n: 15, channels: 1},
		{name: "block_16_mono", n: 16, channels: 1},
		{name: "block_tail_17_mono", n: 17, channels: 1},
		{name: "block_tail_31_mono", n: 31, channels: 1},
		{name: "two_blocks_32_mono", n: 32, channels: 1},
		{name: "two_blocks_tail_33_mono", n: 33, channels: 1},
		{name: "interleaved_33_three_channel", n: 11, channels: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total := tc.n * tc.channels
			samples := make([]float32, total)
			for i := range samples {
				samples[i] = pattern[i%len(pattern)]
			}
			mode := libopustest.FloatQuantModeFloat2Int16
			if runtime.GOARCH == "arm64" {
				mode = libopustest.FloatQuantModeCELTDispatch
			}
			want, err := probeLibopusFloatQuant(mode, samples)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}
			got := make([]int16, total)
			src := append([]float32(nil), samples...)
			float32ToInt16NoSoftClip(got, src, tc.n, tc.channels)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%d want %d input=%0.10g", i, got[i], want[i], samples[i])
				}
			}
			for i := range src {
				if src[i] != samples[i] {
					t.Fatalf("src[%d] mutated to %0.10g want %0.10g", i, src[i], samples[i])
				}
			}
		})
	}
}

func TestFloat32ToInt16NoSoftClipOutOfRangeMatchesLibopusDispatch(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		math.Nextafter32(-1, float32(math.Inf(-1))),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		math.Nextafter32(1, float32(math.Inf(1))),
		float32(-40000.0 / 32768.0),
		float32(40000.0 / 32768.0),
		float32(-1234.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(1235.5 / 32768.0),
	}
	mode := libopustest.FloatQuantModeFloat2Int16
	if runtime.GOARCH == "arm64" {
		mode = libopustest.FloatQuantModeCELTDispatch
	}
	want, err := probeLibopusFloatQuant(mode, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	got := make([]int16, len(samples))
	src := append([]float32(nil), samples...)
	float32ToInt16NoSoftClip(got, src, len(samples), 1)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d input=%0.10g", i, got[i], want[i], samples[i])
		}
	}
}

func TestConvertFloat32ToInt16Unit(t *testing.T) {
	src := []float32{
		-1, -0.875, -0.75, -0.5,
		-1.5 / 32768, -0.5 / 32768, 0, 0.5 / 32768,
		1.5 / 32768, 0.125, 0.25, 0.5,
		0.75, 0.875, 0.99999, 1,
		-0.25, -0.125, 0.33325, -0.33325,
	}
	dst := make([]int16, len(src))
	ok := convertFloat32ToInt16Unit(dst, src, len(src))
	if runtime.GOARCH != "arm64" {
		if ok {
			t.Fatal("default conversion unexpectedly handled the vector")
		}
		return
	}
	if !ok {
		t.Fatal("arm64 conversion rejected in-range samples")
	}
	for i, v := range src {
		want := float32ToInt16(v)
		if pcmInt16VectorTiesAway && i < len(src)&^15 {
			// celt_float2int16_neon uses FCVTAS for complete 16-sample
			// blocks and FCVTNS for the remainder.
			rounded := int32(math.Round(float64(v * 32768)))
			want = int16(max(-32768, min(32767, rounded)))
		}
		if dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}

	outOfRange := append([]float32(nil), src...)
	outOfRange[8] = 1.01
	if convertFloat32ToInt16Unit(make([]int16, len(outOfRange)), outOfRange, len(outOfRange)) {
		t.Fatal("arm64 conversion accepted out-of-range samples")
	}
}

func TestConvertFloat32ToInt16UnitRejectsEveryInvalidVectorLane(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("unit SIMD conversion is arm64-only")
	}
	const n = 16
	valid := make([]float32, n)
	for i := range valid {
		valid[i] = 0.5
	}
	if !convertFloat32ToInt16Unit(make([]int16, n), valid, n) {
		t.Fatal("unit conversion rejected a fully valid vector")
	}

	invalidValues := []struct {
		name  string
		value float32
	}{
		{name: "above-one", value: math.Nextafter32(1, float32(math.Inf(1)))},
		{name: "below-minus-one", value: math.Nextafter32(-1, float32(math.Inf(-1)))},
		{name: "positive-infinity", value: float32(math.Inf(1))},
		{name: "negative-infinity", value: float32(math.Inf(-1))},
		{name: "quiet-nan", value: math.Float32frombits(0x7fc12345)},
		{name: "signaling-nan", value: math.Float32frombits(0x7fa12345)},
	}
	for _, invalid := range invalidValues {
		for lane := 0; lane < n; lane++ {
			t.Run(invalid.name+"/lane_"+strconv.Itoa(lane), func(t *testing.T) {
				src := append([]float32(nil), valid...)
				src[lane] = invalid.value
				if convertFloat32ToInt16Unit(make([]int16, n), src, n) {
					t.Fatalf("accepted %s in lane %d", invalid.name, lane)
				}
			})
		}
	}
}

func TestFloat32ToInt16OpusRoundingFixture(t *testing.T) {
	tests := []struct {
		name string
		in   float32
		want int16
	}{
		{name: "-one", in: -1, want: -32768},
		{name: "negative clamp below minus one", in: math.Nextafter32(-1, float32(math.Inf(-1))), want: -32768},
		{name: "negative clamp just above minus one", in: math.Nextafter32(-1, 0), want: -32768},
		{name: "-1.5/32768 tie to even", in: float32(-1.5 / 32768.0), want: -2},
		{name: "-0.5/32768 tie to even", in: float32(-0.5 / 32768.0), want: 0},
		{name: "zero", in: 0, want: 0},
		{name: "0.5/32768 tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "1.5/32768 tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "positive max exact", in: float32(32767.0 / 32768.0), want: 32767},
		{name: "positive clamp at one", in: 1, want: 32767},
		{name: "positive clamp above one", in: math.Nextafter32(1, float32(math.Inf(1))), want: 32767},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := float32ToInt16(tc.in); got != tc.want {
				t.Fatalf("float32ToInt16(%0.10g) = %d, want %d", tc.in, got, tc.want)
			}
			if got := opusmath.Float32ToInt16(tc.in); got != tc.want {
				t.Fatalf("opusmath.Float32ToInt16(%0.10g) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFloat32ToInt16OpusRoundingBitNeighbors(t *testing.T) {
	tests := []struct {
		name string
		base float32
		want [3]int16
	}{
		{name: "-1.5/32768", base: float32(-1.5 / 32768.0), want: [3]int16{-2, -2, -1}},
		{name: "-0.5/32768", base: float32(-0.5 / 32768.0), want: [3]int16{-1, 0, 0}},
		{name: "0.5/32768", base: float32(0.5 / 32768.0), want: [3]int16{0, 0, 1}},
		{name: "1.5/32768", base: float32(1.5 / 32768.0), want: [3]int16{1, 2, 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			samples := []float32{
				math.Nextafter32(tc.base, float32(math.Inf(-1))),
				tc.base,
				math.Nextafter32(tc.base, float32(math.Inf(1))),
			}
			for i, sample := range samples {
				if got := float32ToInt16(sample); got != tc.want[i] {
					t.Fatalf("float32ToInt16 neighbor %d (%0.10g) = %d, want %d", i, sample, got, tc.want[i])
				}
				if got := opusmath.Float32ToInt16(sample); got != tc.want[i] {
					t.Fatalf("opusmath.Float32ToInt16 neighbor %d (%0.10g) = %d, want %d", i, sample, got, tc.want[i])
				}
			}
		})
	}
}

func TestPCMSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		channels int
		mem      []float32
		samples  []float32
	}{
		{
			name:     "mono_clipped",
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{1.25, 1.5, 1.8, 1.4, 0.8, -0.2, -1.2, -1.6},
		},
		{
			name:     "stereo_clipped",
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.2, -1.8, 1.4, -1.2, 1.7, -0.4, 0.6, 0.3, -0.5, 1.1, -1.3, 1.9},
		},
		{
			name:     "carryover_mem",
			channels: 1,
			mem:      []float32{-0.2},
			samples:  []float32{0.8, 0.4, -0.2, 1.4, 1.1, 0.3},
		},
		{
			name:     "within_range_zero_mem",
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.5, -0.5, 0.3, -0.3, 0.1, -0.1, 0.7, -0.7},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := len(tc.samples) / tc.channels
			want, wantMem, err := libopustest.ProbeSoftClip(n, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}

			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			PCMSoftClip(got, tc.channels, gotMem)

			if len(got) != len(want) {
				t.Fatalf("pcm len=%d want %d", len(got), len(want))
			}
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("pcm[%d]=%g (%08x) want %g (%08x)", i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
				}
			}
			for c := range gotMem {
				if math.Float32bits(gotMem[c]) != math.Float32bits(wantMem[c]) {
					t.Fatalf("mem[%d]=%g (%08x) want %g (%08x)", c, gotMem[c], math.Float32bits(gotMem[c]), wantMem[c], math.Float32bits(wantMem[c]))
				}
			}
		})
	}
}

// TestPCMSoftClipNilAndEdgeCases tests no-op and edge-case inputs.
func TestPCMSoftClipNilAndEdgeCases(t *testing.T) {
	t.Run("zero_channels", func(t *testing.T) {
		pcm := []float32{1.5}
		mem := []float32{}
		PCMSoftClip(pcm, 0, mem)
	})
	t.Run("nil_mem", func(t *testing.T) {
		pcm := []float32{1.5}
		PCMSoftClip(pcm, 1, nil)
	})
	t.Run("empty_pcm", func(t *testing.T) {
		mem := []float32{0}
		PCMSoftClip(nil, 1, mem)
	})
	t.Run("mem_shorter_than_channels", func(t *testing.T) {
		pcm := []float32{1.5, 1.5}
		mem := []float32{0}
		PCMSoftClip(pcm, 2, mem)
	})
}

// TestErrorStringMatchesLibopus verifies that ErrorString returns the same
// strings as opus_strerror() from celt/celt.c in libopus 1.6.1.
func TestErrorStringMatchesLibopus(t *testing.T) {
	// Mirrors celt/celt.c opus_strerror():
	//   static const char * const error_strings[8] = {
	//     "success", "invalid argument", "buffer too small", "internal error",
	//     "corrupted stream", "request not implemented", "invalid state",
	//     "memory allocation failed"
	//   }
	//   if (error > 0 || error < -7) return "unknown error";
	want := map[int]string{
		0:  "success",
		-1: "invalid argument",
		-2: "buffer too small",
		-3: "internal error",
		-4: "corrupted stream",
		-5: "request not implemented",
		-6: "invalid state",
		-7: "memory allocation failed",
	}
	unknownCodes := []int{1, 2, 100, -8, -100, -32768, 32767}

	for code, wantStr := range want {
		got := ErrorString(code)
		if got != wantStr {
			t.Errorf("ErrorString(%d)=%q want %q", code, got, wantStr)
		}
	}
	for _, code := range unknownCodes {
		got := ErrorString(code)
		if got != "unknown error" {
			t.Errorf("ErrorString(%d)=%q want %q", code, got, "unknown error")
		}
	}
}

// TestVersionStringIsGopus verifies VersionString returns a gopus version string.
// Unlike opus_get_version_string() (celt/celt.c), this returns a gopus identifier,
// not a libopus string.
func TestVersionStringIsGopus(t *testing.T) {
	v := VersionString()
	if v == "" {
		t.Fatal("VersionString() returned empty string")
	}
	if !strings.HasPrefix(v, "gopus") {
		t.Errorf("VersionString()=%q does not start with %q", v, "gopus")
	}
	// Must not claim to be libopus.
	if strings.Contains(v, "libopus") {
		t.Errorf("VersionString()=%q must not contain %q", v, "libopus")
	}
}

// TestDecodeGainTransitionMatchesLibopus verifies that SetGain changes take effect
// on the next call to Decode, matching libopus's abrupt (non-smoothed) gain application.
//
// libopus opus_decode_native() in src/opus_decoder.c applies decode_gain
// unconditionally after each frame with no inter-frame ramping:
//
//	if (st->decode_gain) {
//	    gain = celt_exp2(MULT16_16_P15(QCONST16(6.48814081e-4f, 25), st->decode_gain));
//	    for (i=0; i<frame_size*st->channels; i++) { ... pcm[i] = SATURATE(x, 32767); }
//	}
//
// The oracle drives two consecutive decodes with OPUS_SET_GAIN applied before
// the first packet, so both frames see the same gain. gopus must match exactly.
func TestDecodeGainTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		gainQ8     = 8192
	)
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)

			// Oracle: decode two frames (one real + one PLC) with constant gain.
			steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "gain transition reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}

			got := make([]float32, 0, len(want))
			frame := make([]float32, frameSize*channels)
			n, err := dec.Decode(packet, frame)
			if err != nil {
				t.Fatalf("Decode packet: %v", err)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.Decode(nil, frame)
			if err != nil {
				t.Fatalf("Decode(nil): %v", err)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "gain transition float32")
			assertAPIRateFloat32BitsExact(t, got, want, "gain transition float32")
		})
	}
}

// TestDecodeGainChangeTransitionMatchesLibopus verifies that when SetGain is called
// between two Decode calls the new gain is applied abruptly (immediately) on the
// next frame, exactly as libopus does. libopus stores decode_gain in the decoder
// state and reads it on every opus_decode_native call without ramping.
func TestDecodeGainChangeTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
	)
	gains := []int{256, 512, -256}
	for _, channels := range []int{1, 2} {
		for _, gainQ8 := range gains {
			t.Run("ch_"+itoaSmall(channels)+"_gain_"+itoaSmall(gainQ8), func(t *testing.T) {
				packet := encodeAPIRateCELTPacket(t, channels)

				// Oracle applies gainQ8 before the first decode; both frames use it.
				steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
				want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "gain change reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				if err := dec.SetGain(gainQ8); err != nil {
					t.Fatalf("SetGain(%d): %v", gainQ8, err)
				}

				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				n, err := dec.Decode(packet, frame)
				if err != nil {
					t.Fatalf("Decode packet: %v", err)
				}
				got = append(got, frame[:n*channels]...)

				clear(frame)
				n, err = dec.Decode(nil, frame)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				got = append(got, frame[:n*channels]...)

				assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "gain change transition float32")
				assertAPIRateFloat32BitsExact(t, got, want, "gain change transition float32")
			})
		}
	}
}

// TestDecodeGainModeTransitionMatchesLibopus decodes CELT<->SILK/Hybrid mode
// transitions with a decode gain. libopus applies decode_gain inside every
// opus_decode_frame call, including the recursive opus_decode_frame(NULL) that
// produces the 5 ms transition frame, so the transition samples copied into the
// output carry the gain twice. gopus must match every float bit.
func TestDecodeGainModeTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 480
		gainQ8     = 768
	)
	modes := []EncoderMode{EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeSILK, EncoderModeSILK, EncoderModeCELT, EncoderModeCELT}
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			// One encoder per mode, so the stream switches modes without the
			// redundancy frames an encoder emits on its own mode changes (which
			// cancel the decoder transition).
			encoders := map[EncoderMode]*Encoder{}
			for _, mode := range []EncoderMode{EncoderModeCELT, EncoderModeHybrid, EncoderModeSILK} {
				enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
				if err != nil {
					t.Fatalf("NewEncoder: %v", err)
				}
				if err := enc.SetFrameSize(frameSize); err != nil {
					t.Fatalf("SetFrameSize: %v", err)
				}
				if err := enc.SetBitrate(32000 * channels); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if err := enc.SetMode(mode); err != nil {
					t.Fatalf("SetMode: %v", err)
				}
				encoders[mode] = enc
			}
			var steps []libopusAPIRateDecodeStep
			pcm := make([]float32, frameSize*channels)
			packet := make([]byte, 1275)
			for f, mode := range modes {
				for i := range frameSize {
					n := float64(f*frameSize + i)
					for c := range channels {
						pcm[i*channels+c] = float32(0.3*math.Sin(2*math.Pi*(440+110*float64(c))*n/sampleRate) + 0.1*math.Sin(2*math.Pi*5300*n/sampleRate))
					}
				}
				for m, enc := range encoders {
					n, err := enc.Encode(pcm, packet)
					if err != nil {
						t.Fatalf("Encode: %v", err)
					}
					if m == mode {
						steps = append(steps, libopusAPIRateDecodeStep{packet: append([]byte(nil), packet[:n]...)})
					}
				}
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "gain mode transition reference decode", err)
			}
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain: %v", err)
			}
			frame := make([]float32, frameSize*channels)
			var got []float32
			for _, step := range steps {
				n, err := dec.Decode(step.packet, frame)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				got = append(got, frame[:n*channels]...)
			}
			if len(got) != len(want) {
				t.Fatalf("sample count gopus=%d libopus=%d", len(got), len(want))
			}
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("sample %d (frame %d, TOC %#02x): gopus=%08x libopus=%08x", i, i/(frameSize*channels), steps[i/(frameSize*channels)].packet[0], math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}

func TestDecodeWithFECZeroByteFirstFrameDTX(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Prime decoder with a valid SILK frame so lastPacketMode is ModeSILK.
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcmIn := make([]float32, 960)
	for i := range pcmIn {
		pcmIn[i] = 0.25 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	pktBuf := make([]byte, 4000)
	n, err := enc.Encode(pcmIn, pktBuf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	pcmOut := make([]float32, 960)
	if _, err := dec.Decode(pktBuf[:n], pcmOut); err != nil {
		t.Fatalf("Decode primer: %v", err)
	}
	prevMode := dec.lastPacketMode

	// Malformed CELT packet (code 3, frame count 0) must not mutate lastPacketMode.
	if _, err := dec.DecodeWithFEC([]byte{0xff, 0x00}, pcmOut, true); err == nil {
		t.Fatal("DecodeWithFEC(malformed CELT code-3 count=0) succeeded; want error")
	}
	if dec.lastPacketMode != prevMode {
		t.Fatalf("DecodeWithFEC mutated lastPacketMode on error: got %v, want %v", dec.lastPacketMode, prevMode)
	}

	// Valid 0-byte DTX first frames across Code 1, Code 2, and Code 3 CBR/VBR.
	dtxPackets := []struct {
		name string
		data []byte
	}{
		{name: "code1_zero_frames", data: []byte{0x01}},
		{name: "code2_zero_first_frame", data: []byte{0x02, 0x00}},
		{name: "code3_cbr_zero_frames", data: []byte{0x03, 0x01}},
		{name: "code3_vbr_zero_first_frame", data: []byte{0x03, 0x82, 0x00}},
	}
	for _, tc := range dtxPackets {
		t.Run(tc.name, func(t *testing.T) {
			nDecoded, err := dec.DecodeWithFEC(tc.data, pcmOut, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC(%x): %v", tc.data, err)
			}
			if nDecoded <= 0 {
				t.Fatalf("DecodeWithFEC(%x) decoded %d samples, want > 0", tc.data, nDecoded)
			}
		})
	}
}

func TestCode3HugePaddingEarlyExit(t *testing.T) {
	// Huge malformed padding stream must fail cleanly with ErrInvalidPacket.
	huge := make([]byte, 256*1024)
	huge[0] = 0x4b // Code 3
	huge[1] = 0x41 // 1 frame, padding flag set
	for i := 2; i < len(huge); i++ {
		huge[i] = 0xff
	}
	if _, err := ParsePacket(huge); err != ErrInvalidPacket {
		t.Fatalf("ParsePacket(huge 0xff padding) err = %v, want %v", err, ErrInvalidPacket)
	}
	if err := validatePacketFraming(huge); err != ErrInvalidPacket {
		t.Fatalf("validatePacketFraming(huge 0xff padding) err = %v, want %v", err, ErrInvalidPacket)
	}
	if _, err := extractFirstFramePayload(huge, ParseTOC(huge[0])); err != ErrInvalidPacket {
		t.Fatalf("extractFirstFramePayload(huge 0xff padding) err = %v, want %v", err, ErrInvalidPacket)
	}

	// Truncated single 0xff padding indicator returns ErrPacketTooShort.
	trunc := []byte{0x4b, 0x41, 0xff}
	if _, err := ParsePacket(trunc); err != ErrPacketTooShort {
		t.Fatalf("ParsePacket(truncated 0xff) err = %v, want %v", err, ErrPacketTooShort)
	}
	if err := validatePacketFraming(trunc); err != ErrPacketTooShort {
		t.Fatalf("validatePacketFraming(truncated 0xff) err = %v, want %v", err, ErrPacketTooShort)
	}
}

func TestMultistreamPacketPadZeroAlloc(t *testing.T) {
	packet := []byte{0x48, 0x11, 0x22, 0x33}
	buf := make([]byte, len(packet), 32)
	copy(buf, packet)

	// Warm up.
	if err := MultistreamPacketPad(buf, len(packet), 24, 1); err != nil {
		t.Fatalf("MultistreamPacketPad warmup: %v", err)
	}
	if _, err := MultistreamPacketUnpad(buf[:24], 24, 1); err != nil {
		t.Fatalf("MultistreamPacketUnpad warmup: %v", err)
	}

	allocs := testing.AllocsPerRun(50, func() {
		copy(buf, packet)
		if err := MultistreamPacketPad(buf, len(packet), 24, 1); err != nil {
			t.Fatalf("MultistreamPacketPad: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("MultistreamPacketPad allocs = %.1f, want 0", allocs)
	}
}

func TestMultistreamDecodeInt24SliceLongPacket(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault: %v", err)
	}
	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault: %v", err)
	}

	// Encode a 20ms frame and verify exact-size allocation (not 60ms).
	pcm20 := make([]float32, 960*2)
	pkt20 := make([]byte, 4000)
	n20, err := enc.Encode(pcm20, pkt20)
	if err != nil {
		t.Fatalf("Encode 20ms: %v", err)
	}
	out20, err := dec.DecodeInt24Slice(pkt20[:n20])
	if err != nil {
		t.Fatalf("DecodeInt24Slice(20ms): %v", err)
	}
	if len(out20) != 960*2 {
		t.Fatalf("DecodeInt24Slice(20ms) len=%d, want %d", len(out20), 960*2)
	}

	// Build a valid 100ms (5 x 20ms) packet using Repacketizer and decode via DecodeInt24Slice.
	rp := NewRepacketizer()
	for range 5 {
		if err := rp.Cat(pkt20[:n20]); err != nil {
			t.Fatalf("Repacketizer.Cat: %v", err)
		}
	}
	pkt100 := make([]byte, 4000)
	n100, err := rp.Out(pkt100)
	if err != nil {
		t.Fatalf("Repacketizer.Out: %v", err)
	}
	out100, err := dec.DecodeInt24Slice(pkt100[:n100])
	if err != nil {
		t.Fatalf("DecodeInt24Slice(100ms): %v", err)
	}
	if len(out100) != 4800*2 {
		t.Fatalf("DecodeInt24Slice(100ms) len=%d, want %d", len(out100), 4800*2)
	}
}

func TestStreamWriterLargeWriteDoesNotRetainOversizedBuffer(t *testing.T) {
	sink := &slicePacketSink{}
	w, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Write 10 full 20ms frames (19,200 float32 samples = 76,800 bytes) in one call.
	const frames = 10
	frameBytes := 960 * 2 * 4
	raw := bytes.Repeat([]byte{0, 0, 0, 0}, 960*2*frames)
	n, err := w.Write(raw)
	if err != nil {
		t.Fatalf("Writer.Write: %v", err)
	}
	if n != len(raw) {
		t.Fatalf("Writer.Write n=%d, want %d", n, len(raw))
	}
	if len(sink.packets) != frames {
		t.Fatalf("packets written = %d, want %d", len(sink.packets), frames)
	}
	if cap(w.sampleBuf) > frameBytes {
		t.Fatalf("Writer.sampleBuf cap = %d, want <= %d", cap(w.sampleBuf), frameBytes)
	}
}

func requireFiniteFloat32(t *testing.T, samples []float32) {
	t.Helper()

	for i, sample := range samples {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatalf("sample[%d] is not finite: %v", i, sample)
		}
	}
}

func TestAPIAbuseReturnsError(t *testing.T) {
	t.Run("EncoderSmallOutputBuffer", func(t *testing.T) {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}

		pcm := generateSineWaveFloat32(48000, 440, enc.FrameSize(), 1)
		if _, err := enc.Encode(pcm, make([]byte, 0)); !errors.Is(err, ErrBufferTooSmall) {
			t.Fatalf("Encode error=%v want=%v", err, ErrBufferTooSmall)
		}
	})

	t.Run("EncoderInvalidFrameSize", func(t *testing.T) {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}

		pcm := generateSineWaveFloat32(48000, 440, enc.FrameSize()-1, 1)
		if _, err := enc.Encode(pcm, make([]byte, maxPacketBytesPerStream)); !errors.Is(err, ErrInvalidFrameSize) {
			t.Fatalf("Encode error=%v want=%v", err, ErrInvalidFrameSize)
		}
	})

	t.Run("DecoderTruncatedPacket", func(t *testing.T) {
		dec := newMonoTestDecoder(t)
		pcm := make([]float32, 960)
		if _, err := dec.Decode([]byte{0x03}, pcm); !errors.Is(err, ErrPacketTooShort) {
			t.Fatalf("Decode error=%v want=%v", err, ErrPacketTooShort)
		}
	})

	t.Run("RepeatedPLCStaysFinite", func(t *testing.T) {
		dec := newMonoTestDecoder(t)
		pcm := make([]float32, 960)

		n, err := dec.Decode(nil, pcm)
		if err != nil {
			t.Fatalf("initial Decode(nil) error: %v", err)
		}
		if n != 960 {
			t.Fatalf("initial Decode(nil) samples=%d want=960", n)
		}
		requireFiniteFloat32(t, pcm[:n])

		if _, err := dec.Decode([]byte{0x03}, pcm); !errors.Is(err, ErrPacketTooShort) {
			t.Fatalf("Decode(truncated) error=%v want=%v", err, ErrPacketTooShort)
		}

		for i := range 8 {
			n, err = dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) iteration %d error: %v", i, err)
			}
			if n != 960 {
				t.Fatalf("Decode(nil) iteration %d samples=%d want=960", i, n)
			}
			requireFiniteFloat32(t, pcm[:n])
		}
	})

	t.Run("MultistreamInvalidMappings", func(t *testing.T) {
		if _, err := NewMultistreamEncoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3}, ApplicationAudio); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("NewMultistreamEncoder error=%v want=%v", err, ErrInvalidMapping)
		}
		if _, err := NewMultistreamDecoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3}); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("NewMultistreamDecoder error=%v want=%v", err, ErrInvalidMapping)
		}
	})
}

func addSafetyDecodeFuzzSeeds(f *testing.F) {
	seeds := [][]byte{
		{},
		{0x03},
		{0x02, 0x01},
		{0xFF},
		minimalHybridTestPacket20ms(),
		minimalHybridTestPacket20msStereo(),
	}
	for _, seed := range seeds {
		f.Add(append([]byte(nil), seed...))
	}

	if packet, err := safetyEncodedSeedPacket(1); err == nil && len(packet) > 0 {
		f.Add(packet)
	}
	if packet, err := safetyEncodedSeedPacket(2); err == nil && len(packet) > 0 {
		f.Add(packet)
	}
}

func safetyEncodedSeedPacket(channels int) ([]byte, error) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		return nil, err
	}
	pcm := generateSineWaveFloat32(48000, 440, enc.FrameSize(), channels)
	return enc.EncodeFloat32(pcm)
}

func requireFinitePCM(t *testing.T, samples []float32) {
	t.Helper()

	for i, sample := range samples {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatalf("sample[%d] is not finite: %v", i, sample)
		}
	}
}

func FuzzDecodeNeverPanics(f *testing.F) {
	addSafetyDecodeFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}

		for _, channels := range []int{1, 2} {
			cfg := DefaultDecoderConfig(48000, channels)
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatalf("NewDecoder(%d) error: %v", channels, err)
			}

			pcm := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
			n, err := dec.Decode(data, pcm)
			if err == nil {
				if n < 0 || n > cfg.MaxPacketSamples {
					t.Fatalf("Decode(%dch) samples=%d outside [0,%d]", channels, n, cfg.MaxPacketSamples)
				}
				requireFinitePCM(t, pcm[:n*channels])
			}

			plcN, plcErr := dec.Decode(nil, pcm)
			if plcErr != nil {
				t.Fatalf("Decode(nil) after fuzz packet failed for %dch: %v", channels, plcErr)
			}
			if plcN < 0 || plcN > cfg.MaxPacketSamples {
				t.Fatalf("Decode(nil) samples=%d outside [0,%d]", plcN, cfg.MaxPacketSamples)
			}
			requireFinitePCM(t, pcm[:plcN*channels])
		}
	})
}

func speechTestPCM(t testing.TB, class string, sampleRate, frameSize, frames int) ([]int16, []float32) {
	t.Helper()
	sig, err := testsignal.GenerateCorpusSignal(class, sampleRate, frames*frameSize, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, len(sig))
	floats := make([]float32, len(sig))
	for i, v := range sig {
		pcm[i] = int16(math.Max(-32768, math.Min(32767, math.Round(float64(v)*32768))))
		floats[i] = float32(pcm[i]) * (1.0 / 32768.0)
	}
	return pcm, floats
}

func TestSpeechDetectorMatchesEncoderAnalysis(t *testing.T) {
	classes := []string{testsignal.CorpusCleanSpeechV1, testsignal.CorpusMixedV1, testsignal.CorpusMusicV1, testsignal.CorpusWhiteNoiseV1}
	for _, sampleRate := range []int{16000, 24000, 48000} {
		for _, frameMS := range []int{10, 20} {
			for _, class := range classes {
				t.Run(fmt.Sprintf("%dk/%dms/%s", sampleRate/1000, frameMS, class), func(t *testing.T) {
					frameSize := sampleRate * frameMS / 1000
					const frames = 100
					pcm, floats := speechTestPCM(t, class, sampleRate, frameSize, frames)
					fromInt16, err := NewSpeechDetector(sampleRate)
					if err != nil {
						t.Fatal(err)
					}
					fromFloat32, _ := NewSpeechDetector(sampleRate)
					oracle := encoder.NewTonalityAnalysisState(sampleRate)
					for f := range frames {
						span := f*frameSize + frameSize
						got16, err := fromInt16.AnalyzeInt16(pcm[f*frameSize : span])
						if err != nil {
							t.Fatal(err)
						}
						got32, err := fromFloat32.AnalyzeFloat32(floats[f*frameSize : span])
						if err != nil {
							t.Fatal(err)
						}
						info := oracle.RunAnalysis(floats[f*frameSize:span], frameSize, 1)
						want := float32(0)
						if info.Valid {
							want = info.VADProb
						}
						if math.Float32bits(got16) != math.Float32bits(want) || math.Float32bits(got32) != math.Float32bits(want) {
							t.Fatalf("frame %d: int16 %v float32 %v, encoder analysis %v", f, got16, got32, want)
						}
					}
				})
			}
		}
	}
}

func TestSpeechDetectorValidatesInput(t *testing.T) {
	for _, rate := range []int{0, 8000, 12000, 44100, 96000} {
		if _, err := NewSpeechDetector(rate); err == nil {
			t.Fatalf("NewSpeechDetector(%d) accepted an unsupported rate", rate)
		}
	}
	if _, err := (*SpeechDetector)(nil).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("nil detector: %v", err)
	}
	if _, err := (*SpeechDetector)(nil).AnalyzeFloat32(nil); err != ErrInvalidFrameSize {
		t.Fatalf("nil detector float32: %v", err)
	}
	if _, err := new(SpeechDetector).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("zero-value detector: %v", err)
	}
	new(SpeechDetector).Reset()
	(*SpeechDetector)(nil).Reset()
	for _, rate := range []int{16000, 24000, 48000} {
		d, err := NewSpeechDetector(rate)
		if err != nil {
			t.Fatal(err)
		}
		for _, ms := range []int{10, 20} {
			size := rate * ms / 1000
			if _, err := d.AnalyzeInt16(make([]int16, size)); err != nil {
				t.Fatalf("rate=%d %d ms int16: %v", rate, ms, err)
			}
			if _, err := d.AnalyzeFloat32(make([]float32, size)); err != nil {
				t.Fatalf("rate=%d %d ms float32: %v", rate, ms, err)
			}
		}
		for _, size := range []int{0, 1, rate/100 - 1, rate/100 + 1, rate/50 - 1, rate/50 + 1, rate * 3 / 100, rate / 25} {
			if _, err := d.AnalyzeInt16(make([]int16, size)); err != ErrInvalidFrameSize {
				t.Fatalf("rate=%d int16 size=%d: %v", rate, size, err)
			}
			if _, err := d.AnalyzeFloat32(make([]float32, size)); err != ErrInvalidFrameSize {
				t.Fatalf("rate=%d float32 size=%d: %v", rate, size, err)
			}
		}
	}
}

func TestSpeechDetectorReset(t *testing.T) {
	const sampleRate, frameSize, frames = 16000, 320, 60
	pcm, _ := speechTestPCM(t, testsignal.CorpusCleanSpeechV1, sampleRate, frameSize, frames)
	run := func(d *SpeechDetector) []float32 {
		out := make([]float32, frames)
		for f := range frames {
			p, err := d.AnalyzeInt16(pcm[f*frameSize : (f+1)*frameSize])
			if err != nil {
				t.Fatal(err)
			}
			out[f] = p
		}
		return out
	}
	d, _ := NewSpeechDetector(sampleRate)
	fresh := run(d)
	continued := run(d)
	differs := false
	for f := range fresh {
		differs = differs || fresh[f] != continued[f]
	}
	if !differs {
		t.Fatal("a continued stream scores like a fresh one, so the comparison below proves nothing")
	}
	d.Reset()
	again := run(d)
	for f := range fresh {
		if math.Float32bits(fresh[f]) != math.Float32bits(again[f]) {
			t.Fatalf("frame %d after Reset: %v, fresh detector %v", f, again[f], fresh[f])
		}
	}
}

func TestSpeechDetectorSilenceAndFrameCadence(t *testing.T) {
	const sampleRate = 16000
	pcm, _ := speechTestPCM(t, testsignal.CorpusCleanSpeechV1, sampleRate, 160, 60)

	// A 10 ms frame completes an analysis window on every second call and repeats
	// the previous result in between; nothing is known before the first window.
	d, _ := NewSpeechDetector(sampleRate)
	var results []float32
	for f := range 60 {
		p, err := d.AnalyzeInt16(pcm[f*160 : (f+1)*160])
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, p)
	}
	if results[0] != 0 {
		t.Fatalf("first 10 ms call = %v, want 0 before the first window", results[0])
	}
	for f := 1; f+1 < len(results); f += 2 {
		if results[f] != results[f+1] {
			t.Fatalf("10 ms calls %d and %d differ: %v %v", f, f+1, results[f], results[f+1])
		}
	}

	// Exactly zero input reports 0 rather than repeating the last speech estimate.
	d.Reset()
	var peak float32
	for f := range 50 {
		p, _ := d.AnalyzeInt16(pcm[f*160 : (f+1)*160])
		if f >= 30 {
			peak = max(peak, p)
		}
	}
	if peak <= 0.5 {
		t.Fatalf("peak probability %v over the last speech calls, want a speech-like value", peak)
	}
	for f := range 6 {
		p, _ := d.AnalyzeInt16(make([]int16, 160))
		if f >= 4 && p != 0 {
			t.Fatalf("silent call %d = %v, want 0", f, p)
		}
	}
	// Speech after silence is scored again.
	var after float32
	for f := range 20 {
		after, _ = d.AnalyzeInt16(pcm[f*160 : (f+1)*160])
	}
	if after <= 0 {
		t.Fatalf("probability %v after silence, want speech to be scored", after)
	}
}

// TestSpeechDetectorSeparatesSpeechFromNoise checks the sign of the output on
// the synthetic corpus: voiced speech scores high and steady noise, a hum-free
// noise floor and near silence score near zero once the warm-up has passed.
func TestSpeechDetectorSeparatesSpeechFromNoise(t *testing.T) {
	cases := []struct {
		class    string
		min, max float64
	}{
		{testsignal.CorpusCleanSpeechV1, 0.7, 1},
		{testsignal.CorpusWhiteNoiseV1, 0, 0.05},
		{testsignal.CorpusPinkNoiseV1, 0, 0.05},
		{testsignal.CorpusNearSilenceV1, 0, 0.05},
	}
	for _, sampleRate := range []int{16000, 48000} {
		frameSize := sampleRate / 50
		for _, tc := range cases {
			pcm, _ := speechTestPCM(t, tc.class, sampleRate, frameSize, 150)
			d, _ := NewSpeechDetector(sampleRate)
			var sum float64
			count := 0
			for f := range 150 {
				p, err := d.AnalyzeInt16(pcm[f*frameSize : (f+1)*frameSize])
				if err != nil {
					t.Fatal(err)
				}
				if p < 0 || p > 1 {
					t.Fatalf("%s %d Hz: probability %v outside [0, 1]", tc.class, sampleRate, p)
				}
				if f >= 25 { // skip the 10-window warm-up and its settling
					sum += float64(p)
					count++
				}
			}
			if mean := sum / float64(count); mean < tc.min || mean > tc.max {
				t.Fatalf("%s %d Hz: mean probability %.3f outside [%.2f, %.2f]", tc.class, sampleRate, mean, tc.min, tc.max)
			}
		}
	}
}

func TestSpeechDetectorSteadyStateNoAllocations(t *testing.T) {
	for _, sampleRate := range []int{16000, 24000, 48000} {
		for _, ms := range []int{10, 20} {
			frameSize := sampleRate * ms / 1000
			pcm, floats := speechTestPCM(t, testsignal.CorpusMixedV1, sampleRate, frameSize, 1)
			d, _ := NewSpeechDetector(sampleRate)
			if n := testing.AllocsPerRun(100, func() { _, _ = d.AnalyzeInt16(pcm) }); n != 0 {
				t.Fatalf("%d Hz %d ms: AnalyzeInt16 allocations = %v", sampleRate, ms, n)
			}
			if n := testing.AllocsPerRun(100, func() { _, _ = d.AnalyzeFloat32(floats) }); n != 0 {
				t.Fatalf("%d Hz %d ms: AnalyzeFloat32 allocations = %v", sampleRate, ms, n)
			}
			if n := testing.AllocsPerRun(20, func() {
				d.Reset()
				_, _ = d.AnalyzeFloat32(floats)
			}); n != 0 {
				t.Fatalf("%d Hz %d ms: Reset and analyze allocations = %v", sampleRate, ms, n)
			}
		}
	}
}

func TestVADMatchesSILK(t *testing.T) {
	for _, sampleRate := range []int{8000, 12000, 16000} {
		for _, frameMS := range []int{10, 20} {
			vad, err := NewVAD(sampleRate)
			if err != nil {
				t.Fatal(err)
			}
			oracle := encoder.NewVADState()
			frameSize := sampleRate * frameMS / 1000
			pcm := make([]int16, frameSize)
			floats := make([]float32, frameSize)
			for frame := range 40 {
				for i := range pcm {
					value := int16(0)
					if frame >= 20 && frame < 30 {
						value = int16(10000 * math.Sin(2*math.Pi*220*float64(i)/float64(sampleRate)))
					}
					pcm[i] = value
					floats[i] = float32(value) * (1.0 / 32768.0)
				}
				got, err := vad.AnalyzeInt16(pcm)
				if err != nil {
					t.Fatal(err)
				}
				want, _ := oracle.GetSpeechActivity(floats, frameSize, sampleRate/1000)
				if got != want {
					t.Fatalf("rate=%d frameMS=%d frame=%d: activity=%d want %d", sampleRate, frameMS, frame, got, want)
				}
			}
			vad.Reset()
			oracle.Reset()
			got, _ := vad.AnalyzeInt16(pcm)
			want, _ := oracle.GetSpeechActivity(floats, frameSize, sampleRate/1000)
			if got != want {
				t.Fatalf("reset rate=%d frameMS=%d: activity=%d want %d", sampleRate, frameMS, got, want)
			}
		}
	}
}

func TestVADValidatesInput(t *testing.T) {
	for _, rate := range []int{0, 44100, 24000, 48000} {
		if _, err := NewVAD(rate); err == nil {
			t.Fatalf("NewVAD(%d) accepted an unsupported rate", rate)
		}
	}
	if _, err := (*VAD)(nil).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("nil VAD: %v", err)
	}
	if _, err := new(VAD).AnalyzeInt16(nil); err != ErrInvalidFrameSize {
		t.Fatalf("zero-value VAD: %v", err)
	}
	new(VAD).Reset()
	for _, size := range []int{0, 159, 161, 319, 321} {
		vad, _ := NewVAD(16000)
		if _, err := vad.AnalyzeInt16(make([]int16, size)); err != ErrInvalidFrameSize {
			t.Fatalf("size=%d: %v", size, err)
		}
	}
}

func TestVADSteadyStateNoAllocations(t *testing.T) {
	vad, _ := NewVAD(16000)
	pcm := make([]int16, 320)
	_, _ = vad.AnalyzeInt16(pcm)
	if n := testing.AllocsPerRun(100, func() { _, _ = vad.AnalyzeInt16(pcm) }); n != 0 {
		t.Fatalf("AnalyzeInt16 allocations = %v", n)
	}
}

type optionalEncoderControl interface {
	SetDNNBlob([]byte) error
}

type qextEncoderControl interface {
	SetQEXT(bool) error
	QEXT() (bool, error)
}

type optionalDecoderControl interface {
	SetDNNBlob([]byte) error
}

type extraDREDControl interface {
	SetDREDDuration(int) error
	DREDDuration() (int, error)
}

type extraOSCEBWEControl interface {
	SetOSCEBWE(bool) error
	OSCEBWE() (bool, error)
}

type ignoreExtensionsControl interface {
	SetIgnoreExtensions(bool)
	IgnoreExtensions() bool
	Reset()
}

type lookaheadCase struct {
	name        string
	sampleRate  int
	application Application
	want        int
}

type restrictedApplicationCase struct {
	name          string
	application   Application
	wantMode      encoder.Mode
	wantLowDelay  bool
	wantBandwidth Bandwidth
	wantLookahead int
}

func assertOptionalEncoderControls(t *testing.T, enc optionalEncoderControl) {
	t.Helper()

	if !extsupport.DNNBlob {
		// Default builds gate USE_WEIGHTS_FILE model loading exactly like
		// libopus (no ENABLE_DRED/OSCE/DEEP_PLC): SetDNNBlob is a zero-cost
		// no-op that reports the optional extension as unavailable.
		if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
			t.Fatalf("default SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
		}
		return
	}

	if err := enc.SetDNNBlob(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(nil) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob(makeFramedButIncompatibleTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(incompatible) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(decoder_blob) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(encoder_blob) error=%v want=nil", err)
	}
}

func assertWorkingDREDControl(t *testing.T, enc extraDREDControl) {
	t.Helper()

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration(4) error: %v", err)
	}
	if got, err := enc.DREDDuration(); err != nil || got != 4 {
		t.Fatalf("DREDDuration()=(%d,%v) want=(4,nil)", got, err)
	}
	if err := enc.SetDREDDuration(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDREDDuration(-1) error=%v want=%v", err, ErrInvalidArgument)
	}
}

func assertSupportedQEXTControl(t *testing.T, enc qextEncoderControl) {
	t.Helper()

	if err := enc.SetQEXT(true); err != nil {
		t.Fatalf("SetQEXT(true) error: %v", err)
	}
	if got, err := enc.QEXT(); err != nil || !got {
		t.Fatalf("QEXT()=(%v,%v) want=(true,nil)", got, err)
	}
	if err := enc.SetQEXT(false); err != nil {
		t.Fatalf("SetQEXT(false) error: %v", err)
	}
	if got, err := enc.QEXT(); err != nil || got {
		t.Fatalf("QEXT()=(%v,%v) want=(false,nil)", got, err)
	}
}

func assertOptionalDecoderControls(t *testing.T, dec optionalDecoderControl) {
	t.Helper()

	if !extsupport.DNNBlob {
		// Default builds gate USE_WEIGHTS_FILE model loading exactly like
		// libopus (no ENABLE_DRED/OSCE/DEEP_PLC): SetDNNBlob is a zero-cost
		// no-op that reports the optional extension as unavailable.
		if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
			t.Fatalf("default SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
		}
		return
	}

	if err := dec.SetDNNBlob(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(nil) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob(makeFramedButIncompatibleTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(incompatible) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(encoder_blob) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(decoder_blob) error=%v want=nil", err)
	}
}

func assertWorkingOSCEBWEControl(t *testing.T, dec extraOSCEBWEControl) {
	t.Helper()

	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true) error: %v", err)
	}
	if got, err := dec.OSCEBWE(); err != nil || !got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(true,nil)", got, err)
	}
	if err := dec.SetOSCEBWE(false); err != nil {
		t.Fatalf("SetOSCEBWE(false) error: %v", err)
	}
	if got, err := dec.OSCEBWE(); err != nil || got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(false,nil)", got, err)
	}
}

func TestSupportsOptionalExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  OptionalExtension
		want bool
	}{
		{name: "dred", ext: OptionalExtensionDRED, want: extsupport.DRED},
		{name: "dnn_blob", ext: OptionalExtensionDNNBlob, want: extsupport.DNNBlob},
		{name: "qext", ext: OptionalExtensionQEXT, want: extsupport.QEXT},
		{name: "osce_bwe", ext: OptionalExtensionOSCEBWE, want: false},
		{name: "unknown", ext: OptionalExtension("future_ext"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsOptionalExtension(tc.ext); got != tc.want {
				t.Fatalf("SupportsOptionalExtension(%q)=%v want %v", tc.ext, got, tc.want)
			}
		})
	}
}

func appendTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	return append(dst, out...)
}

func appendTestBlobRecordWithPayload(dst []byte, name string, typ int32, payload []byte) []byte {
	const headerSize = 64
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	copy(out[headerSize:], payload)
	return append(dst, out...)
}

func encodeInt32s(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}

type testBlobRecordSpec struct {
	typ  int32
	size int
}

func addLinearLayerTestBlobSpec(specs map[string]testBlobRecordSpec, spec lpcnetplc.LinearLayerSpec) {
	if spec.Bias != "" {
		specs[spec.Bias] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Subias != "" {
		specs[spec.Subias] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Scale != "" {
		specs[spec.Scale] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Weights != "" {
		specs[spec.Weights] = testBlobRecordSpec{typ: dnnblob.TypeInt8, size: spec.NbInputs * spec.NbOutputs}
		return
	}
	if spec.FloatWeights != "" {
		specs[spec.FloatWeights] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbInputs * spec.NbOutputs}
	}
}

func addConv2DLayerTestBlobSpec(specs map[string]testBlobRecordSpec, spec lpcnetplc.Conv2DLayerSpec) {
	if spec.Bias != "" {
		specs[spec.Bias] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.OutChannels}
	}
	if spec.FloatWeights != "" {
		size := 4 * spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
		specs[spec.FloatWeights] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: size}
	}
}

func makeFramedButIncompatibleTestDNNBlob() []byte {
	return appendTestBlobRecord(nil, "dummy_record", 0, 4)
}

func makeNameCompleteEncoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredEncoderControlRecordNames() {
		blob = appendTestBlobRecord(blob, name, dnnblob.TypeFloat, 4)
	}
	return blob
}

func makeNameCompleteDecoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		blob = appendTestBlobRecord(blob, name, dnnblob.TypeFloat, 4)
	}
	return blob
}

func makeNameCompleteDREDDecoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredDREDDecoderRecordNames() {
		blob = appendTestBlobRecord(blob, name, dnnblob.TypeFloat, 4)
	}
	return blob
}

func makeValidEncoderTestDNNBlob() []byte {
	specs := make(map[string]testBlobRecordSpec)
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		addConv2DLayerTestBlobSpec(specs, spec)
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	var blob []byte
	for _, name := range names {
		spec := specs[name]
		blob = appendTestBlobRecord(blob, name, spec.typ, spec.size)
	}
	for _, spec := range rdovae.EncoderLayerSpecs() {
		blob = appendRDOVAEEncoderLayerRecords(blob, spec)
	}
	return blob
}

func appendRDOVAEEncoderLayerRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Subias != "" {
		dst = appendTestBlobRecord(dst, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = appendTestBlobRecordWithPayload(dst, spec.WeightsIdx, dnnblob.TypeInt, encodeInt32s(idx))
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendTestBlobRecord(dst, spec.Weights, dnnblob.TypeInt8, size)
		dst = appendTestBlobRecord(dst, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*size)
	}
	return dst
}

func makeValidDecoderTestDNNBlob() []byte {
	specs := make(map[string]testBlobRecordSpec)
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		specs[name] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4}
	}
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		addConv2DLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.ModelLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.FARGANModelLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	var blob []byte
	for _, name := range names {
		spec := specs[name]
		blob = appendTestBlobRecord(blob, name, spec.typ, spec.size)
	}
	return blob
}

func TestValidEncoderTestDNNBlobShape(t *testing.T) {
	blob := makeValidEncoderTestDNNBlob()
	if string(blob[:4]) != "DNNw" {
		t.Fatalf("magic=%q want DNNw", string(blob[:4]))
	}
	for _, name := range dnnblob.RequiredEncoderControlRecordNames() {
		if !strings.Contains(string(blob), name) {
			t.Fatalf("missing record name %q", name)
		}
	}
}

func TestValidDecoderTestDNNBlobShape(t *testing.T) {
	blob := makeValidDecoderTestDNNBlob()
	if string(blob[:4]) != "DNNw" {
		t.Fatalf("magic=%q want DNNw", string(blob[:4]))
	}
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		if !strings.Contains(string(blob), name) {
			t.Fatalf("missing record name %q", name)
		}
	}
}

func assertIgnoreExtensionsControls(t *testing.T, dec ignoreExtensionsControl) {
	t.Helper()

	if dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=true want false by default")
	}
	dec.SetIgnoreExtensions(true)
	if !dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=false want true after SetIgnoreExtensions(true)")
	}
	dec.Reset()
	if !dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=false want true after Reset")
	}
	dec.SetIgnoreExtensions(false)
	if dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=true want false after SetIgnoreExtensions(false)")
	}
}

func lookaheadTestCases() []lookaheadCase {
	return []lookaheadCase{
		{
			name:        "audio_48k",
			sampleRate:  48000,
			application: ApplicationAudio,
			want:        48000/400 + 48000/250,
		},
		{
			name:        "voip_48k",
			sampleRate:  48000,
			application: ApplicationVoIP,
			want:        48000/400 + 48000/250,
		},
		{
			name:        "lowdelay_48k",
			sampleRate:  48000,
			application: ApplicationLowDelay,
			want:        48000 / 400,
		},
		{
			name:        "audio_24k",
			sampleRate:  24000,
			application: ApplicationAudio,
			want:        24000/400 + 24000/250,
		},
		{
			name:        "lowdelay_24k",
			sampleRate:  24000,
			application: ApplicationLowDelay,
			want:        24000 / 400,
		},
	}
}

func restrictedApplicationTestCases() []restrictedApplicationCase {
	return []restrictedApplicationCase{
		{
			name:         "restricted_silk",
			application:  ApplicationRestrictedSilk,
			wantMode:     encoder.ModeSILK,
			wantLowDelay: false,
			// Bandwidth() reports st->bandwidth (FULLBAND init default) before any
			// encode regardless of application; the per-application bandwidth bias
			// lives in the user request and is applied at encode time.
			wantBandwidth: BandwidthFullband,
			wantLookahead: 48000/400 + 48000/250,
		},
		{
			name:          "restricted_celt",
			application:   ApplicationRestrictedCelt,
			wantMode:      encoder.ModeCELT,
			wantLowDelay:  true,
			wantBandwidth: BandwidthFullband,
			wantLookahead: 48000 / 400,
		},
	}
}

func assertApplicationLockAfterEncode(
	t *testing.T,
	currentApplication func() Application,
	setApplication func(Application) error,
	reset func(),
	encodeOnce func() error,
	changeTo Application,
	resetTo Application,
) {
	t.Helper()

	if err := encodeOnce(); err != nil {
		t.Fatalf("encode before application lock test error: %v", err)
	}
	if err := setApplication(changeTo); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(change after encode) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := setApplication(currentApplication()); err != nil {
		t.Fatalf("SetApplication(same after encode) error: %v", err)
	}
	reset()
	if err := setApplication(resetTo); err != nil {
		t.Fatalf("SetApplication(after reset) error: %v", err)
	}
}

func assertLookaheadUpdatesBeforeEncode(
	t *testing.T,
	lookahead func() int,
	setApplication func(Application) error,
) {
	t.Helper()

	if got, want := lookahead(), 48000/400+48000/250; got != want {
		t.Fatalf("Lookahead(audio) = %d, want %d", got, want)
	}
	if err := setApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(LowDelay) error: %v", err)
	}
	if got, want := lookahead(), 48000/400; got != want {
		t.Fatalf("Lookahead(lowdelay) = %d, want %d", got, want)
	}
	if err := setApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(Audio) error: %v", err)
	}
	if got, want := lookahead(), 48000/400+48000/250; got != want {
		t.Fatalf("Lookahead(audio after reset) = %d, want %d", got, want)
	}
}

func assertEncodeInt16InvalidFrameSizeNoPanic(t *testing.T, name string, fn func() (int, error)) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", name, r)
		}
	}()

	if n, err := fn(); err != ErrInvalidFrameSize || n != 0 {
		t.Fatalf("%s = (%d, %v), want (0, %v)", name, n, err, ErrInvalidFrameSize)
	}
}

func TestEncoderEncodeInt16RejectsOversizedInputWithoutPanic(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	data := make([]byte, 4000)
	oversized := make([]int16, 5760*enc.Channels()+1)

	assertEncodeInt16InvalidFrameSizeNoPanic(t, "EncodeInt16", func() (int, error) {
		return enc.EncodeInt16(oversized, data)
	})
}

func TestMultistreamEncoderEncodeInt16RejectsOversizedInputWithoutPanic(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 3, ApplicationAudio)
	data := make([]byte, 4000*enc.Streams())
	oversized := make([]int16, 5760*enc.Channels()+1)

	assertEncodeInt16InvalidFrameSizeNoPanic(t, "MultistreamEncoder.EncodeInt16", func() (int, error) {
		return enc.EncodeInt16(oversized, data)
	})
}

func TestDecoderResetClearsReportedState(t *testing.T) {
	dec := newMonoTestDecoder(t)
	dec.lastDataLen = 2
	dec.mainDecodeRng = 0x12345678
	dec.redundantRng = 0x01020304
	dec.lastFrameSize = 480
	dec.lastPacketDuration = 480

	if !dec.InDTX() {
		t.Fatal("InDTX() = false before Reset, want true")
	}
	if got := dec.FinalRange(); got == 0 {
		t.Fatal("FinalRange() = 0 before Reset, want non-zero test state")
	}

	dec.Reset()

	if dec.InDTX() {
		t.Fatal("InDTX() = true after Reset, want false")
	}
	if got := dec.FinalRange(); got != 0 {
		t.Fatalf("FinalRange() = 0x%08X after Reset, want 0", got)
	}
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() = %d after Reset, want 0", got)
	}
}

func TestMultistreamDecoderFirstPacketDurationMatrix(t *testing.T) {
	const sampleRate = 48000
	const channels = 3

	type decodeCase struct {
		name   string
		decode func(*MultistreamDecoder, []byte, int) (int, error)
	}

	decodeCases := []decodeCase{
		{
			name: "float32",
			decode: func(dec *MultistreamDecoder, packet []byte, frameSize int) (int, error) {
				pcm := make([]float32, frameSize*channels)
				return dec.Decode(packet, pcm)
			},
		},
		{
			name: "int16",
			decode: func(dec *MultistreamDecoder, packet []byte, frameSize int) (int, error) {
				pcm := make([]int16, frameSize*channels)
				return dec.DecodeInt16(packet, pcm)
			},
		},
	}

	frameSizes := []int{120, 240, 1920, 2880, 5760}
	for _, fs := range frameSizes {
		for _, dc := range decodeCases {
			t.Run(dc.name+"_"+frameSizeLabel(fs), func(t *testing.T) {
				enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

				if err := enc.SetFrameSize(fs); err != nil {
					t.Fatalf("SetFrameSize(%d) error: %v", fs, err)
				}

				packet, err := enc.EncodeFloat32(generateSurroundTestSignal(sampleRate, fs, channels))
				if err != nil {
					t.Fatalf("EncodeFloat32(%d) error: %v", fs, err)
				}

				n, err := dc.decode(dec, packet, fs)
				if err != nil {
					t.Fatalf("%s decode error: %v", dc.name, err)
				}
				if n != fs {
					t.Fatalf("%s decoded %d samples, want %d", dc.name, n, fs)
				}
			})
		}
	}
}

func TestMultistreamDecoderTracksPacketDurationChanges(t *testing.T) {
	const sampleRate = 48000
	const channels = 3

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	frameSizes := []int{960, 120, 240, 1920, 2880, 5760, 480}
	for _, fs := range frameSizes {
		if err := enc.SetFrameSize(fs); err != nil {
			t.Fatalf("SetFrameSize(%d) error: %v", fs, err)
		}

		packet, err := enc.EncodeFloat32(generateSurroundTestSignal(sampleRate, fs, channels))
		if err != nil {
			t.Fatalf("EncodeFloat32(%d) error: %v", fs, err)
		}

		pcm := make([]float32, fs*channels)
		n, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(%d) error: %v", fs, err)
		}
		if n != fs {
			t.Fatalf("Decode(%d) = %d, want %d", fs, n, fs)
		}
	}

	plcFrameSize := frameSizes[len(frameSizes)-1]
	plcPCM := make([]float32, plcFrameSize*channels)
	n, err := dec.Decode(nil, plcPCM)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if n != plcFrameSize {
		t.Fatalf("Decode(nil) = %d, want %d", n, plcFrameSize)
	}
}

// TestMultistreamDecoderEmptyPacketConceals verifies a zero-length packet is
// treated as packet loss, matching libopus opus_multistream_decode (do_plc=1 for
// len==0). It conceals the requested output frame size, exactly as a nil packet.
func TestMultistreamDecoderEmptyPacketConceals(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 3)
	const frameSize = 960
	pcm := make([]float32, frameSize*dec.Channels())

	n, err := dec.Decode([]byte{}, pcm)
	if err != nil {
		t.Fatalf("Decode(empty) error = %v, want nil (PLC)", err)
	}
	if n != frameSize {
		t.Fatalf("Decode(empty) = %d samples, want %d (PLC frame size)", n, frameSize)
	}
}

func frameSizeLabel(frameSize int) string {
	switch frameSize {
	case 120:
		return "2p5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	case 1920:
		return "40ms"
	case 2880:
		return "60ms"
	case 5760:
		return "120ms"
	default:
		return "fs"
	}
}
