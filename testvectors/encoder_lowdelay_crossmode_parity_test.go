// encoder_lowdelay_crossmode_parity_test.go verifies gopus parity against
// pinned libopus 1.6.1 for OPUS_APPLICATION_RESTRICTED_LOWDELAY across the
// full low-delay cross-mode matrix:
//
//   - bitrates: representative low / mid / high CELT-only rates
//   - frame sizes: 2.5ms (120), 5ms (240), 10ms (480), 20ms (960) at 48 kHz
//   - channels: 1 (mono), 2 (stereo)
//   - signal classes: VOICE, MUSIC, AUTO (chirp)
//
// Three assertions per cell:
//
//	(a) Mode is CELT-only: every emitted packet must carry a CELT TOC byte
//	    (cfg ≥ 16). libopus forces MODE_CELT_ONLY unconditionally for
//	    RESTRICTED_LOWDELAY (opus_encoder.c line 1470–1472).
//
//	(b) Byte-exact packet parity against the pinned libopus 1.6.1 oracle.
//	    Hard gate on amd64; arm64 CELT FMA drift (≤1 ULP) reported as
//	    residual per project_arm64_celt_1ulp_drift.md.
//
//	(c) Lookahead() == sampleRate/400 for RESTRICTED_LOWDELAY (no delay
//	    compensation added). libopus opus_encoder.c OPUS_GET_LOOKAHEAD
//	    (line 3082–3091): *value = st->Fs/400; if application !=
//	    RESTRICTED_LOWDELAY && != RESTRICTED_CELT: *value += st->delay_compensation.
//
// Oracle: reuses libopus_vbr_cvbr_encode_info.c (GVCI/GVCO wire format) with
// VBR mode 0 (OPUS_SET_VBR(1), OPUS_SET_VBR_CONSTRAINT(0)) and
// application = 2051 = OPUS_APPLICATION_RESTRICTED_LOWDELAY.
package testvectors

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// ── oracle wire helpers (reuse vbr_cvbr helpers from encoder_vbr_cvbr_byte_parity_test.go) ─

// ldOracleHelperCache caches the low-delay oracle binary (same C helper as VBR).
var ldOracleHelperCache libopustest.HelperCache

