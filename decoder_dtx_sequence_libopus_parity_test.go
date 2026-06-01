//go:build gopus_libopus_oracle

package gopus

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Decode-side DTX-sequence parity: libopus encodes a speech -> silence -> speech
// stream with OPUS_SET_DTX(1), emitting the TOC-only (1/2-byte) DTX packets that
// the gopus encoder also produces but that the decode-side oracle gates do not
// otherwise cover. The SAME packet sequence (including DTX packets) is decoded
// through both libopus (opus_decode_float) and gopus, sample-aligned, and gated
// on the trusted near-exact comparator. This hardens the decoder's handling of
// DTX TOC-only packets and the silence/CNG runs they drive.
//
// The DTX emit helper needs src/opus_private.h (MODE_* / OPUS_SET_FORCE_MODE),
// so it is built against the DRED reference tree like the FEC emit helper and is
// gated behind the gopus_libopus_oracle build tag.

const libopusDTXPacketOutputMagic = "GDTX"

var libopusDTXEmitPacketsHelper libopustest.HelperCache

func getLibopusDTXEmitPacketsHelperPath() (string, error) {
	return libopusDTXEmitPacketsHelper.Path(func() (string, error) {
		repoRoot, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		return libopustest.BuildDREDHelper(repoRoot, "libopus_dtx_emit_packets.c", "gopus_libopus_dtx_emit_packets", true)
	})
}

type libopusDTXConfig struct {
	FrameSize   int
	Channels    int
	Bitrate     int
	Bandwidth   string // "nb","mb","wb","swb","fb"
	Mode        string // "silk","hybrid","celt"
	Application string // "audio","voip","rsilk"
	CBR         bool
}

// emitLibopusDTXPackets runs the libopus DTX encoder over the supplied float32
// PCM (interleaved) and returns every emitted packet, including TOC-only DTX
// packets.
func emitLibopusDTXPackets(cfg libopusDTXConfig, pcm []float32) ([][]byte, error) {
	binPath, err := getLibopusDTXEmitPacketsHelperPath()
	if err != nil {
		return nil, err
	}
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = 24000
	}
	if cfg.Bandwidth == "" {
		cfg.Bandwidth = "wb"
	}
	if cfg.Mode == "" {
		cfg.Mode = "silk"
	}
	if cfg.Application == "" {
		cfg.Application = "audio"
	}
	maxFrames := len(pcm) / (cfg.FrameSize * cfg.Channels)
	env := []string{
		fmt.Sprintf("GOPUS_DTX_FRAME_SIZE=%d", cfg.FrameSize),
		fmt.Sprintf("GOPUS_DTX_CHANNELS=%d", cfg.Channels),
		fmt.Sprintf("GOPUS_DTX_BITRATE=%d", cfg.Bitrate),
		fmt.Sprintf("GOPUS_DTX_BANDWIDTH=%s", cfg.Bandwidth),
		fmt.Sprintf("GOPUS_DTX_MODE=%s", cfg.Mode),
		fmt.Sprintf("GOPUS_DTX_APPLICATION=%s", cfg.Application),
		fmt.Sprintf("GOPUS_DTX_MAX_FRAMES=%d", maxFrames),
		"GOPUS_DTX_PCM_STDIN=1",
	}
	if cfg.CBR {
		env = append(env, "GOPUS_DTX_CBR=1")
	}
	out, err := libopustest.RunHelperEnv(binPath, fecPCMInputLE(pcm), env)
	if err != nil {
		return nil, fmt.Errorf("run dtx emit helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderVersion("dtx emit", libopusDTXPacketOutputMagic, out)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("dtx emit helper version=%d want 1", version)
	}
	frameSize := int(reader.U32())
	channels := int(reader.U32())
	count := int(reader.U32())
	if frameSize != cfg.FrameSize || channels != cfg.Channels {
		return nil, fmt.Errorf("dtx emit header frameSize=%d channels=%d want %d/%d", frameSize, channels, cfg.FrameSize, cfg.Channels)
	}
	packets := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		n := int(reader.U32())
		packets = append(packets, append([]byte(nil), reader.Bytes(n)...))
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return packets, nil
}

