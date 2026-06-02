package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// This file broadens decode-side libopus-oracle parity coverage across
// under-exercised configuration edges that the gopus *encoder* does not itself
// select: CBR streams, restricted-SILK/CELT applications, the full
// bandwidth/frame-size grid, and the boundary bitrates (6 kbps min, 510 kbps
// max). libopus encodes each stream (libopus_cbr_encode_packets.c), then the
// SAME packets are decoded both by libopus (opus_decode_float via
// libopus_refdecode_single.c) and by gopus. The two PCM streams are
// sample-aligned, so we gate on the trusted near-exact comparator
// (assertAPIRateQualityFloat32) which is byte-exact on amd64/CI and absorbs the
// documented darwin/arm64 1-ULP CELT/Hybrid float drift.
//
// These gates exercise the gopus decoder on the libopus output space, not just
// the subset gopus would itself produce, hardening decode-side production
// confidence across mode/bandwidth/bitrate/frame-size edges.

// libopus application codes for libopus_cbr_encode_packets.c.
const (
	cbrAppAudio          = uint32(0)
	cbrAppVoIP           = uint32(1)
	cbrAppRestrictedSILK = uint32(2)
	cbrAppRestrictedCELT = uint32(3)
)

// libopus bandwidth constants (opus_defines.h), used by the CBR helper.
const (
	cbrBWNarrowband    = uint32(1101)
	cbrBWMediumband    = uint32(1102)
	cbrBWWideband      = uint32(1103)
	cbrBWSuperwideband = uint32(1104)
	cbrBWFullband      = uint32(1105)
)

var libopusCBREncodeHelper libopustest.HelperCache

func getLibopusCBREncodeHelperPath() (string, error) {
	return libopusCBREncodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "cbr encode packets",
		OutputBase: "gopus_libopus_cbr_encode_packets",
		SourceFile: "libopus_cbr_encode_packets.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

type cbrEncodeConfig struct {
	app        uint32
	bandwidth  uint32
	channels   int
	bitrate    int
	frameSize  int // samples per frame at 48 kHz
	complexity int
	numFrames  int
}

// encodeLibopusCBRPackets drives libopus opus_encode_float() in CBR mode over a
// sequence of frames, returning the encoded packets. The encoder always runs at
// 48 kHz (gopus internal rate).
func encodeLibopusCBRPackets(cfg cbrEncodeConfig, pcm []float32) ([][]byte, error) {
	binPath, err := getLibopusCBREncodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GCBR", 1,
		cfg.app,
		cfg.bandwidth,
		uint32(cfg.channels),
		uint32(cfg.bitrate),
		uint32(cfg.frameSize),
		uint32(cfg.complexity),
		uint32(cfg.numFrames),
	)
	payload.Float32s(pcm...)
	reader, version, err := libopustest.NewOracleReaderVersion("cbr encode", "GCBO", mustRunHelper(binPath, payload.Bytes()))
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("cbr encode helper version=%d want 1", version)
	}
	count := reader.Count(-1)
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

func mustRunHelper(binPath string, input []byte) []byte {
	out, err := libopustest.RunHelper(binPath, input)
	if err != nil {
		panic(err)
	}
	return out
}

// configEdgesPCM produces a deterministic, seeded multi-tone + envelope signal
// suitable for exercising every coding mode. It is band-limited softly so SILK,
// Hybrid, and CELT all encode meaningful content.
func configEdgesPCM(frameSize, channels, numFrames int) []float32 {
	total := frameSize * channels * numFrames
	pcm := make([]float32, total)
	const sampleRate = 48000
	for f := 0; f < numFrames; f++ {
		for i := 0; i < frameSize; i++ {
			n := f*frameSize + i
			tt := float64(n) / float64(sampleRate)
			env := 0.7 + 0.3*math.Sin(2*math.Pi*0.9*tt)
			s := 0.0
			s += 0.30 * math.Sin(2*math.Pi*180*tt)
			s += 0.18 * math.Sin(2*math.Pi*523*tt+0.3)
			s += 0.10 * math.Sin(2*math.Pi*1200*tt+0.7)
			s += 0.05 * math.Sin(2*math.Pi*3300*tt+1.1)
			v := float32(env * s)
			for ch := 0; ch < channels; ch++ {
				// Slight inter-channel decorrelation for stereo coverage.
				pcm[(f*frameSize+i)*channels+ch] = v * (1.0 - 0.07*float32(ch))
			}
		}
	}
	return pcm
}