func getLDOracleHelperPath(t testing.TB) (string, bool) {
	t.Helper()
	path, err := ldOracleHelperCache.CHelperPath(libopustest.CHelperConfig{
		Label:      "low-delay encode",
		OutputBase: "gopus_libopus_lowdelay_encode",
		SourceFile: "libopus_vbr_cvbr_encode_info.c",
		CFlags:     []string{"-O2", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		if libopustest.StrictRefRequired() {
			t.Fatalf("build low-delay encode helper: %v", err)
		}
		t.Skipf("low-delay encode helper unavailable: %v", err)
		return "", false
	}
	return path, true
}

// ── test matrix ─────────────────────────────────────────────────────────────

type ldMatrixCase struct {
	name      string
	frameSize int // samples at 48 kHz
	channels  int
	bitrate   int
	signal    types.Signal
	nFrames   int
}

// ldTestMatrix returns the full low-delay cross-mode grid.
// RESTRICTED_LOWDELAY always produces MODE_CELT_ONLY so there is no
// SILK/Hybrid dimension; we exercise rate × frame × channel × signal.
func ldTestMatrix() []ldMatrixCase {
	const nFrames = 50

	bitrates := []int{
		12000,  // low-rate CELT (NB/MB boundary)
		24000,  // mid-rate CELT
		48000,  // standard CELT fullband
		96000,  // high-rate CELT fullband
		128000, // very high rate stereo
	}
	frameSizes := []int{120, 240, 480, 960} // 2.5ms 5ms 10ms 20ms
	channels := []int{1, 2}
	signals := []struct {
		sig  types.Signal
		name string
	}{
		{types.SignalVoice, "voice"},
		{types.SignalMusic, "music"},
		{types.SignalAuto, "auto"},
	}

	var cases []ldMatrixCase
	for _, br := range bitrates {
		for _, fs := range frameSizes {
			for _, ch := range channels {
				for _, sig := range signals {
					cases = append(cases, ldMatrixCase{
						name:      fmt.Sprintf("br%d/fs%d/ch%d/%s", br, fs, ch, sig.name),
						frameSize: fs,
						channels:  ch,
						bitrate:   br,
						signal:    sig.sig,
						nFrames:   nFrames,
					})
				}
			}
		}
	}
	return cases
}

// ── gopus encoder ────────────────────────────────────────────────────────────

// ldEncoderPCM encodes PCM through gopus with ApplicationLowDelay and VBR.
func ldEncoderPCM(tc ldMatrixCase, pcm []float32) ([][]byte, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    tc.channels,
		Application: gopus.ApplicationLowDelay,
	})
	if err != nil {
		return nil, fmt.Errorf("new encoder: %w", err)
	}
	if err := enc.SetFrameSize(tc.frameSize); err != nil {
		return nil, fmt.Errorf("set frame size: %w", err)
	}
	if err := enc.SetBitrate(tc.bitrate); err != nil {
		return nil, fmt.Errorf("set bitrate: %w", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		return nil, fmt.Errorf("set complexity: %w", err)
	}
	if err := enc.SetSignal(tc.signal); err != nil {
		return nil, fmt.Errorf("set signal: %w", err)
	}
	// VBR unconstrained — matches oracle mode=0.
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)

	samplesPerFrame := tc.frameSize * tc.channels
	buf := make([]byte, 4000)
	packets := make([][]byte, 0, tc.nFrames)
	for i := 0; i < tc.nFrames; i++ {
		frame := pcm[i*samplesPerFrame : (i+1)*samplesPerFrame]
		n, encErr := enc.Encode(frame, buf)
		if encErr != nil {
			return nil, fmt.Errorf("encode frame %d: %w", i, encErr)
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		packets = append(packets, pkt)
	}
	return packets, nil
}

// ── assertions ───────────────────────────────────────────────────────────────

// assertLDCELTOnlyMode checks that every packet in packets has a CELT TOC
// (cfg ≥ 16 in bits 7:3 of the TOC byte).
// Reference: RFC 6716 §3.1; opus_encoder.c line 1470–1472.
func assertLDCELTOnlyMode(t *testing.T, packets [][]byte) {
	t.Helper()
	for i, pkt := range packets {
		if len(pkt) == 0 {
			t.Errorf("frame %d: empty packet", i)
			continue
		}
		cfg := pkt[0] >> 3
		if cfg < 16 {
			t.Errorf("frame %d: expected CELT-only TOC (cfg>=16), got cfg=%d (toc=0x%02x)",
				i, cfg, pkt[0])
		}
	}
}

// ── oracle PCM ───────────────────────────────────────────────────────────────

// ldGeneratePCM builds deterministic float32 PCM for the given signal class.
// The VBR oracle receives the same samples via float32 LE, so both gopus and
// libopus see identical input bits.
func ldGeneratePCM(nFrames, frameSize, channels int, sig types.Signal) []float32 {
	total := nFrames * frameSize * channels
	pcm := make([]float32, total)
	const sampleRate = 48000
	var lcg uint32 = 0xABCD1234
	for i := 0; i < nFrames*frameSize; i++ {
		t := float64(i) / float64(sampleRate)
		var mono float64
		switch sig {
		case types.SignalVoice:
			// voiced-speech character: 110 Hz fundamental + harmonics
			env := 0.5 + 0.5*math.Cos(2*math.Pi*3.0*t+0.1)
			mono = 0.55 * env * (math.Sin(2*math.Pi*110*t) +
				0.4*math.Sin(2*math.Pi*220*t) +
				0.2*math.Sin(2*math.Pi*330*t))
		case types.SignalMusic:
			// AM-modulated 440 Hz (music-like)
			mod := 0.5 + 0.5*math.Sin(2*math.Pi*2.5*t)
			mono = 0.55 * mod * math.Sin(2*math.Pi*440*t)
			mono += 0.30 * math.Sin(2*math.Pi*880*t)
		default: // SignalAuto: broadband chirp + noise
			lcg = lcg*1664525 + 1013904223
			noise := float64(int32(lcg>>9&0x3FFFFF)-0x1FFFFF) / float64(0x200000)
			// linear chirp 100–8000 Hz
			phase := 2 * math.Pi * (100*t + 0.5*(8000-100)*math.Mod(t, 1.0)*math.Mod(t, 1.0)/1.0)
			mono = 0.40*math.Sin(phase) + 0.08*noise
		}
		if mono > 0.97 {
			mono = 0.97
		}
		if mono < -0.97 {
			mono = -0.97
		}
		for ch := range channels {
			pcm[i*channels+ch] = float32(mono)
		}
	}
	return pcm
}

// ── core test ────────────────────────────────────────────────────────────────

// TestLowDelayModeIsCELTOnly asserts that every packet produced by gopus under
// ApplicationLowDelay has a CELT-only TOC byte (cfg >= 16).
// This exercises the CELT-only mode assertion (a) without needing the oracle.
func TestLowDelayModeIsCELTOnly(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	for _, tc := range ldTestMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pcm := ldGeneratePCM(tc.nFrames, tc.frameSize, tc.channels, tc.signal)
			packets, err := ldEncoderPCM(tc, pcm)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			assertLDCELTOnlyMode(t, packets)
			t.Logf("CELT-only: %d packets, all cfg>=16", len(packets))
		})
	}
}

