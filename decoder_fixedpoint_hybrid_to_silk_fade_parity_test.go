//go:build gopus_fixed_point

package gopus

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type fixedHybridToSILKStep struct {
	name   string
	packet []byte
	format uint32
}

func assertFixedHybridToSILKWarmZeroAllocs(t *testing.T, sampleRate, channels int, hybrid, silk []byte) {
	t.Helper()

	hybridSamples, err := packetSamplesAtRate(hybrid, sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	silkSamples, err := packetSamplesAtRate(silk, sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	out16 := make([]int16, hybridSamples*channels)
	out24 := make([]int32, silkSamples*channels)
	var hybridN, silkN int
	var decodeErr error
	decode := func() {
		dec.Reset()
		hybridN, decodeErr = dec.DecodeInt16(hybrid, out16)
		if decodeErr != nil {
			return
		}
		silkN, decodeErr = dec.DecodeInt24(silk, out24)
	}

	decode()
	if decodeErr != nil || hybridN != hybridSamples || silkN != silkSamples {
		t.Fatalf("warm Hybrid→SILK decode counts=(%d,%d) want=(%d,%d), err=%v", hybridN, silkN, hybridSamples, silkSamples, decodeErr)
	}
	allocs := testing.AllocsPerRun(20, decode)
	if decodeErr != nil || hybridN != hybridSamples || silkN != silkSamples {
		t.Fatalf("measured Hybrid→SILK decode counts=(%d,%d) want=(%d,%d), err=%v", hybridN, silkN, hybridSamples, silkSamples, decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm Hybrid→SILK decode allocated %g times per sequence", allocs)
	}
}

// assertFixedHybridToSILKSequence checks a persistent Go decoder against the
// selected fixed-point C decoder while changing public PCM APIs between frames.
func assertFixedHybridToSILKSequence(t *testing.T, dec *Decoder, sampleRate, channels int, steps []fixedHybridToSILKStep) {
	t.Helper()

	cases := make([]libopustest.DecodeDiffCase, len(steps))
	frameSizes := make([]int, len(steps))
	previousFrameSize := sampleRate / 50
	for i, step := range steps {
		frameSize := previousFrameSize
		if len(step.packet) > 0 {
			var err error
			frameSize, err = packetSamplesAtRate(step.packet, sampleRate)
			if err != nil {
				t.Fatalf("step %d %s packetSamplesAtRate: %v", i, step.name, err)
			}
		}
		frameSizes[i] = frameSize
		previousFrameSize = frameSize
		cases[i] = libopustest.DecodeDiffCase{
			Packet:    step.packet,
			Format:    step.format,
			FrameSize: uint32(frameSize),
		}
	}

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		t.Fatal(err)
	}
	for i, step := range steps {
		frameSize := frameSizes[i]
		sampleCount := frameSize * channels
		var gotF32 []float32
		var gotI16 []int16
		var gotI24 []int32
		var n int
		switch step.format {
		case libopustest.DecodeDiffFormatFloat32:
			gotF32 = make([]float32, sampleCount)
			n, err = dec.Decode(step.packet, gotF32)
		case libopustest.DecodeDiffFormatInt16:
			gotI16 = make([]int16, sampleCount)
			n, err = dec.DecodeInt16(step.packet, gotI16)
		case libopustest.DecodeDiffFormatInt24:
			gotI24 = make([]int32, sampleCount)
			n, err = dec.DecodeInt24(step.packet, gotI24)
		default:
			t.Fatalf("step %d %s has unsupported PCM format %d", i, step.name, step.format)
		}
		if err != nil || n != int(want[i].Code) {
			t.Fatalf("step %d %s samples=%d err=%v C=%d", i, step.name, n, err, want[i].Code)
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step %d %s final range=%08x C=%08x", i, step.name, got, want[i].FinalRange)
		}

		bytesPerSample := 4
		if step.format == libopustest.DecodeDiffFormatInt16 {
			bytesPerSample = 2
		}
		if len(want[i].PCM) != sampleCount*bytesPerSample {
			t.Fatalf("step %d %s C PCM bytes=%d want=%d", i, step.name, len(want[i].PCM), sampleCount*bytesPerSample)
		}
		for j := 0; j < sampleCount; j++ {
			var got, expected int64
			switch step.format {
			case libopustest.DecodeDiffFormatFloat32:
				got = int64(math.Float32bits(gotF32[j]))
				expected = int64(binary.LittleEndian.Uint32(want[i].PCM[j*4:]))
			case libopustest.DecodeDiffFormatInt16:
				got = int64(gotI16[j])
				expected = int64(int16(binary.LittleEndian.Uint16(want[i].PCM[j*2:])))
			case libopustest.DecodeDiffFormatInt24:
				got = int64(gotI24[j])
				expected = int64(int32(binary.LittleEndian.Uint32(want[i].PCM[j*4:])))
			}
			if got != expected {
				t.Fatalf("step %d %s sample %d Go=%d C=%d", i, step.name, j, got, expected)
			}
		}
	}
}

// TestDecoderFixedPointHybridToSILKFadeParity covers the 2.5 ms CELT silence
// accumulation that opus_decode_frame applies after SILK when the previous mode
// is Hybrid. The test uses a 60 ms SILK packet so the captured fixed-point output
// starts after the Hybrid frame in the same persistent state and exercises the
// packet-tail offset used when adding the CELT overlap.
func TestDecoderFixedPointHybridToSILKFadeParity(t *testing.T) {
	libopustest.RequireOracle(t)

	hybrid := encodeAPIRateHybridPacketFrameSize(t, 1, 1920)
	silk := encodeAPIRateSILKPacketFrameSize(t, 2, 2880)
	celtShort := encodeAPIRateCELTPacketFrameSize(t, 1, 120)
	if got := ParseTOC(hybrid[0]).Mode; got != ModeHybrid {
		t.Fatalf("Hybrid witness TOC mode=%v", got)
	}
	if got := ParseTOC(silk[0]).Mode; got != ModeSILK {
		t.Fatalf("SILK witness TOC mode=%v", got)
	}

	sampleRates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		sampleRates = append(sampleRates, 96000)
	}
	for _, sampleRate := range sampleRates {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dHz/ch%d", sampleRate, channels), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				assertFixedHybridToSILKSequence(t, dec, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "hybrid-40ms-int16", packet: hybrid, format: libopustest.DecodeDiffFormatInt16},
					{name: "silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
					{name: "silk-60ms-float-followup", packet: silk, format: libopustest.DecodeDiffFormatFloat32},
					{name: "silk-plc-int16", format: libopustest.DecodeDiffFormatInt16},
					{name: "celt-2p5ms-int24-recovery", packet: celtShort, format: libopustest.DecodeDiffFormatInt24},
				})

				// A fresh SILK decode is the control: the one-unit mismatch requires
				// the preceding Hybrid state and its CELT overlap fade.
				fresh, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				assertFixedHybridToSILKSequence(t, fresh, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "fresh-silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
				})

				// Reset must clear the prior Hybrid/CELT history before comparing
				// the same sequence suffix with a newly initialized C decoder.
				dec.Reset()
				assertFixedHybridToSILKSequence(t, dec, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "reset-silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
					{name: "reset-hybrid-40ms-int16", packet: hybrid, format: libopustest.DecodeDiffFormatInt16},
					{name: "reset-hybrid-plc-float", format: libopustest.DecodeDiffFormatFloat32},
					{name: "reset-celt-2p5ms-int24", packet: celtShort, format: libopustest.DecodeDiffFormatInt24},
				})

				assertFixedHybridToSILKWarmZeroAllocs(t, sampleRate, channels, hybrid, silk)
			})
		}
	}
}