// decodeGopusFloat32Sequence decodes a packet sequence through a single gopus
// decoder instance (stateful), returning interleaved float32 PCM.
func decodeGopusFloat32Sequence(t *testing.T, sampleRate, channels, frameSize int, packets [][]byte) []float32 {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, 0, frameSize*channels*len(packets))
	buf := make([]float32, frameSize*channels)
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
		out = append(out, buf[:n*channels]...)
	}
	return out
}

// TestDecodeLibopusCBRConfigEdgesMatchesLibopus exercises the gopus decoder on
// CBR libopus-encoded streams across the bandwidth × frame-size × channels grid.
// CBR is a configuration the gopus encoder does not pick by default, so this
// covers decode of libopus output the encoder-driven gates never produce.
func TestDecodeLibopusCBRConfigEdgesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type bwCase struct {
		name string
		bw   uint32
		// frame sizes valid for the encoder bandwidth; libopus clamps SILK to
		// >=10 ms, so NB/MB/WB SILK use >=480 here, while FB CELT covers short
		// frames too.
		frameSizes []int
	}
	bwCases := []bwCase{
		{"nb", cbrBWNarrowband, []int{480, 960, 1920}},
		{"mb", cbrBWMediumband, []int{480, 960, 1920}},
		{"wb", cbrBWWideband, []int{480, 960, 1920, 2880}},
		{"swb", cbrBWSuperwideband, []int{480, 960, 1920}},
		{"fb", cbrBWFullband, []int{120, 240, 480, 960, 1920, 2880}},
	}

	for _, channels := range []int{1, 2} {
		for _, bc := range bwCases {
			for _, frameSize := range bc.frameSizes {
				name := fmt.Sprintf("ch%d_%s_fs%d", channels, bc.name, frameSize)
				t.Run(name, func(t *testing.T) {
					// Choose a moderate per-bandwidth bitrate so CBR fits the
					// frame budget at all sizes.
					bitrate := 48000
					if channels == 2 {
						bitrate = 96000
					}
					cfg := cbrEncodeConfig{
						app:        cbrAppAudio,
						bandwidth:  bc.bw,
						channels:   channels,
						bitrate:    bitrate,
						frameSize:  frameSize,
						complexity: 10,
						numFrames:  numFrames,
					}
					pcm := configEdgesPCM(frameSize, channels, numFrames)
					packets, err := encodeLibopusCBRPackets(cfg, pcm)
					if err != nil {
						libopustest.HelperUnavailable(t, "cbr encode", err)
					}
					if len(packets) == 0 {
						t.Fatalf("libopus produced no packets")
					}

					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "cbr reference decode", err)
					}
					got := decodeGopusFloat32Sequence(t, sampleRate, channels, frameSize, packets)
					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, name)
				})
			}
		}
	}
}

// TestDecodeLibopusBoundaryBitratesMatchesLibopus exercises the extreme bitrate
// edges: the 6 kbps minimum and 510 kbps maximum, across mode-selecting
// bandwidths and frame sizes. The libopus encoder picks SILK/Hybrid/CELT
// internally at these rates; the gopus decoder must track its output.
func TestDecodeLibopusBoundaryBitratesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type edge struct {
		name      string
		bw        uint32
		bitrate   int
		channels  int
		frameSize int
	}
	edges := []edge{
		// 6 kbps minimum: libopus forces SILK/NB-class at the floor.
		{"min6k_nb_mono_20ms", cbrBWNarrowband, 6000, 1, 960},
		{"min6k_wb_mono_20ms", cbrBWWideband, 6000, 1, 960},
		{"min6k_wb_mono_60ms", cbrBWWideband, 6000, 1, 2880},
		// 510 kbps maximum: libopus forces CELT/FB at the ceiling.
		{"max510k_fb_mono_20ms", cbrBWFullband, 510000, 1, 960},
		{"max510k_fb_stereo_20ms", cbrBWFullband, 510000, 2, 960},
		{"max510k_fb_stereo_5ms", cbrBWFullband, 510000, 2, 240},
		{"max510k_fb_stereo_2_5ms", cbrBWFullband, 510000, 2, 120},
		// Mode-selection boundary region: ~16-24 kbps WB toggles SILK<->Hybrid.
		{"bound16k_wb_mono_20ms", cbrBWWideband, 16000, 1, 960},
		{"bound24k_swb_stereo_20ms", cbrBWSuperwideband, 24000, 2, 960},
		{"bound64k_fb_stereo_20ms", cbrBWFullband, 64000, 2, 960},
	}

	for _, e := range edges {
		t.Run(e.name, func(t *testing.T) {
			cfg := cbrEncodeConfig{
				app:        cbrAppAudio,
				bandwidth:  e.bw,
				channels:   e.channels,
				bitrate:    e.bitrate,
				frameSize:  e.frameSize,
				complexity: 10,
				numFrames:  numFrames,
			}
			pcm := configEdgesPCM(e.frameSize, e.channels, numFrames)
			packets, err := encodeLibopusCBRPackets(cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr encode", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, e.channels, e.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, e.channels, e.frameSize, packets)
			assertAPIRateQualityFloat32(t, got, want, sampleRate, e.channels, e.name)
		})
	}
}

