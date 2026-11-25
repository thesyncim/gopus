//go:build gopus_libopus_oracle

package gopus

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

const libopusDTXPacketOutputMagic = "GDTX"

var libopusDTXEmitPacketsHelper libopustest.HelperCache

func getLibopusDTXEmitPacketsHelperPath() (string, error) {
	return libopusDTXEmitPacketsHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "public DTX emit",
			OutputBase:  "gopus_libopus_dtx_emit_packets",
			SourceFile:  "libopus_dtx_emit_packets.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk", "src"},
		})
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
			assertAPIRateFloat32BitsExact(t, got, want, tc.name)
		})
	}
}

func makeCode1Dup(base []byte) []byte {
	if len(base) < 2 {
		return nil
	}
	toc := (base[0] &^ 0x03) | 0x01 // code 1
	frame := base[1:]
	out := make([]byte, 0, 1+2*len(frame))
	out = append(out, toc)
	out = append(out, frame...)
	out = append(out, frame...)
	return out
}

// appendOpusFrameLength appends the RFC 6716 §3.1 frame-length encoding of L
// (length = 4*secondByte + firstByte for the two-byte form).
func appendOpusFrameLength(dst []byte, l int) []byte {
	if l < 252 {
		return append(dst, byte(l))
	}
	first := 252 + ((l - 252) & 3)
	second := (l - first) / 4
	return append(dst, byte(first), byte(second))
}

// makeCode2LastDTX builds a code-2 packet whose first frame is the real frame
// and whose last (second) frame is empty -> inner DTX last frame.
func makeCode2LastDTX(base []byte) []byte {
	if len(base) < 2 {
		return nil
	}
	frame1 := base[1:]
	if len(frame1) > maxOpusFrameBytes {
		return nil
	}
	toc := (base[0] &^ 0x03) | 0x02 // code 2
	out := make([]byte, 0, 3+len(frame1))
	out = append(out, toc)
	out = appendOpusFrameLength(out, len(frame1)) // frame1 length
	out = append(out, frame1...)
	// frame2 implicit length = remaining = 0 bytes -> DTX last frame.
	return out
}

// makeCode2FirstDTX builds a code-2 packet whose first frame is empty (DTX) and
// whose last (second) frame is the real frame.
func makeCode2FirstDTX(base []byte) []byte {
	if len(base) < 2 {
		return nil
	}
	toc := (base[0] &^ 0x03) | 0x02 // code 2
	frame := base[1:]
	out := make([]byte, 0, 2+len(frame))
	out = append(out, toc)
	out = append(out, byte(0))  // frame1 length = 0 -> DTX
	out = append(out, frame...) // frame2 = remaining = real frame
	return out
}

// makeTOCOnlyDTX returns the 1-byte TOC-only DTX packet for base's TOC (code 0).
func makeTOCOnlyDTX(base []byte) []byte {
	if len(base) < 1 {
		return nil
	}
	return []byte{base[0] &^ 0x03} // code 0, no payload
}

func TestDecodeFinalRangePLCDTXMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	const sampleRate = 48000

	type modecase struct {
		name string
		make func(t *testing.T, channels int) []byte
	}
	modes := []modecase{
		{"silk_wb", encodeAPIRateSILKPacket},
		{"celt_fb", encodeAPIRateCELTPacket},
		{"hybrid_fb", encodeAPIRateHybridPacket},
	}

	for _, mc := range modes {
		for _, channels := range []int{1, 2} {
			name := mc.name + "_ch" + itoaSmall(channels)
			t.Run(name, func(t *testing.T) {
				src := mc.make(t, channels)
				if len(src) <= 2 {
					t.Fatalf("encoded %s packet too small (len=%d) to build multi-frame variants", mc.name, len(src))
				}

				tocDTX := makeTOCOnlyDTX(src)
				lastDTX := makeCode2LastDTX(src)
				firstDTX := makeCode2FirstDTX(src)
				code1 := makeCode1Dup(src)
				if tocDTX == nil || lastDTX == nil || firstDTX == nil || code1 == nil {
					t.Fatalf("could not construct multi-frame variants for %s (src len=%d)", mc.name, len(src))
				}

				// Requested frame_size (uniform for every step in the oracle's v6
				// protocol): large enough for the longest (two-frame) packet at
				// 48 kHz (2 x 20 ms = 1920 samples/channel) plus headroom. A NULL
				// PLC step conceals up to this duration on both sides.
				const reqFrame = 2880

				// normal -> PLC(NULL) -> TOC-only DTX -> recovery
				//        -> last-DTX multiframe -> recovery
				//        -> first-DTX multiframe -> CBR code-1 -> recovery
				steps := []libopusAPIRateDecodeStep{
					{packet: src},
					{}, // PLC: nil packet
					{packet: tocDTX},
					{packet: src},
					{packet: lastDTX},
					{packet: src},
					{packet: firstDTX},
					{packet: code1},
					{packet: src},
				}

				wantPCM, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, reqFrame, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate final range reference decode", err)
				}
				if len(ranges) != len(steps) {
					t.Fatalf("oracle returned %d ranges, want %d", len(ranges), len(steps))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				buf := make([]float32, reqFrame*channels)
				gotPCM := make([]float32, 0, len(wantPCM))

				labels := []string{
					"normal", "PLC(nil)", "DTX-TOC-only", "recovery",
					"multiframe-last-DTX", "recovery2",
					"multiframe-first-DTX", "CBR-code1-allnormal", "recovery3",
				}

				for i, s := range steps {
					clear(buf)
					var n int
					var derr error
					if s.packet == nil {
						n, derr = dec.Decode(nil, buf)
					} else {
						n, derr = dec.Decode(s.packet, buf)
					}
					if derr != nil {
						t.Fatalf("step %d (%s) gopus decode: %v", i, labels[i], derr)
					}
					gotPCM = append(gotPCM, buf[:n*channels]...)
					got := dec.FinalRange()
					want := ranges[i]
					if got != want {
						t.Fatalf("step %d (%s): FinalRange()=0x%08X want 0x%08X (len=%d)",
							i, labels[i], got, want, packetStepLen(s))
					}
				}
				assertAPIRateFloat32BitsExact(t, gotPCM, wantPCM, "PLC/DTX final-range sequence")

				// Sanity: the inner-DTX-last step must be 0 (regression for the
				// stale-range bug), and a normal recovery step must be non-zero.
				if ranges[4] != 0 {
					t.Fatalf("oracle ground truth changed: multiframe-last-DTX range=0x%08X want 0", ranges[4])
				}
				if ranges[3] == 0 {
					t.Fatalf("oracle ground truth changed: recovery range unexpectedly 0")
				}
			})
		}
	}
}

func packetStepLen(s libopusAPIRateDecodeStep) int {
	if s.packet == nil {
		return -1
	}
	return len(s.packet)
}
