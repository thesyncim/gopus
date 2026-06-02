package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// SILK low-bitrate DTX/loss-concealment shared-path parity.
//
// This regression locks in the classification of the SILK-MB/NB mono 10 ms
// 8000 bps "shared float path diverges" finding surfaced by the fixed-point
// decode fuzz harness (decode_differential_fuzz_fixedpoint_test.go's
// fixedPointSpecificDivergence), and pins the parts of that path that ARE exact
// so any regression in them hard-fails.
//
// Root cause (verified): at SILK low bitrate the encoder emits code-3 padded
// packets whose single inner frame is 1 byte (an empty/DTX frame). libopus
// opus_decode_frame routes any per-frame len<=1 to data=NULL → lost_flag=1
// (src/opus_decoder.c:315-321, 469), i.e. SILK loss concealment. gopus mirrors
// that routing (decoder_opus_frame.go's len(data)<=1 → PLC). Both therefore run
// silk_PLC_conceal for that frame. The two findings:
//
//  1. ACTIVE SILK decode is bit-exact. Every frame up to the first concealed
//     (DTX) frame matches the stateful libopus FLOAT oracle sample-for-sample,
//     and the entropy coder (per-packet final range) is bit-identical on every
//     active frame. So NLSF/LPC/LTP/gains/resample are correct (asserted here).
//
//  2. The SILK loss-concealment SYNTHESIS is bit-exact too. silk_PLC_conceal
//     (silk/PLC.c) runs at the SILK frame's actual LPC order (10 for NB/MB, 16
//     for WB) and reads the exact integer sLPC_Q14_buf history, so the concealed
//     frame AND the following active frame match the libopus float oracle
//     sample-for-sample. This pins the regression where the mono PLC path used a
//     stale Decoder-level LPC order (defaulting to 16) and so discarded the
//     integer LPC history in favour of a float-derived fallback, corrupting
//     unvoiced concealment and leaving wrong cross-frame state.
//
// Both findings are hard-asserted on every architecture (the SILK PLC path is
// integer Q-domain and bit-reproducible).
func TestSILKLowBitrateDTXConcealmentSharedPathParity(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const frames = 16

	type spec struct {
		name     string
		bw       Bandwidth
		frameMs  int
		bitrate  int
		channels int
		content  string
	}
	cases := []spec{
		{"silk_mb_ch1_10ms_8000bps_chirp", BandwidthMediumband, 10, 8000, 1, "chirp"},
		{"silk_nb_ch1_10ms_8000bps_chirp", BandwidthNarrowband, 10, 8000, 1, "chirp"},
		{"silk_mb_ch1_10ms_8000bps_ramp", BandwidthMediumband, 10, 8000, 1, "ramp"},
		{"silk_nb_ch1_10ms_8000bps_ramp", BandwidthNarrowband, 10, 8000, 1, "ramp"},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			packets, frameSize := encodeSILKConcealmentProbePackets(t, c.bw, c.frameMs, c.bitrate, c.channels, c.content, frames)
			if len(packets) == 0 {
				t.Skip("encoder produced no packets")
			}

			firstPLC := -1
			for i, p := range packets {
				if silkPacketInnerFrameIsConceal(p) {
					firstPLC = i
					break
				}
			}
			if firstPLC < 0 {
				t.Skipf("%s: no DTX/concealment frame in stream — not the target edge", c.name)
			}

			// gopus stateful FLOAT decode + per-packet final range.
			gdec, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}
			gPCM := make([]float32, 0, len(packets)*frameSize*c.channels)
			gRanges := make([]uint32, 0, len(packets))
			buf := make([]float32, frameSize*c.channels)
			for _, p := range packets {
				n, derr := gdec.Decode(p, buf)
				if derr != nil {
					t.Fatalf("gopus decode: %v", derr)
				}
				gPCM = append(gPCM, buf[:n*c.channels]...)
				gRanges = append(gRanges, gdec.FinalRange())
			}

			// libopus stateful FLOAT decode + per-packet final range.
			steps := make([]libopusAPIRateDecodeStep, len(packets))
			for i, p := range packets {
				steps[i] = libopusAPIRateDecodeStep{packet: p, frameSize: frameSize}
			}
			oPCM, oRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, c.channels, frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "stateful float reference", err)
				return
			}
			if len(gPCM) != len(oPCM) {
				t.Fatalf("%s: sample count gopus=%d libopus=%d", c.name, len(gPCM), len(oPCM))
			}

			// Finding (1) HARD assertion: every ACTIVE frame strictly before the
			// first concealment frame must be bit-exact, and its per-packet final
			// range bit-identical. The whole path here — SILK active decode, the
			// silk_PLC_conceal synthesis, and the native->API-rate resampler — is
			// integer Q-domain (no CELT/Hybrid float FMA), so it is bit-reproducible
			// on every architecture, including darwin/arm64. The tolerance is
			// therefore a strict 0 on all arches, not the few-LSB FMA budget the
			// shared float path carries.
			const activeTol = float32(0)
			activeLimit := firstPLC * frameSize * c.channels
			var worstActive float32
			for i := 0; i < activeLimit; i++ {
				if d := absF32(gPCM[i] - oPCM[i]); d > worstActive {
					worstActive = d
				}
			}
			if worstActive > activeTol {
				t.Errorf("%s: ACTIVE SILK decode diverged before any concealment frame: worst|Δ|=%g > %g (active SILK must be bit-exact)", c.name, worstActive, activeTol)
			}
			for i := 0; i < firstPLC; i++ {
				if i < len(oRanges) && gRanges[i] != oRanges[i] {
					t.Errorf("%s: ACTIVE frame %d final range mismatch gopus=0x%08x libopus=0x%08x (entropy decode must match on active frames)", c.name, i, gRanges[i], oRanges[i])
				}
			}

			// Finding (1b): the first concealment frame's LEADING samples are
			// bit-exact (the LPC/gain/pitch state handed to silk_PLC_conceal is
			// correct). Assert at least a handful match to lock the state handoff.
			lo := firstPLC * frameSize * c.channels
			leadExact := 0
			for lo+leadExact < len(gPCM) && leadExact < frameSize*c.channels {
				if gPCM[lo+leadExact] != oPCM[lo+leadExact] {
					break
				}
				leadExact++
			}
			if leadExact < 8 {
				t.Errorf("%s: concealment frame leading samples not bit-exact (leadExact=%d); the PLC state handoff (sLPC/prevGain/lag) regressed", c.name, leadExact)
			}

			// Finding (2) HARD assertion: the concealment synthesis and every
			// active frame that follows it are bit-exact vs the libopus float
			// oracle. The SILK PLC path is integer Q-domain, so the concealed frame
			// and the recovered active frames match the oracle sample-for-sample
			// (strict 0, every architecture) once the PLC runs at the frame's true
			// LPC order.
			var worstFull float32
			worstIdx := 0
			for i := range gPCM {
				if d := absF32(gPCM[i] - oPCM[i]); d > worstFull {
					worstFull = d
					worstIdx = i
				}
			}
			if worstFull > activeTol {
				t.Errorf("%s: SILK PLC concealment diverged from libopus: worst|Δ|=%g > %g @sample%d (frame %d); the concealed frame + following active frames must be bit-exact (silk/PLC.c silk_PLC_conceal)",
					c.name, worstFull, activeTol, worstIdx, worstIdx/(frameSize*c.channels))
			}
		})
	}
}

