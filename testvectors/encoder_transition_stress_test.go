package testvectors

import (
	"fmt"
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

func appendSignalVariant(dst []float32, variant string, sampleRate, samples, channels int) []float32 {
	seg, err := testsignal.GenerateEncoderSignalVariant(variant, sampleRate, samples*channels, channels)
	if err != nil {
		panic(fmt.Sprintf("generate signal variant %q: %v", variant, err))
	}
	return append(dst, seg...)
}

func rmsFloat32(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum / float64(len(v)))
}

func encodeFramesForSignal(t *testing.T, enc *encoder.Encoder, signal []float32, frameSize, channels int) [][]byte {
	t.Helper()
	samplesPerFrame := frameSize * channels
	numFrames := len(signal) / samplesPerFrame
	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pkt, err := enc.Encode(float32ToFloat64(signal[start:end]), frameSize)
		if err != nil {
			t.Fatalf("encode frame %d: %v", i, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("empty packet at frame %d", i)
		}
		p := make([]byte, len(pkt))
		copy(p, pkt)
		packets[i] = p
	}
	return packets
}

func TestEncoderModeSwitchStreamQuality(t *testing.T) {
	requireTestTier(t, testTierParity)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 480 // 10ms
	)

	enc := encoder.NewEncoder(sampleRate, channels)
	enc.SetMode(encoder.ModeSILK)
	enc.SetSignalType(types.SignalVoice)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(32000)

	// Force deterministic transitions through explicit mode changes across segments.
	type segment struct {
		variant string
		mode    encoder.Mode
		bw      types.Bandwidth
		signal  types.Signal
		bitrate int
	}
	segments := []segment{
		{variant: testsignal.EncoderVariantSpeechLikeV1, mode: encoder.ModeSILK, bw: types.BandwidthWideband, signal: types.SignalVoice, bitrate: 32000},
		{variant: testsignal.EncoderVariantAMMultisineV1, mode: encoder.ModeCELT, bw: types.BandwidthFullband, signal: types.SignalMusic, bitrate: 64000},
		{variant: testsignal.EncoderVariantChirpSweepV1, mode: encoder.ModeHybrid, bw: types.BandwidthFullband, signal: types.SignalVoice, bitrate: 64000},
	}

	var signal []float32
	var packets [][]byte
	samplesPerFrame := frameSize * channels
	for _, seg := range segments {
		enc.SetMode(seg.mode)
		enc.SetBandwidth(seg.bw)
		enc.SetSignalType(seg.signal)
		enc.SetBitrate(seg.bitrate)
		segSig, err := testsignal.GenerateEncoderSignalVariant(seg.variant, sampleRate, sampleRate*channels, channels)
		if err != nil {
			t.Fatalf("generate segment signal: %v", err)
		}
		signal = append(signal, segSig...)
		numFrames := len(segSig) / samplesPerFrame
		for i := 0; i < numFrames; i++ {
			start := i * samplesPerFrame
			end := start + samplesPerFrame
			pkt, err := enc.Encode(float32ToFloat64(segSig[start:end]), frameSize)
			if err != nil {
				t.Fatalf("encode segment frame %d: %v", i, err)
			}
			if len(pkt) == 0 {
				t.Fatalf("empty packet at segment frame %d", i)
			}
			p := make([]byte, len(pkt))
			copy(p, pkt)
			packets = append(packets, p)
		}
	}

	seenMode := map[gopus.Mode]int{}
	for i, p := range packets {
		toc := gopus.ParseTOC(p[0])
		seenMode[toc.Mode]++
		if i < 5 {
			t.Logf("frame %d mode=%v bw=%v packet=%d", i, toc.Mode, toc.Bandwidth, len(p))
		}
	}
	if len(seenMode) < 2 {
		t.Fatalf("mode switch stream failed to transition, seen=%v", seenMode)
	}

	decoded, err := decodeComplianceWithInternalDecoder(packets, channels)
	if err != nil {
		t.Fatalf("internal decode: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("decoded stream is empty")
	}
	preSkip := OpusPreSkip * channels
	if len(decoded) > preSkip {
		decoded = decoded[preSkip:]
	}

	compareLen := len(signal)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}
	q, delay := ComputeQualityFloat32WithDelay(decoded[:compareLen], signal[:compareLen], sampleRate, 2*frameSize)
	if math.IsNaN(q) || math.IsInf(q, 0) {
		t.Fatalf("invalid quality for mode transitions: q=%v", q)
	}
	inRMS := rmsFloat32(signal[:compareLen])
	outRMS := rmsFloat32(decoded[:compareLen])
	ratio := 0.0
	if inRMS > 0 {
		ratio = outRMS / inRMS
	}
	if inRMS > 0 && (ratio < 0.05 || ratio > 5.0) {
		t.Fatalf("mode-transition energy regression: q=%.2f snr=%.2f delay=%d rmsRatio=%.3f", q, SNRFromQuality(q), delay, ratio)
	}
}

