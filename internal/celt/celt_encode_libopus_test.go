package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// libopusCELTEncodeCase drives a full CELT-codec encode through both gopus'
// standalone CELT encoder (with the top-level dc_reject / delay-compensation /
// lsb-depth pre-processing disabled so only the codec runs) and libopus'
// internal celt_encode_with_ec(). The produced packet bytes must match exactly.
//
// Reference: libopus celt/celt_encoder.c celt_encode_with_ec(); the encoder is
// configured via celt_encoder_init(48000) exactly as src/opus_encoder.c does.
type libopusCELTEncodeCase struct {
	name       string
	channels   int
	frameSize  int
	bitrate    int32
	complexity int32
	// maxPayloadBytes, when > 0, caps the CBR payload (mirrors libopus
	// nbCompressedBytes); used to exercise the low-budget transient_got_disabled
	// path. 0 means "derive from bitrate".
	maxPayloadBytes int
	pcm             []float32
}

var libopusCELTEncodeHelper libopustest.HelperCache

func getLibopusCELTEncodeHelperPath() (string, error) {
	return libopusCELTEncodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "celt encode",
		OutputBase:  "gopus_libopus_celt_encode",
		SourceFile:  "libopus_celt_encode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// targetBytesForCase mirrors Encoder.cbrPayloadBytes for the chosen config so
// the libopus side receives the identical nbCompressedBytes budget.
func targetBytesForCase(tc libopusCELTEncodeCase) int {
	enc := newCELTEncodeCaseEncoder(tc)
	return enc.cbrPayloadBytes(tc.frameSize)
}

func newCELTEncodeCaseEncoder(tc libopusCELTEncodeCase) *Encoder {
	enc := NewEncoder(tc.channels)
	// Restrict to the pure CELT codec: disable the top-level Opus
	// preprocessing stages that celt_encode_with_ec() does not perform.
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetDelayCompensationEnabled(false)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	enc.SetComplexity(int(tc.complexity))
	enc.SetBitrate(int(tc.bitrate))
	if tc.maxPayloadBytes > 0 {
		enc.SetMaxPayloadBytes(tc.maxPayloadBytes)
	}
	return enc
}

func probeLibopusCELTEncode(cases []libopusCELTEncodeCase) ([][]byte, error) {
	binPath, err := getLibopusCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GCEI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(targetBytesForCase(tc)))
		payload.I32(tc.bitrate)
		payload.I32(tc.complexity)
		for _, sample := range tc.pcm {
			payload.Float32(sample)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt encode", "GCEO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]byte, count)
	for i := range count {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestCELTEncodeMatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEncodeCase{
		{
			name:       "mono_20ms_64k_tone",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 960, 0.0),
		},
		{
			name:       "mono_20ms_64k_phase",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 960, 0.7),
		},
		{
			name:       "stereo_20ms_128k",
			channels:   2,
			frameSize:  960,
			bitrate:    128000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(2, 960, 0.3),
		},
		{
			name:       "mono_10ms_96k",
			channels:   1,
			frameSize:  480,
			bitrate:    96000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 480, 1.1),
		},
		{
			name:       "mono_5ms_64k",
			channels:   1,
			frameSize:  240,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 240, 0.2),
		},
		{
			name:       "mono_2_5ms_64k",
			channels:   1,
			frameSize:  120,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 120, 0.9),
		},
		{
			name:       "mono_20ms_transient",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeTransientPCM(1, 960),
		},
		{
			name:       "stereo_20ms_transient_320k",
			channels:   2,
			frameSize:  960,
			bitrate:    320000,
			complexity: 10,
			pcm:        celtEncodeTransientPCM(2, 960),
		},
		{
			name:       "mono_20ms_noise_low_bitrate",
			channels:   1,
			frameSize:  960,
			bitrate:    16000,
			complexity: 10,
			pcm:        celtEncodeNoisePCM(1, 960, 12345),
		},
		{
			name:       "stereo_20ms_noise_510k",
			channels:   2,
			frameSize:  960,
			bitrate:    510000,
			complexity: 10,
			pcm:        celtEncodeNoisePCM(2, 960, 0xC0FFEE),
		},
		{
			name:       "mono_20ms_complexity5",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 5,
			pcm:        celtEncodeOraclePCM(1, 960, 0.55),
		},
		{
			name:       "mono_20ms_complexity0",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 0,
			pcm:        celtEncodeOraclePCM(1, 960, 0.55),
		},
		{
			// Mirrors TestEncodeFrameBudgetDisabledTransientAdvancesConsecTransient:
			// complexity 0, 2-byte budget, 440 Hz sine onset from silence.
			name:            "mono_20ms_complexity0_budget2_sine",
			channels:        1,
			frameSize:       960,
			bitrate:         64000,
			complexity:      0,
			maxPayloadBytes: 2,
			pcm:             float32Slice(generateSineWave(440.0, 960)),
		},
	}
	want, err := probeLibopusCELTEncode(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt encode", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := newCELTEncodeCaseEncoder(tc)
			got, err := enc.EncodeFrame(append([]float32(nil), tc.pcm...), tc.frameSize)
			if err != nil {
				t.Fatalf("gopus EncodeFrame: %v", err)
			}
			ref := want[i]
			if len(got) != len(ref) {
				t.Fatalf("packet length=%d want %d", len(got), len(ref))
			}
			for j := range got {
				if got[j] != ref[j] {
					t.Fatalf("packet byte[%d]=0x%02x want 0x%02x (first diff)", j, got[j], ref[j])
				}
			}
		})
	}
}