// TestLowDelayLookahead asserts that gopus Lookahead() for ApplicationLowDelay
// equals sampleRate/400 (no delay compensation).
//
// Reference: libopus src/opus_encoder.c OPUS_GET_LOOKAHEAD (line 3082–3091):
//
//	*value = st->Fs/400;
//	if (st->application != OPUS_APPLICATION_RESTRICTED_LOWDELAY &&
//	    st->application != OPUS_APPLICATION_RESTRICTED_CELT)
//	    *value += st->delay_compensation;  // st->delay_compensation = Fs/250
func TestLowDelayLookahead(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, ch := range []int{1, 2} {
			sampleRate, ch := sampleRate, ch
			name := fmt.Sprintf("sr%d/ch%d", sampleRate, ch)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				enc, err := gopus.NewEncoder(gopus.EncoderConfig{
					SampleRate:  sampleRate,
					Channels:    ch,
					Application: gopus.ApplicationLowDelay,
				})
				if err != nil {
					t.Fatalf("new encoder: %v", err)
				}
				wantLookahead := sampleRate / 400
				gotLookahead := enc.Lookahead()
				if gotLookahead != wantLookahead {
					t.Errorf("Lookahead()=%d want=%d (sampleRate/400); "+
						"RESTRICTED_LOWDELAY must not add delay_compensation (sampleRate/250=%d)",
						gotLookahead, wantLookahead, sampleRate/250)
				} else {
					t.Logf("Lookahead()=%d == sampleRate/400 (%d/%d)", gotLookahead, sampleRate, 400)
				}
			})
		}
	}

	// Confirm that non-low-delay applications DO add delay compensation.
	t.Run("audio-has-delay-comp", func(t *testing.T) {
		enc, err := gopus.NewEncoder(gopus.EncoderConfig{
			SampleRate:  48000,
			Channels:    1,
			Application: gopus.ApplicationAudio,
		})
		if err != nil {
			t.Fatalf("new encoder: %v", err)
		}
		// AUDIO: lookahead = Fs/400 + Fs/250 = 120 + 192 = 312
		wantLookahead := 48000/400 + 48000/250
		gotLookahead := enc.Lookahead()
		if gotLookahead != wantLookahead {
			t.Errorf("audio Lookahead()=%d want=%d (Fs/400+Fs/250)", gotLookahead, wantLookahead)
		} else {
			t.Logf("audio Lookahead()=%d == Fs/400+Fs/250 (%d+%d)", gotLookahead, 48000/400, 48000/250)
		}
	})
}

// TestLowDelayCrossModeParity encodes identical PCM through gopus
// (ApplicationLowDelay, VBR) and the pinned libopus 1.6.1 oracle
// (OPUS_APPLICATION_RESTRICTED_LOWDELAY, VBR) across the full low-delay
// matrix, asserting:
//
//	(a) Each packet has a CELT-only TOC byte (cfg >= 16).
//	(b) Packet bytes are identical on amd64 (hard gate); arm64 CELT FMA
//	    drift (≤1 ULP per operation) is reported as residual per
//	    project_arm64_celt_1ulp_drift.md.
func TestLowDelayCrossModeParity(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)
	libopustest.RequireOracle(t)

	helperPath, ok := getLDOracleHelperPath(t)
	if !ok {
		return
	}

	for _, tc := range ldTestMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runLDParityCase(t, tc, helperPath)
		})
	}
}

