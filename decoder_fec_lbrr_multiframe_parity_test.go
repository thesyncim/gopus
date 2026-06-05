package gopus

// Multi-frame SILK in-band FEC (LBRR) recovery parity tests.
//
// A 40 ms / 60 ms SILK packet carries 2 / 3 internal 20 ms LBRR sub-frames whose
// presence is signalled by the per-packet LBRR_flags symbol. The symbol can be
// PARTIAL (e.g. 0b110 = frames 0,1 carry LBRR, frame 2 does not), so a single FEC
// (decode_fec=1) recovery decode mixes real LBRR sub-frames with concealed
// (silk_PLC) sub-frames in one packet.
//
// Regression: the per-sub-frame concealment of a no-LBRR sub-frame must not
// corrupt the already-decoded LBRR output of earlier sub-frames in the same
// packet. The FEC-recovered frame must be bit-exact vs the libopus
// opus_decode(decode_fec=1) oracle (both per-sample and final-range), because
// libopus' LBRR decode is fully deterministic.
//
// Reference: libopus silk/dec_API.c (silk_Decode FLAG_DECODE_LBRR loop),
// silk/decode_frame.c (per-sub-frame silk_PLC for absent LBRR),
// src/opus_decoder.c (the FEC do/while that drives one silk_Decode per 20 ms).

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// fecMultiframeStereoPuregoTol bounds the amd64 pure-Go float-output drift of a
// 40/60 ms stereo SILK LBRR recovery vs the scalar libopus reference. The decode
// coding-state is bit-exact (final-range matches); only the SILK stereo MS->LR
// unmix + synthesis float ops round a few LSB differently from gcc's scalar C on
// the Go amd64 backend, accumulating to ~0.001 (~33/32768). The arm64 pure-Go
// build is bit-exact, so this budget applies only to amd64 pure-Go. It is three
// orders of magnitude below a real LBRR desync (~1.0-2.0).
const fecMultiframeStereoPuregoTol = 2.0 / 1000.0