// transitionSegment describes one libopus-encoded run whose packets are
// concatenated into a single switching stream.
type transitionSegment struct {
	cfg    cbrEncodeConfig
	frames int
}

// buildTransitionStream encodes each segment as a separate libopus CBR run and
// concatenates the packets into one stateful stream. All segments must share the
// channel count (a single decoder instance is used end-to-end). Returns the
// packets and the max frame size across segments (the decoder output buffer
// size). The PCM is phase-continuous across segments so the switch points carry
// real signal.
func buildTransitionStream(t *testing.T, channels int, segments []transitionSegment) (packets [][]byte, maxFrameSize int) {
	t.Helper()
	globalFrame := 0
	for _, seg := range segments {
		if seg.cfg.channels != channels {
			t.Fatalf("segment channels=%d want %d", seg.cfg.channels, channels)
		}
		if seg.cfg.frameSize > maxFrameSize {
			maxFrameSize = seg.cfg.frameSize
		}
		cfg := seg.cfg
		cfg.numFrames = seg.frames
		// Generate phase-continuous PCM offset by the global frame index so the
		// signal flows across the switch boundary.
		pcm := configEdgesPCMOffset(cfg.frameSize, channels, seg.frames, globalFrame*cfg.frameSize)
		segPackets, err := encodeLibopusCBRPackets(cfg, pcm)
		if err != nil {
			libopustest.HelperUnavailable(t, "transition cbr encode", err)
		}
		packets = append(packets, segPackets...)
		globalFrame += seg.frames
	}
	return packets, maxFrameSize
}

// configEdgesPCMOffset is configEdgesPCM with a sample offset so that
// concatenated segments form a continuous waveform.
func configEdgesPCMOffset(frameSize, channels, numFrames, sampleOffset int) []float32 {
	total := frameSize * channels * numFrames
	pcm := make([]float32, total)
	const sampleRate = 48000
	for f := 0; f < numFrames; f++ {
		for i := 0; i < frameSize; i++ {
			n := sampleOffset + f*frameSize + i
			tt := float64(n) / float64(sampleRate)
			env := 0.7 + 0.3*math.Sin(2*math.Pi*0.9*tt)
			s := 0.0
			s += 0.30 * math.Sin(2*math.Pi*180*tt)
			s += 0.18 * math.Sin(2*math.Pi*523*tt+0.3)
			s += 0.10 * math.Sin(2*math.Pi*1200*tt+0.7)
			s += 0.05 * math.Sin(2*math.Pi*3300*tt+1.1)
			v := float32(env * s)
			for ch := 0; ch < channels; ch++ {
				pcm[(f*frameSize+i)*channels+ch] = v * (1.0 - 0.07*float32(ch))
			}
		}
	}
	return pcm
}