// dtxSpeechSilencePCM builds a deterministic speech -> silence -> speech signal:
// a voiced multi-tone burst, a long silent run (to trigger DTX), then voiced
// again. The silence run is long enough to cross libopus's
// NB_SPEECH_FRAMES_BEFORE_DTX / MAX_CONSECUTIVE_DTX thresholds.
func dtxSpeechSilencePCM(frameSize, channels, speechFrames, silenceFrames int) []float32 {
	const sampleRate = 48000
	totalFrames := speechFrames + silenceFrames + speechFrames
	pcm := make([]float32, frameSize*channels*totalFrames)
	voiced := func(globalSample int) float32 {
		tt := float64(globalSample) / float64(sampleRate)
		env := 0.8 + 0.2*math.Sin(2*math.Pi*1.1*tt)
		s := 0.32*math.Sin(2*math.Pi*150*tt) +
			0.18*math.Sin(2*math.Pi*300*tt+0.2) +
			0.09*math.Sin(2*math.Pi*600*tt+0.5)
		return float32(env * s)
	}
	for f := 0; f < totalFrames; f++ {
		isSpeech := f < speechFrames || f >= speechFrames+silenceFrames
		for i := 0; i < frameSize; i++ {
			var v float32
			if isSpeech {
				v = voiced(f*frameSize + i)
			}
			for ch := 0; ch < channels; ch++ {
				pcm[(f*frameSize+i)*channels+ch] = v
			}
		}
	}
	return pcm
}

// TestDecodeLibopusDTXSequenceMatchesLibopus exercises the gopus decoder on a
// libopus DTX stream (speech -> silence -> speech) across SILK and Hybrid modes,
// mono and stereo. The decoded PCM is compared sample-for-sample against the
// libopus decode of the identical packet sequence.
func TestDecodeLibopusDTXSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000

	type tcase struct {
		name     string
		cfg      libopusDTXConfig
		channels int
	}
	cases := []tcase{
		{
			name:     "silk_wb_mono_20ms",
			channels: 1,
			cfg:      libopusDTXConfig{FrameSize: 960, Channels: 1, Bitrate: 24000, Bandwidth: "wb", Mode: "silk", Application: "voip"},
		},
		{
			name:     "silk_nb_mono_20ms",
			channels: 1,
			cfg:      libopusDTXConfig{FrameSize: 960, Channels: 1, Bitrate: 16000, Bandwidth: "nb", Mode: "silk", Application: "voip"},
		},
		{
			name:     "silk_wb_stereo_20ms",
			channels: 2,
			cfg:      libopusDTXConfig{FrameSize: 960, Channels: 2, Bitrate: 32000, Bandwidth: "wb", Mode: "silk", Application: "voip"},
		},
		{
			name:     "hybrid_fb_mono_20ms",
			channels: 1,
			cfg:      libopusDTXConfig{FrameSize: 960, Channels: 1, Bitrate: 48000, Bandwidth: "fb", Mode: "hybrid", Application: "audio"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 6 speech frames, 12 silent frames (long enough to cross the DTX
			// cadence thresholds), then 6 speech frames again.
			pcm := dtxSpeechSilencePCM(tc.cfg.FrameSize, tc.channels, 6, 12)
			packets, err := emitLibopusDTXPackets(tc.cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "dtx emit", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			// Confirm the stream actually contains TOC-only DTX packets, so the
			// gate is exercising the DTX path and not a plain active stream.
			dtxCount := 0
			for _, p := range packets {
				if len(p) <= 2 {
					dtxCount++
				}
			}
			if dtxCount == 0 {
				t.Fatalf("%s: expected DTX TOC-only packets, found none in %d packets", tc.name, len(packets))
			}
			t.Logf("%s: %d packets, %d DTX TOC-only", tc.name, len(packets), dtxCount)

			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, tc.channels, tc.cfg.FrameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "dtx reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, tc.channels, tc.cfg.FrameSize, packets)
			assertAPIRateQualityFloat32(t, got, want, sampleRate, tc.channels, tc.name)
		})
	}
}