func TestLongFrameAbove960StabilityMatrix(t *testing.T) {
	requireTestTier(t, testTierParity)

	cases := []struct {
		name      string
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		channels  int
		bitrate   int
	}{
		{"silk-wb-40ms", encoder.ModeSILK, types.BandwidthWideband, 1920, 1, 32000},
		{"silk-wb-60ms", encoder.ModeSILK, types.BandwidthWideband, 2880, 1, 32000},
		{"hybrid-swb-40ms", encoder.ModeHybrid, types.BandwidthSuperwideband, 1920, 1, 48000},
		{"hybrid-fb-60ms", encoder.ModeHybrid, types.BandwidthFullband, 2880, 1, 64000},
		{"celt-fb-40ms", encoder.ModeCELT, types.BandwidthFullband, 1920, 1, 64000},
		{"celt-fb-60ms", encoder.ModeCELT, types.BandwidthFullband, 2880, 1, 64000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			totalSamples := tc.frameSize * 8 * tc.channels
			signal, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantSpeechLikeV1, 48000, totalSamples, tc.channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}

			enc := encoder.NewEncoder(48000, tc.channels)
			enc.SetMode(tc.mode)
			enc.SetBandwidth(tc.bandwidth)
			enc.SetBitrate(tc.bitrate)
			switch tc.mode {
			case encoder.ModeSILK, encoder.ModeHybrid:
				enc.SetSignalType(types.SignalVoice)
			default:
				enc.SetSignalType(types.SignalMusic)
			}
			packets := encodeFramesForSignal(t, enc, signal, tc.frameSize, tc.channels)

			for i, p := range packets {
				if len(p) == 0 {
					t.Fatalf("frame %d encoded empty packet", i)
				}
				toc := gopus.ParseTOC(p[0])
				if tc.mode != encoder.ModeSILK && tc.frameSize > 960 && toc.FrameCode != 3 {
					t.Fatalf("frame %d expected frame code 3 for long packet, got %d", i, toc.FrameCode)
				}
			}

			decoded, err := decodeComplianceWithInternalDecoder(packets, tc.channels)
			if err != nil {
				t.Fatalf("internal decode failed: %v", err)
			}
			if len(decoded) == 0 {
				t.Fatal("decoded stream is empty")
			}
			preSkip := OpusPreSkip * tc.channels
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}

			compareLen := len(signal)
			if len(decoded) < compareLen {
				compareLen = len(decoded)
			}
			q, delay := ComputeQualityFloat32WithDelay(decoded[:compareLen], signal[:compareLen], 48000, tc.frameSize)
			if math.IsNaN(q) || math.IsInf(q, 0) {
				t.Fatalf("invalid quality result: q=%v", q)
			}
			if q < -95.0 {
				t.Fatalf("long-frame stability regression: q=%.2f snr=%.2f delay=%d", q, SNRFromQuality(q), delay)
			}

			inRMS := rmsFloat32(signal[:compareLen])
			outRMS := rmsFloat32(decoded[:compareLen])
			if inRMS > 0 {
				ratio := outRMS / inRMS
				if ratio < 0.20 || ratio > 2.20 {
					t.Fatalf("long-frame RMS ratio out of bounds: ratio=%.3f in=%.6f out=%.6f", ratio, inRMS, outRMS)
				}
			}
		})
	}
}