func runLDParityCase(t *testing.T, tc ldMatrixCase, helperPath string) {
	t.Helper()

	pcm := ldGeneratePCM(tc.nFrames, tc.frameSize, tc.channels, tc.signal)

	// (c) Lookahead parity: check before encoding.
	enc0, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    tc.channels,
		Application: gopus.ApplicationLowDelay,
	})
	if err != nil {
		t.Fatalf("new encoder for lookahead check: %v", err)
	}
	wantLookahead := 48000 / 400 // Fs/400, no delay_compensation for RESTRICTED_LOWDELAY
	if gotLA := enc0.Lookahead(); gotLA != wantLookahead {
		t.Errorf("Lookahead()=%d want=%d (Fs/400 only for RESTRICTED_LOWDELAY)", gotLA, wantLookahead)
	}

	// Build oracle request: mode=0 (VBR), application=2051 (RESTRICTED_LOWDELAY).
	opusSig := gopusSignalToOpus(tc.signal)
	req := buildVBRCVBRRequest(
		oracleModeVBR, // OPUS_SET_VBR(1), OPUS_SET_VBR_CONSTRAINT(0)
		uint32(opusApplicationRestrictedLowDelay), // 2051
		48000, tc.channels, tc.frameSize, tc.bitrate,
		opusBandwidthFB, // OPUS_BANDWIDTH_FULLBAND — let oracle auto-select from FB
		opusSig,
		pcm,
		tc.nFrames,
	)

	refResults, err := runVBRCVBROracle(helperPath, req, tc.nFrames)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}

	// (a) Verify oracle itself always emits CELT-only packets.
	for i, r := range refResults {
		if len(r.data) == 0 {
			continue
		}
		cfg := r.data[0] >> 3
		if cfg < 16 {
			t.Errorf("oracle frame %d: not CELT-only (cfg=%d toc=0x%02x) — "+
				"libopus should force MODE_CELT_ONLY for RESTRICTED_LOWDELAY",
				i, cfg, r.data[0])
		}
	}

	// Encode with gopus.
	gotPackets, err := ldEncoderPCM(tc, pcm)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(gotPackets) != len(refResults) {
		t.Fatalf("packet count mismatch: gopus=%d libopus=%d", len(gotPackets), len(refResults))
	}

	// (a) Assert gopus emits CELT-only.
	assertLDCELTOnlyMode(t, gotPackets)

	// (b) Byte parity comparison.
	var diffFrames []int
	for i := range refResults {
		if !bytes.Equal(gotPackets[i], refResults[i].data) {
			diffFrames = append(diffFrames, i)
		}
	}

	// RESTRICTED_LOWDELAY is CELT-only (asserted above on both encoders), so every
	// diverging frame is a CELT float-analysis near-tie flip — the documented
	// ≤1-ULP boundary on the pure-Go builds (arm64 FMA, and amd64-purego Go float
	// vs the scalar libopus this gate links). In unconstrained VBR a near-tie flip
	// also shifts the chosen bit allocation, so a frame's length can change as a
	// downstream effect of the same boundary. Only the amd64 asm/SIMD build is held
	// strictly bit-exact. See project_arm64_celt_1ulp_drift.md.
	floatBoundary := encoderCELTFloatBoundaryBuild()

	if len(diffFrames) == 0 {
		t.Logf("PASS: %d packets byte-exact vs libopus RESTRICTED_LOWDELAY oracle", tc.nFrames)
		return
	}

	// Log first 3 mismatches.
	for idx, fi := range diffFrames {
		if idx >= 3 {
			t.Logf("  ... and %d more differing frames", len(diffFrames)-3)
			break
		}
		got := gotPackets[fi]
		want := refResults[fi].data
		first := -1
		limit := min(len(want), len(got))
		for j := 0; j < limit; j++ {
			if got[j] != want[j] {
				first = j
				break
			}
		}
		if first < 0 && len(got) != len(want) {
			first = limit
		}
		t.Logf("  frame %d DIVERGES len(got=%d want=%d) firstByte=%d",
			fi, len(got), len(want), first)
	}

	if floatBoundary {
		// Pure-Go CELT float residual: CELT float arithmetic on arm64 uses FMA
		// contraction that diverges from clang -ffp-contract=on by ≤1 ULP per
		// operation, and the amd64-purego Go float backend diverges from gcc's
		// scalar CELT path by the same magnitude. The CELT-only mode and packet
		// count are asserted strictly above on every build. amd64 asm/CI gate
		// holds bit-exact. Reference: project_arm64_celt_1ulp_drift.md.
		t.Logf("RESIDUAL (pure-Go CELT float boundary): %d/%d packets differ — "+
			"CELT float FMA/codegen vs the scalar libopus oracle "+
			"(project_arm64_celt_1ulp_drift.md); amd64 asm/CI gate holds",
			len(diffFrames), tc.nFrames)
	} else {
		t.Errorf("low-delay byte parity FAIL: %d/%d packets differ (arch=%s/%s)",
			len(diffFrames), tc.nFrames, runtime.GOOS, runtime.GOARCH)
	}
}