// TestCELTEncodeMultiFrameMatchesLibopusC drives several consecutive frames
// through one encoder instance on each side, verifying inter-frame state
// (energy prediction, prefilter, preemphasis memory, VBR/CVBR reservoir)
// stays byte-exact. The libopus side encodes the frames sequentially with a
// single celt_encode_with_ec() encoder (see libopus_celt_encode_info.c, which
// resets per case; here we use one case per frame but a persistent gopus
// encoder, and compare each frame against an independently-seeded libopus
// encoder driven through the multi-frame helper).
func TestCELTEncodeMultiFrameMatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 1
		frameSize  = 960
		bitrate    = 64000
		complexity = 10
		nFrames    = 6
	)
	frames := make([][]float32, nFrames)
	for f := range nFrames {
		frames[f] = celtEncodeOraclePCM(channels, frameSize, 0.21*float64(f))
	}
	want, err := probeLibopusCELTEncodeStream(channels, frameSize, bitrate, complexity, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt encode stream", err)
	}
	tc := libopusCELTEncodeCase{channels: channels, frameSize: frameSize, bitrate: bitrate, complexity: complexity}
	enc := newCELTEncodeCaseEncoder(tc)
	for f := range nFrames {
		got, err := enc.EncodeFrame(append([]float32(nil), frames[f]...), frameSize)
		if err != nil {
			t.Fatalf("frame %d EncodeFrame: %v", f, err)
		}
		ref := want[f]
		if len(got) != len(ref) {
			t.Fatalf("frame %d packet length=%d want %d", f, len(got), len(ref))
		}
		for j := range got {
			if got[j] != ref[j] {
				t.Fatalf("frame %d packet byte[%d]=0x%02x want 0x%02x", f, j, got[j], ref[j])
			}
		}
	}
}

// probeLibopusCELTEncodeStream encodes a sequence of frames through a single
// libopus encoder (no per-frame reset) so inter-frame state is exercised.
func probeLibopusCELTEncodeStream(channels, frameSize int, bitrate, complexity int32, frames [][]float32) ([][]byte, error) {
	binPath, err := getLibopusCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}
	tc := libopusCELTEncodeCase{channels: channels, frameSize: frameSize, bitrate: bitrate, complexity: complexity}
	target := targetBytesForCase(tc)
	payload := libopustest.NewOraclePayloadVersion("GCSI", 1, uint32(len(frames)))
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(target))
	payload.I32(bitrate)
	payload.I32(complexity)
	for _, frame := range frames {
		for _, sample := range frame {
			payload.Float32(sample)
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt encode stream", "GCSO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(frames))
	out := make([][]byte, count)
	for i := range count {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func celtEncodeOraclePCM(channels, frames int, phase float64) []float32 {
	pcm := make([]float32, frames*channels)
	for i := range frames {
		tm := float64(i) * 0.013
		l := 0.42*math.Sin(2*math.Pi*440*float64(i)/48000+phase) +
			0.09*math.Cos(0.31*tm) + 0.03*math.Sin(2*math.Pi*1830*float64(i)/48000)
		pcm[i*channels] = float32(l)
		if channels == 2 {
			r := 0.35*math.Sin(2*math.Pi*523*float64(i)/48000+0.4) -
				0.07*math.Cos(0.19*tm+phase)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// celtEncodeTransientPCM builds a quiet-then-burst signal to exercise the
// transient analysis / short-block MDCT path.
func celtEncodeTransientPCM(channels, frames int) []float32 {
	pcm := make([]float32, frames*channels)
	for i := range frames {
		var amp float64
		if i >= frames/2 && i < frames/2+40 {
			amp = 0.8
		} else {
			amp = 0.02
		}
		l := amp * math.Sin(2*math.Pi*1000*float64(i)/48000)
		pcm[i*channels] = float32(l)
		if channels == 2 {
			r := amp * math.Sin(2*math.Pi*1500*float64(i)/48000+0.3)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// celtEncodeNoisePCM builds a deterministic pseudo-random noise signal.
func celtEncodeNoisePCM(channels, frames int, seed uint32) []float32 {
	pcm := make([]float32, frames*channels)
	state := seed
	next := func() float32 {
		state = state*1664525 + 1013904223
		return float32(int32(state)) / float32(math.MaxInt32) * 0.5
	}
	for i := 0; i < frames*channels; i++ {
		pcm[i] = next()
	}
	return pcm
}