// TestDecodeWithFECMultiFrameSILKMatchesLibopus encodes 40 ms and 60 ms SILK
// streams (NB/MB/WB) with FEC and a bursty (variable speech-activity) signal so
// the encoder emits partial multi-frame LBRR symbols, then FEC-recovers each
// LBRR-carrying packet and asserts bit-exact recovery vs libopus.
//
// The recovery is driven WITHOUT a preceding packet-loss-concealment step
// (decode the prior packet normally, then decode_fec=1 on the recovery packet).
// That isolates the LBRR multi-frame decode itself: with no upstream PLC
// divergence the recovered frame must be sample-exact for every bandwidth.
func TestDecodeWithFECMultiFrameSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	cases := []struct {
		name      string
		frameSize int // at 48 kHz: 1920 = 40 ms, 2880 = 60 ms
		bw        Bandwidth
		decRate   int // decoder API rate (native rate keeps the check resampler-noise free)
		channels  int
	}{
		{"NB_40ms", 1920, BandwidthNarrowband, 8000, 1},
		{"MB_40ms", 1920, BandwidthMediumband, 12000, 1},
		{"WB_40ms", 1920, BandwidthWideband, 16000, 1},
		{"NB_60ms", 2880, BandwidthNarrowband, 8000, 1},
		{"MB_60ms", 2880, BandwidthMediumband, 12000, 1},
		{"WB_60ms", 2880, BandwidthWideband, 16000, 1},
		{"WB_60ms_48k", 2880, BandwidthWideband, 48000, 1},
		{"WB_60ms_stereo", 2880, BandwidthWideband, 16000, 2},
		{"WB_40ms_stereo", 1920, BandwidthWideband, 16000, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channels := tc.channels
			packets := encodeFECBurstyStreamForTest(t, tc.bw, 24000, channels, tc.frameSize, 40)

			fs, err := packetSamplesAtRate(packets[0], tc.decRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			// Collect every LBRR-carrying recovery packet (index >= 2 so the
			// decoder is warmed up) and verify each as an FEC recovery step.
			recovered := 0
			partial := 0
			for r := 2; r < len(packets); r++ {
				if !packetHasInBandFEC(t, packets[r]) {
					continue
				}

				// Plan: decode packets[0..r-1] normally, then FEC-recover packets[r].
				steps := make([]libopusAPIRateDecodeStep, 0, r+1)
				for i := 0; i < r; i++ {
					steps = append(steps, libopusAPIRateDecodeStep{packet: packets[i]})
				}
				steps = append(steps, libopusAPIRateDecodeStep{packet: packets[r], fec: true})
				fecIdx := len(steps) - 1

				want, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(tc.decRate, channels, fs, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "multi-frame FEC reference", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(tc.decRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				buf := make([]float32, fs*channels)
				got := make([]float32, 0, len(want))
				var gotRange uint32
				fecStart := 0
				for i, s := range steps {
					if i == fecIdx {
						fecStart = len(got)
					}
					var n int
					var de error
					if s.fec {
						n, de = dec.DecodeWithFEC(s.packet, buf, true)
					} else {
						n, de = dec.Decode(s.packet, buf)
					}
					if de != nil {
						t.Fatalf("recovery packet %d step %d: %v", r, i, de)
					}
					got = append(got, buf[:n*channels]...)
					if i == fecIdx {
						gotRange = dec.FinalRange()
					}
				}

				// Final-range must be bit-exact on the FEC step.
				if gotRange != wantRanges[fecIdx] {
					t.Errorf("recovery packet %d: FEC final-range mismatch gopus=0x%08x libopus=0x%08x",
						r, gotRange, wantRanges[fecIdx])
				}

				// FEC-recovered frame must be sample-exact.
				fecEnd := min(fecStart+fs*channels, len(got))
				if fecEnd > len(want) {
					fecEnd = len(want)
				}
				maxDiff := 0.0
				worst := -1
				for j := fecStart; j < fecEnd; j++ {
					d := math.Abs(float64(got[j] - want[j]))
					if d > maxDiff {
						maxDiff = d
						worst = j - fecStart
					}
				}
				// The FEC LBRR decode coding-state is bit-exact (the final-range
				// matches above), so gopus and libopus make identical entropy-decode
				// decisions. The recovered float PCM, however, runs through the SILK
				// stereo MS->LR unmix and synthesis float ops, which the amd64 pure-Go
				// build rounds a few LSB differently from gcc's scalar C (the lane
				// links the scalar libopus); over a 40/60 ms stereo LBRR recovery that
				// accumulates to ~0.001. The arm64 pure-Go build is bit-exact here, and
				// the amd64 asm build matches the SIMD libopus, so require bit-exact on
				// those and hold the amd64 pure-Go build to a per-arch float-output
				// budget that is three orders of magnitude below any real desync (the
				// fixed SILK LBRR concealment bug produced ~1.0-2.0).
				tol := 0.0
				if runtime.GOARCH == "amd64" && testPuregoBuild {
					tol = fecMultiframeStereoPuregoTol
				}
				if maxDiff > tol {
					t.Errorf("recovery packet %d: FEC frame exceeds tol %.6f: worst per-sample diff=%.6f at sample %d (fs=%d)",
						r, tol, maxDiff, worst, fs)
				}

				recovered++
				if channels == 1 && isPartialMultiFrameLBRR(t, packets[r]) {
					partial++
				}
			}

			if recovered == 0 {
				t.Fatalf("no LBRR-carrying recovery packets emitted for %s", tc.name)
			}
			// The partial-LBRR gate (the multi-LBRR-frame mix this regression
			// targets) is asserted for mono, where the first-payload flag layout is
			// unambiguous. Stereo packets still exercise the same DecodeFEC path.
			if channels == 1 && partial == 0 {
				t.Fatalf("no PARTIAL multi-frame LBRR packets emitted for %s; "+
					"test does not exercise the multi-LBRR-frame mix it targets", tc.name)
			}
			t.Logf("%s: verified %d FEC recoveries (%d partial multi-frame LBRR)", tc.name, recovered, partial)
		})
	}
}

// encodeFECBurstyStreamForTest encodes a SILK stream with FEC enabled and a
// bursty (variable speech-activity) signal so the encoder emits PARTIAL
// multi-frame LBRR symbols: the ~2.3 Hz amplitude-modulated envelope has
// near-silent troughs, so within a 40/60 ms packet only some 20 ms sub-frames
// exceed the encoder's LBRR speech-activity threshold and carry LBRR.
func encodeFECBurstyStreamForTest(t *testing.T, bandwidth Bandwidth, bitrate, channels, frameSize, nFrames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, set := range []struct {
		name string
		fn   func() error
	}{
		{"SetMode", func() error { return enc.SetMode(EncoderModeSILK) }},
		{"SetFrameSize", func() error { return enc.SetFrameSize(frameSize) }},
		{"SetBandwidth", func() error { return enc.SetBandwidth(bandwidth) }},
		{"SetBitrate", func() error { return enc.SetBitrate(bitrate * channels) }},
		{"SetSignal", func() error { return enc.SetSignal(SignalVoice) }},
		{"SetPacketLoss", func() error { return enc.SetPacketLoss(25) }},
	} {
		if err := set.fn(); err != nil {
			t.Skipf("%s: %v", set.name, err)
		}
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Skipf("SetForceChannels: %v", err)
		}
	}
	enc.SetFEC(true)

	packets := make([][]byte, 0, nFrames)
	for frameIndex := range nFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			env := 0.5 + 0.5*math.Sin(2*math.Pi*2.3*tm)
			env = env * env // sharpen troughs to push some sub-frames below the LBRR threshold
			f0 := 200.0 + 40.0*math.Sin(2*math.Pi*0.7*tm)
			s := env * (0.5*math.Sin(2*math.Pi*f0*tm) + 0.2*math.Sin(2*math.Pi*2*f0*tm+0.3))
			pcm[i*channels] = float32(s)
			if channels == 2 {
				pcm[i*channels+1] = float32(s * 0.9)
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil || len(packet) == 0 {
			t.Fatalf("Encode frame %d: %v len=%d", frameIndex, err, len(packet))
		}
		if toc := ParseTOC(packet[0]); toc.Mode != ModeSILK {
			t.Skipf("Encode frame %d mode=%v want SILK", frameIndex, toc.Mode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

// isPartialMultiFrameLBRR reports whether a multi-frame SILK packet carries LBRR
// for some but not all of its internal 20 ms sub-frames (a partial LBRR_flags
// symbol). These are the packets that mix real LBRR sub-frames with concealed
// sub-frames inside one FEC recovery decode.
func isPartialMultiFrameLBRR(t *testing.T, packet []byte) bool {
	t.Helper()
	toc := ParseTOC(packet[0])
	if toc.Mode == ModeCELT || toc.FrameSize <= 960 {
		return false
	}
	first, err := extractFirstFramePayload(packet, toc)
	if err != nil || len(first) == 0 {
		return false
	}
	nFrames := toc.FrameSize / 960
	if nFrames < 2 || nFrames > 3 {
		return false
	}
	flags, ok := decodeMonoLBRRFlagsForTest(first, nFrames)
	if !ok {
		return false
	}
	set := 0
	for i := range nFrames {
		set += flags[i]
	}
	return set > 0 && set < nFrames
}

// silk_LBRR_flags_iCDF for 2- and 3-frame packets (silk/tables_other.c). Used
// to replay the LBRR_flags symbol decode for the partial-LBRR test gate.
var lbrrFlagsICDFForTest = map[int][]uint8{
	2: {203, 150, 0},
	3: {215, 195, 166, 125, 110, 82, 0},
}

// decodeMonoLBRRFlagsForTest replays the libopus mono VAD + LBRR flag decode on a
// first-frame SILK payload and returns the per-sub-frame LBRR flags. Mirrors
// silk/dec_API.c: nFrames VAD bits, one LBRR-present bit, then (if present) the
// LBRR_flags symbol = ec_dec_icdf(silk_LBRR_flags_iCDF_ptr[nFrames-2]) + 1.
func decodeMonoLBRRFlagsForTest(firstFrame []byte, nFrames int) ([3]int, bool) {
	var out [3]int
	icdf, ok := lbrrFlagsICDFForTest[nFrames]
	if !ok || len(firstFrame) == 0 {
		return out, false
	}
	var rd rangecoding.Decoder
	rd.Init(firstFrame)
	for range nFrames {
		_ = rd.DecodeBit(1) // VAD flag
	}
	lbrr := rd.DecodeBit(1) // LBRR-present flag
	if lbrr == 0 {
		return out, true
	}
	symbol := rd.DecodeICDF8Unchecked(icdf) + 1
	for i := range nFrames {
		out[i] = (symbol >> uint(i)) & 1
	}
	return out, true
}
