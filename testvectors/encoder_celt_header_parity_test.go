package testvectors

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/rangecoding"
)

type celtPostfilterHeader struct {
	period int
	qg     int
	tapset int
}

func TestEncoderVariantCELTHeaderParityAgainstFixture(t *testing.T) {
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	// This case still exposes a pitch-search/remove-doubling gap, so keep it
	// out of the exact header ratchet until that path is green.
	knownHeaderGaps := map[string]struct{}{
		"CELT-FB-2.5ms-mono-64k-speech_like_v1": {},
	}
	expectedCoverage := 27
	if runtime.GOARCH == "amd64" {
		// These tone-path chirp cases remain excluded on amd64 until the x86
		// CELT tone analysis path is matched to its libopus fixture.
		knownHeaderGaps["CELT-FB-10ms-mono-64k-chirp_sweep_v1"] = struct{}{}
		knownHeaderGaps["CELT-FB-2.5ms-mono-64k-chirp_sweep_v1"] = struct{}{}
		expectedCoverage = 25
	}

	covered := 0
	for _, c := range fixture.Cases {
		if c.Mode != fixtureModeName(encoder.ModeCELT) {
			continue
		}
		testName := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		if _, ok := knownHeaderGaps[testName]; ok {
			continue
		}
		c := c
		t.Run(testName, func(t *testing.T) {
			assertCELTVariantPostfilterHeaderParityForCase(t, c)
		})
		covered++
	}
	if covered != expectedCoverage {
		t.Fatalf("CELT header fixture coverage mismatch: got=%d want=%d", covered, expectedCoverage)
	}
}

func assertCELTVariantPostfilterHeaderParityForCase(t *testing.T, fixtureCase encoderComplianceVariantsFixtureCase) {
	t.Helper()

	totalSamples := fixtureCase.SignalFrames * fixtureCase.FrameSize * fixtureCase.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(
		fixtureCase.Variant,
		48000,
		totalSamples,
		fixtureCase.Channels,
	)
	if err != nil {
		t.Fatalf("generate signal: %v", err)
	}
	if got := testsignal.HashFloat32LE(signal); got != fixtureCase.SignalSHA256 {
		t.Fatalf("signal hash mismatch: got=%s want=%s", got, fixtureCase.SignalSHA256)
	}

	libPackets, _, err := decodeEncoderVariantsFixturePackets(fixtureCase)
	if err != nil {
		t.Fatalf("decode fixture packets: %v", err)
	}
	goPackets, goStats, err := encodeGopusForVariantsCaseWithPrefilterStats(fixtureCase, signal)
	if err != nil {
		t.Fatalf("encode gopus packets with stats: %v", err)
	}
	if len(goPackets) != len(libPackets) {
		t.Fatalf("packet count mismatch: got=%d want=%d", len(goPackets), len(libPackets))
	}
	if len(goStats) != len(goPackets) {
		t.Fatalf("prefilter stats count mismatch: got=%d want=%d", len(goStats), len(goPackets))
	}

	libDec := celt.NewDecoder(fixtureCase.Channels)
	goDec := celt.NewDecoder(fixtureCase.Channels)

	var mismatches []string
	for i := range libPackets {
		got, err := decodeCELTPostfilterHeader(goDec, goPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("decode gopus header frame %d: %v", i, err)
		}
		want, err := decodeCELTPostfilterHeader(libDec, libPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("decode fixture header frame %d: %v", i, err)
		}
		if got != want {
			mismatches = append(mismatches, fmt.Sprintf("frame %d: got pitch=%d qg=%d tap=%d, want pitch=%d qg=%d tap=%d",
				i, got.period, got.qg, got.tapset, want.period, want.qg, want.tapset))
			stats := goStats[i]
			t.Logf("frame %d stats: enabled=%v tonePath=%v pitchPath=%v tf=%.6f toneFreq=%.6f toneish=%.6f maxPitchRatio=%.6f search=%d beforeRD=%d afterRD=%d pfOn=%v qg=%d gain=%.6f",
				i,
				stats.Enabled,
				stats.UsedTonePath,
				stats.UsedPitchPath,
				stats.TFEstimate,
				stats.ToneFreq,
				stats.Toneishness,
				stats.MaxPitchRatio,
				stats.PitchSearchOut,
				stats.PitchBeforeRD,
				stats.PitchAfterRD,
				stats.PFOn,
				stats.QG,
				stats.Gain,
			)
		}
	}

	for _, msg := range mismatches {
		t.Log(msg)
	}
	if len(mismatches) > 0 {
		t.Fatalf("CELT postfilter header mismatches: %d/%d", len(mismatches), len(libPackets))
	}
}

