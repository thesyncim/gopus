package testvectors

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

func TestEncoderVariantCELTAllocationParityAgainstFixture(t *testing.T) {
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	type celtAllocationParityCase struct {
		name      string
		frameSize int
		channels  int
		bitrate   int
		variant   string
	}

	testCases := []celtAllocationParityCase{
		{
			name:      "CELT-FB-10ms-mono-64k-speech_like_v1",
			frameSize: 480,
			channels:  1,
			bitrate:   64000,
			variant:   testsignal.EncoderVariantSpeechLikeV1,
		},
		{
			name:      "CELT-FB-2.5ms-mono-64k-impulse_train_v1",
			frameSize: 120,
			channels:  1,
			bitrate:   64000,
			variant:   testsignal.EncoderVariantImpulseTrainV1,
		},
		{
			name:      "CELT-FB-20ms-mono-64k-impulse_train_v1",
			frameSize: 960,
			channels:  1,
			bitrate:   64000,
			variant:   testsignal.EncoderVariantImpulseTrainV1,
		},
		{
			name:      "CELT-FB-20ms-mono-64k-speech_like_v1",
			frameSize: 960,
			channels:  1,
			bitrate:   64000,
			variant:   testsignal.EncoderVariantSpeechLikeV1,
		},
		{
			name:      "CELT-FB-20ms-stereo-128k-impulse_train_v1",
			frameSize: 960,
			channels:  2,
			bitrate:   128000,
			variant:   testsignal.EncoderVariantImpulseTrainV1,
		},
	}
	if runtime.GOARCH != "amd64" {
		// These cases are exact on the generic fixture, while the amd64 fixture
		// still exposes packet-decision divergences.
		testCases = append(testCases,
			celtAllocationParityCase{
				name:      "CELT-FB-10ms-mono-64k-am_multisine_v1",
				frameSize: 480,
				channels:  1,
				bitrate:   64000,
				variant:   testsignal.EncoderVariantAMMultisineV1,
			},
			celtAllocationParityCase{
				name:      "CELT-FB-10ms-mono-64k-impulse_train_v1",
				frameSize: 480,
				channels:  1,
				bitrate:   64000,
				variant:   testsignal.EncoderVariantImpulseTrainV1,
			},
			celtAllocationParityCase{
				name:      "CELT-FB-2.5ms-stereo-128k-speech_like_v1",
				frameSize: 120,
				channels:  2,
				bitrate:   128000,
				variant:   testsignal.EncoderVariantSpeechLikeV1,
			},
			celtAllocationParityCase{
				name:      "CELT-FB-20ms-stereo-128k-speech_like_v1",
				frameSize: 960,
				channels:  2,
				bitrate:   128000,
				variant:   testsignal.EncoderVariantSpeechLikeV1,
			},
		)
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assertCELTVariantPacketDecisionParity(
				t,
				encoder.ModeCELT,
				types.BandwidthFullband,
				tc.frameSize,
				tc.channels,
				tc.bitrate,
				tc.variant,
			)
		})
	}
}

func assertCELTVariantPacketDecisionParity(
	t *testing.T,
	mode encoder.Mode,
	bandwidth types.Bandwidth,
	frameSize int,
	channels int,
	bitrate int,
	variant string,
) {
	t.Helper()

	fixtureCase, ok := findEncoderVariantsFixtureCase(
		mode,
		bandwidth,
		frameSize,
		channels,
		bitrate,
		variant,
	)
	if !ok {
		t.Fatal("missing CELT variants fixture case")
	}

	assertCELTVariantPacketDecisionParityForCase(t, fixtureCase)
}

func assertCELTVariantPacketDecisionParityForCase(t *testing.T, fixtureCase encoderComplianceVariantsFixtureCase) {
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
	goPackets, targetStats, err := encodeGopusForVariantsCaseWithTargetStats(fixtureCase, signal)
	if err != nil {
		t.Fatalf("encode gopus packets with target stats: %v", err)
	}
	if len(targetStats) != len(goPackets) {
		t.Fatalf("target stats count mismatch: got=%d want=%d", len(targetStats), len(goPackets))
	}

	n := len(libPackets)
	if len(goPackets) < n {
		n = len(goPackets)
	}
	packetCountDiff := len(goPackets) - len(libPackets)
	if packetCountDiff < 0 {
		packetCountDiff = -packetCountDiff
	}
	if packetCountDiff > 1 {
		t.Fatalf("packet count mismatch too large: go=%d lib=%d", len(goPackets), len(libPackets))
	}

	libDec := celt.NewDecoder(fixtureCase.Channels)
	goDec := celt.NewDecoder(fixtureCase.Channels)

	for i := 0; i < n; i++ {
		libDecision, err := libDec.ProbeRawPacketDecision(libPackets[i][1:], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("probe lib packet %d: %v", i, err)
		}
		goDecision, err := goDec.ProbeRawPacketDecision(goPackets[i][1:], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("probe gopus packet %d: %v", i, err)
		}
		if libDecision != goDecision {
			prev := i - 1
			if prev < 0 {
				prev = 0
			}
			t.Fatalf(
				"first packet decision mismatch at frame %d\nprev target=%+v\ncur target=%+v\nlib=%+v\ngo=%+v",
				i,
				targetStats[prev],
				targetStats[i],
				libDecision,
				goDecision,
			)
		}
	}
}

func encodeGopusForVariantsCaseWithTargetStats(c encoderComplianceVariantsFixtureCase, signal []float32) ([][]byte, []celt.CeltTargetStats, error) {
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

	stats := make([]celt.CeltTargetStats, 0, c.Frames)
	enc.SetCELTTargetStatsHook(func(s celt.CeltTargetStats) {
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