// TestDecodeLibopusModeTransitionsMatchesLibopus exercises within-stream
// transitions: the gopus decoder must track libopus across SILK<->Hybrid<->CELT
// mode switches, NB<->WB<->SWB<->FB bandwidth steps, and frame-size changes
// across consecutive packets. Each segment is a separate libopus CBR encode;
// concatenating their packets produces a real switching stream decoded through a
// single stateful decoder on both sides.
func TestDecodeLibopusModeTransitionsMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000

	const fs20 = 960 // 20 ms at 48 kHz
	seg := func(app, bw uint32, channels, bitrate, frameSize, frames int) transitionSegment {
		return transitionSegment{
			cfg: cbrEncodeConfig{
				app:        app,
				bandwidth:  bw,
				channels:   channels,
				bitrate:    bitrate,
				frameSize:  frameSize,
				complexity: 10,
			},
			frames: frames,
		}
	}

	type tcase struct {
		name     string
		channels int
		segments []transitionSegment
	}
	cases := []tcase{
		{
			// SILK -> Hybrid -> CELT mode walk (mono).
			name:     "silk_hybrid_celt_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppVoIP, cbrBWWideband, 1, 16000, fs20, 3),  // SILK WB
				seg(cbrAppAudio, cbrBWFullband, 1, 64000, fs20, 3), // Hybrid/CELT FB
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, fs20, 3),
			},
		},
		{
			// NB -> WB -> SWB -> FB bandwidth steps (mono).
			name:     "bandwidth_steps_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppAudio, cbrBWNarrowband, 1, 16000, fs20, 3),
				seg(cbrAppAudio, cbrBWWideband, 1, 32000, fs20, 3),
				seg(cbrAppAudio, cbrBWSuperwideband, 1, 48000, fs20, 3),
				seg(cbrAppAudio, cbrBWFullband, 1, 96000, fs20, 3),
			},
		},
		{
			// Frame-size changes across consecutive frames (FB CELT, mono):
			// 2.5 -> 5 -> 10 -> 20 -> 40 -> 60 ms.
			name:     "frame_size_walk_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 120, 4),  // 2.5 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 240, 4),  // 5 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 480, 4),  // 10 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 960, 4),  // 20 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 1920, 2), // 40 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 2880, 2), // 60 ms
			},
		},
		{
			// Stereo SILK -> CELT bandwidth+mode walk.
			name:     "silk_celt_stereo",
			channels: 2,
			segments: []transitionSegment{
				seg(cbrAppVoIP, cbrBWWideband, 2, 32000, fs20, 3),   // SILK WB stereo
				seg(cbrAppAudio, cbrBWFullband, 2, 128000, fs20, 3), // CELT/Hybrid FB stereo
				seg(cbrAppRestrictedCELT, cbrBWFullband, 2, 192000, fs20, 3),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packets, maxFrameSize := buildTransitionStream(t, tc.channels, tc.segments)
			if len(packets) == 0 {
				t.Fatalf("no packets produced")
			}
			// Decode through a single libopus decoder (stateful, max frame size).
			steps := make([]libopusAPIRateDecodeStep, len(packets))
			for i, pkt := range packets {
				steps[i] = libopusAPIRateDecodeStep{packet: pkt}
			}
			wantI16, err := decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, tc.channels, maxFrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "transition reference decode", err)
			}
			// Decode through a single gopus decoder (stateful).
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			gotI16 := make([]int16, 0, len(wantI16))
			buf := make([]int16, maxFrameSize*tc.channels)
			for i, pkt := range packets {
				n, err := dec.DecodeInt16(pkt, buf)
				if err != nil {
					t.Fatalf("DecodeInt16 packet %d: %v", i, err)
				}
				gotI16 = append(gotI16, buf[:n*tc.channels]...)
			}
			assertAPIRateQualityInt16(t, gotI16, wantI16, sampleRate, tc.channels, tc.name)
		})
	}
}

// TestDecodeLibopusRestrictedAppEdgesMatchesLibopus exercises decode of streams
// from the restricted-SILK and restricted-CELT applications, which lock the
// encoder into a single coding mode the auto encoder would not always pick.
func TestDecodeLibopusRestrictedAppEdgesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type rc struct {
		name      string
		app       uint32
		bw        uint32
		channels  int
		bitrate   int
		frameSize int
	}
	cases := []rc{
		{"rsilk_nb_mono_20ms", cbrAppRestrictedSILK, cbrBWNarrowband, 1, 24000, 960},
		{"rsilk_wb_stereo_20ms", cbrAppRestrictedSILK, cbrBWWideband, 2, 48000, 960},
		{"rsilk_mb_mono_40ms", cbrAppRestrictedSILK, cbrBWMediumband, 1, 24000, 1920},
		{"rcelt_fb_mono_10ms", cbrAppRestrictedCELT, cbrBWFullband, 1, 64000, 480},
		{"rcelt_fb_stereo_20ms", cbrAppRestrictedCELT, cbrBWFullband, 2, 128000, 960},
		{"rcelt_fb_mono_2_5ms", cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 120},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := cbrEncodeConfig{
				app:        c.app,
				bandwidth:  c.bw,
				channels:   c.channels,
				bitrate:    c.bitrate,
				frameSize:  c.frameSize,
				complexity: 10,
				numFrames:  numFrames,
			}
			pcm := configEdgesPCM(c.frameSize, c.channels, numFrames)
			packets, err := encodeLibopusCBRPackets(cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr encode", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, c.channels, c.frameSize, packets)
			assertAPIRateQualityFloat32(t, got, want, sampleRate, c.channels, c.name)
		})
	}
}