func decodeCELTPostfilterHeader(dec *celt.Decoder, packet []byte, frameSize int) (celtPostfilterHeader, error) {
	if len(packet) < 2 {
		return celtPostfilterHeader{}, fmt.Errorf("packet too short: %d", len(packet))
	}
	if getModeFromConfig(packet[0]>>3) != "CELT" {
		return celtPostfilterHeader{}, fmt.Errorf("unexpected non-CELT config %d", packet[0]>>3)
	}

	var rd rangecoding.Decoder
	rd.Init(packet[1:])
	if _, err := dec.DecodeFrameWithDecoder(&rd, frameSize); err != nil {
		return celtPostfilterHeader{}, err
	}
	return celtPostfilterHeader{
		period: dec.PostfilterPeriod(),
		qg:     qgFromPostfilterGain(dec.PostfilterGain()),
		tapset: dec.PostfilterTapset(),
	}, nil
}

func qgFromPostfilterGain(gain float64) int {
	if gain <= 0 {
		return 0
	}
	return int(math.Round(gain/0.09375)) - 1
}

func encodeGopusForVariantsCaseWithPrefilterStats(c encoderComplianceVariantsFixtureCase, signal []float32) ([][]byte, []celt.PrefilterDebugStats, error) {
	mode, err := parseFixtureMode(c.Mode)
	if err != nil {
		return nil, nil, err
	}
	bandwidth, err := parseFixtureBandwidth(c.Bandwidth)
	if err != nil {
		return nil, nil, err
	}

	enc := encoder.NewEncoder(48000, c.Channels)
	encMode := mode
	if mode == encoder.ModeHybrid {
		encMode = encoder.ModeAuto
	}
	enc.SetLowDelay(mode == encoder.ModeCELT)
	enc.SetMode(encMode)
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(c.Bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)

	stats := make([]celt.PrefilterDebugStats, 0, c.Frames)
	enc.SetCELTPrefilterDebugHook(func(s celt.PrefilterDebugStats) {
		stats = append(stats, s)
	})

	packets := make([][]byte, 0, c.Frames)
	samplesPerFrame := c.FrameSize * c.Channels
	for i := 0; i < c.SignalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		frame := float32ToFloat64OpusDemoF32(signal[start:end])
		pkt, err := enc.Encode(frame, c.FrameSize)
		if err != nil {
			return nil, nil, fmt.Errorf("encode frame %d: %w", i, err)
		}
		if len(pkt) == 0 {
			return nil, nil, fmt.Errorf("empty packet at frame %d", i)
		}
		pktCopy := make([]byte, len(pkt))
		copy(pktCopy, pkt)
		packets = append(packets, pktCopy)
	}

	if len(packets) < c.Frames {
		flushLimit := c.Frames + 4
		silence := make([]float64, samplesPerFrame)
		for len(packets) < c.Frames && len(packets) < flushLimit {
			pkt, err := enc.Encode(silence, c.FrameSize)
			if err != nil {
				return nil, nil, fmt.Errorf("flush frame %d: %w", len(packets), err)
			}
			if len(pkt) == 0 {
				continue
			}
			pktCopy := make([]byte, len(pkt))
			copy(pktCopy, pkt)
			packets = append(packets, pktCopy)
		}
	}
	return packets, stats, nil
}