// encodeSILKConcealmentProbePackets encodes a deterministic SILK-only CBR stream
// at the given bandwidth/duration/bitrate. Low SILK bitrates emit code-3 padded
// packets whose inner frame is 1 byte (DTX), which the decoder routes to SILK
// loss concealment — the edge this test targets.
func encodeSILKConcealmentProbePackets(t *testing.T, bw Bandwidth, frameMs, bitrate, channels int, content string, frames int) ([][]byte, int) {
	t.Helper()
	frameSize := frameMs * 48 // 48 kHz API rate
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("create encoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("set frame size: %v", err)
	}
	if err := enc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("force SILK: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Fatalf("set bandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("set bitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatalf("set CBR: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("force stereo: %v", err)
		}
	}

	packet := make([]byte, 4000)
	packets := make([][]byte, 0, frames)
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			n := f*frameSize + i
			tt := float64(n) / 48000.0
			var s float64
			switch content {
			case "chirp":
				f0 := 100.0 + 2000.0*float64(n%4000)/4000.0
				s = 0.4 * math.Sin(2*math.Pi*f0*tt)
			case "ramp":
				s = float64((n*73)%20000-10000) / 24000.0
			default:
				s = 0.35 * math.Sin(2*math.Pi*220*tt)
			}
			for ch := 0; ch < channels; ch++ {
				pcm[i*channels+ch] = float32(s)
			}
		}
		nn, eerr := enc.Encode(pcm, packet)
		if eerr != nil {
			t.Fatalf("encode frame %d: %v", f, eerr)
		}
		if nn > 0 {
			packets = append(packets, append([]byte(nil), packet[:nn]...))
		}
	}
	return packets, frameSize
}

// silkPacketInnerFrameIsConceal reports whether a code-3 single-frame Opus packet
// carries a <=1-byte inner frame, which both libopus and gopus route to loss
// concealment (per-frame len<=1 → PLC). This is the encoder's CBR-padded DTX
// shape at low SILK bitrate.
func silkPacketInnerFrameIsConceal(p []byte) bool {
	if len(p) < 2 || (p[0]&0x03) != 3 {
		return false
	}
	m := int(p[1] & 0x3F)
	if m <= 0 {
		return false
	}
	hasPadding := (p[1] & 0x40) != 0
	off := 2
	padding := 0
	if hasPadding {
		for off < len(p) {
			pb := int(p[off])
			off++
			if pb == 255 {
				padding += 254
			} else {
				padding += pb
				break
			}
		}
	}
	frameDataLen := len(p) - off - padding
	if frameDataLen < 0 {
		return false
	}
	return frameDataLen/m <= 1
}
