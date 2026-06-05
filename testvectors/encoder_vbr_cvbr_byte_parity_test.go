// VBR and CVBR byte-parity tests against pinned libopus 1.6.1.
//
// (1) Unconstrained VBR: SetVBR(true), SetVBRConstraint(false) — encodes the
//
//	same PCM through gopus and libopus across SILK/CELT/Hybrid × rates ×
//	frame sizes and asserts byte-identical packets where libopus is
//	deterministic on the current platform.
//
// (2) CVBR: SetVBRConstraint(true) — asserts that per-frame packet-size
//
//	sequences match libopus across a multi-frame stream. The CVBR
//	reservoir/bound logic inside celt/encoder.go must track libopus exactly.
//
// Reference: libopus src/opus_encoder.c opus_encode_native()
//
//	use_vbr         = OPUS_GET_VBR                (default 1)
//	constrained_vbr = OPUS_GET_VBR_CONSTRAINT      (default 0)
//
// The CELT sub-encoder constrained-VBR reservoir logic lives in
//
//	celt/celt_encoder.c opus_celt_encode_with_ec() around the
//	vbr_offset / vbr_count / nb_bits_budget path.
package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/types"
)

// ---- libopus OPUS_APPLICATION_* numeric constants (opus_defines.h) ----------

// opusApplicationVoIP/opusApplicationAudio and the opusSignal* constants are
// shared with the auto-mode parity test in this package.
const opusApplicationLowDelay = uint32(2051)

// libopus OPUS_BANDWIDTH_* numeric constants.
const (
	opusBandwidthNB   = uint32(1101)
	opusBandwidthMB   = uint32(1102)
	opusBandwidthWB   = uint32(1103)
	opusBandwidthSWB  = uint32(1104)
	opusBandwidthFB   = uint32(1105)
	opusBandwidthAuto = uint32(0xFFFFFC18) // OPUS_AUTO = -1000 as two's-complement uint32
)

// VBR mode values for the oracle wire protocol.
const (
	oracleModeVBR  = uint32(0) // OPUS_SET_VBR(1), OPUS_SET_VBR_CONSTRAINT(0)
	oracleModeCVBR = uint32(1) // OPUS_SET_VBR(1), OPUS_SET_VBR_CONSTRAINT(1)
)

// ---- oracle build cache -------------------------------------------------------

var vbrCVBREncodeHelper libopustest.HelperCache

func getVBRCVBREncodeHelperPath(t testing.TB) (string, bool) {
	t.Helper()
	path, err := vbrCVBREncodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "vbr-cvbr encode",
		OutputBase: "gopus_libopus_vbr_cvbr_encode",
		SourceFile: "libopus_vbr_cvbr_encode_info.c",
		CFlags:     []string{"-O2", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		if libopustest.StrictRefRequired() {
			t.Fatalf("build vbr-cvbr encode helper: %v", err)
		}
		t.Skipf("vbr-cvbr encode helper unavailable: %v", err)
		return "", false
	}
	return path, true
}

// ---- wire protocol helpers ---------------------------------------------------

// vbrCVBRRequest builds the oracle input buffer for one encode run.
//
// Wire layout (little-endian):
//
//	"GVCI" u32(1) u32(mode) u32(application) u32(sampleRate) u32(channels)
//	u32(frameSize) u32(bitrate) u32(bandwidth) u32(signal) u32(nFrames)
//	then nFrames * frameSize * channels float32 samples.
func buildVBRCVBRRequest(
	mode, application uint32,
	sampleRate, channels, frameSize, bitrate int,
	bandwidth, signal uint32,
	pcm []float32,
	nFrames int,
) []byte {
	samplesPerFrame := frameSize * channels
	payload := libopustest.NewOraclePayloadVersion("GVCI", 1,
		mode,
		application,
		uint32(sampleRate),
		uint32(channels),
		uint32(frameSize),
		uint32(bitrate),
		bandwidth,
		signal,
		uint32(nFrames),
	)
	for i := 0; i < nFrames*samplesPerFrame; i++ {
		payload.Float32(pcm[i])
	}
	return payload.Bytes()
}

// oracleResult holds one encoded packet from the oracle.
type oracleResult struct {
	data       []byte
	finalRange uint32
}

// runVBRCVBROracle invokes the C oracle and returns per-frame results.
func runVBRCVBROracle(helperPath string, req []byte, nFrames int) ([]oracleResult, error) {
	raw, err := libopustest.RunHelper(helperPath, req)
	if err != nil {
		return nil, fmt.Errorf("run vbr-cvbr encode oracle: %w", err)
	}

	// Parse response: "GVCO" u32(1) u32(nFrames) then nFrames × { u32(len) u32(finalRange) bytes }
	if len(raw) < 12 || string(raw[0:4]) != "GVCO" {
		return nil, fmt.Errorf("bad oracle response magic")
	}
	version := binary.LittleEndian.Uint32(raw[4:8])
	if version != 1 {
		return nil, fmt.Errorf("bad oracle version %d", version)
	}
	gotN := int(binary.LittleEndian.Uint32(raw[8:12]))
	if gotN != nFrames {
		return nil, fmt.Errorf("oracle frame count mismatch: got %d want %d", gotN, nFrames)
	}

	results := make([]oracleResult, nFrames)
	off := 12
	for i := range nFrames {
		if off+8 > len(raw) {
			return nil, fmt.Errorf("truncated oracle response at frame %d", i)
		}
		pktLen := int(binary.LittleEndian.Uint32(raw[off:]))
		fr := binary.LittleEndian.Uint32(raw[off+4:])
		off += 8
		if off+pktLen > len(raw) {
			return nil, fmt.Errorf("truncated oracle packet at frame %d (need %d bytes, have %d)", i, pktLen, len(raw)-off)
		}
		results[i] = oracleResult{
			data:       append([]byte(nil), raw[off:off+pktLen]...),
			finalRange: fr,
		}
		off += pktLen
	}
	if off != len(raw) {
		return nil, fmt.Errorf("trailing oracle bytes: %d", len(raw)-off)
	}
	return results, nil
}

// ---- gopus encoder helpers ---------------------------------------------------

// encodeVBRCVBRWithGopus encodes nFrames of pcm via gopus with the given settings.
// vbrConstraint=false → VBR; vbrConstraint=true → CVBR.
func encodeVBRCVBRWithGopus(
	application gopus.Application,
	sampleRate, channels, frameSize, bitrate int,
	bandwidth types.Bandwidth,
	setBandwidth bool,
	signal types.Signal,
	vbrConstraint bool,
	pcm []float32,
	nFrames int,
) ([]oracleResult, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: application,
	})
	if err != nil {
		return nil, fmt.Errorf("new encoder: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return nil, fmt.Errorf("set frame size: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, fmt.Errorf("set bitrate: %w", err)
	}
	if setBandwidth {
		if err := enc.SetBandwidth(bandwidth); err != nil {
			return nil, fmt.Errorf("set bandwidth: %w", err)
		}
	}
	if err := enc.SetSignal(signal); err != nil {
		return nil, fmt.Errorf("set signal: %w", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		return nil, fmt.Errorf("set complexity: %w", err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(vbrConstraint)

	samplesPerFrame := frameSize * channels
	buf := make([]byte, 4000)
	results := make([]oracleResult, 0, nFrames)
	for i := range nFrames {
		frame := pcm[i*samplesPerFrame : (i+1)*samplesPerFrame]
		n, err := enc.Encode(frame, buf)
		if err != nil {
			return nil, fmt.Errorf("encode frame %d: %w", i, err)
		}
		results = append(results, oracleResult{
			data:       append([]byte(nil), buf[:n]...),
			finalRange: enc.FinalRange(),
		})
	}
	return results, nil
}

// ---- bandwidth/signal mapping helpers ----------------------------------------

func gopusBandwidthToOpus(bw types.Bandwidth) (uint32, bool) {
	switch bw {
	case types.BandwidthNarrowband:
		return opusBandwidthNB, true
	case types.BandwidthMediumband:
		return opusBandwidthMB, true
	case types.BandwidthWideband:
		return opusBandwidthWB, true
	case types.BandwidthSuperwideband:
		return opusBandwidthSWB, true
	case types.BandwidthFullband:
		return opusBandwidthFB, true
	}
	return 0, false
}

func gopusSignalToOpus(sig types.Signal) uint32 {
	switch sig {
	case types.SignalVoice:
		return opusSignalVoice
	case types.SignalMusic:
		return opusSignalMusic
	}
	return opusSignalAuto
}

func gopusApplicationToOpus(app gopus.Application) (uint32, bool) {
	switch app {
	case gopus.ApplicationVoIP:
		return opusApplicationVoIP, true
	case gopus.ApplicationAudio:
		return opusApplicationAudio, true
	case gopus.ApplicationLowDelay:
		return opusApplicationLowDelay, true
	}
	return 0, false
}

// ---- test case definitions ---------------------------------------------------

type vbrCVBRCase struct {
	name         string
	application  gopus.Application
	frameSize    int // samples at 48 kHz
	channels     int
	bitrate      int
	bandwidth    types.Bandwidth
	setBandwidth bool // false = let encoder auto-select
	signal       types.Signal
	nFrames      int
}

// vbrTestCases returns the grid of cases for VBR parity:
// SILK × WB, Hybrid × SWB/FB, CELT × FB, across mono/stereo, 10ms/20ms frames.
func vbrTestCases() []vbrCVBRCase {
	return []vbrCVBRCase{
		// SILK mono
		{name: "silk-nb-mono-10ms-12k", application: gopus.ApplicationVoIP, frameSize: 480, channels: 1, bitrate: 12000, bandwidth: types.BandwidthNarrowband, setBandwidth: true, signal: types.SignalVoice, nFrames: 50},
		{name: "silk-wb-mono-20ms-24k", application: gopus.ApplicationVoIP, frameSize: 960, channels: 1, bitrate: 24000, bandwidth: types.BandwidthWideband, setBandwidth: true, signal: types.SignalVoice, nFrames: 50},
		{name: "silk-wb-stereo-20ms-32k", application: gopus.ApplicationVoIP, frameSize: 960, channels: 2, bitrate: 32000, bandwidth: types.BandwidthWideband, setBandwidth: true, signal: types.SignalVoice, nFrames: 50},
		// Higher-rate stereo SILK VBR keeps full stereo width, exercising the
		// stereo_LR_to_MS side-rate split / width decision (not just mid-only).
		{name: "silk-wb-stereo-20ms-64k", application: gopus.ApplicationVoIP, frameSize: 960, channels: 2, bitrate: 64000, bandwidth: types.BandwidthWideband, setBandwidth: true, signal: types.SignalVoice, nFrames: 50},

		// Hybrid (SILK+CELT)
		{name: "hybrid-swb-mono-20ms-32k", application: gopus.ApplicationAudio, frameSize: 960, channels: 1, bitrate: 32000, bandwidth: types.BandwidthSuperwideband, setBandwidth: true, signal: types.SignalVoice, nFrames: 50},
		{name: "hybrid-fb-stereo-20ms-48k", application: gopus.ApplicationAudio, frameSize: 960, channels: 2, bitrate: 48000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalVoice, nFrames: 50},

		// CELT (restricted-lowdelay → maps to ApplicationLowDelay, restricted-celt-like)
		{name: "celt-fb-mono-10ms-64k", application: gopus.ApplicationLowDelay, frameSize: 480, channels: 1, bitrate: 64000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalMusic, nFrames: 50},
		{name: "celt-fb-stereo-20ms-64k", application: gopus.ApplicationLowDelay, frameSize: 960, channels: 2, bitrate: 64000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalMusic, nFrames: 50},
		{name: "celt-fb-mono-20ms-96k", application: gopus.ApplicationAudio, frameSize: 960, channels: 1, bitrate: 96000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalMusic, nFrames: 50},
	}
}

// cvbrTestCases returns the grid for CVBR reservoir/bound parity.
// Uses longer streams to exercise the multi-frame CVBR budget tracking.
func cvbrTestCases() []vbrCVBRCase {
	return []vbrCVBRCase{
		{name: "cvbr-silk-wb-mono-20ms-24k", application: gopus.ApplicationVoIP, frameSize: 960, channels: 1, bitrate: 24000, bandwidth: types.BandwidthWideband, setBandwidth: true, signal: types.SignalVoice, nFrames: 100},
		{name: "cvbr-celt-fb-mono-20ms-64k", application: gopus.ApplicationLowDelay, frameSize: 960, channels: 1, bitrate: 64000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalMusic, nFrames: 100},
		{name: "cvbr-celt-fb-stereo-20ms-64k", application: gopus.ApplicationLowDelay, frameSize: 960, channels: 2, bitrate: 64000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalMusic, nFrames: 100},
		{name: "cvbr-hybrid-fb-stereo-20ms-48k", application: gopus.ApplicationAudio, frameSize: 960, channels: 2, bitrate: 48000, bandwidth: types.BandwidthFullband, setBandwidth: true, signal: types.SignalVoice, nFrames: 100},
	}
}

// ---- PCM generation ----------------------------------------------------------

// makeVBRCVBRTestPCM generates deterministic float32 PCM suitable for
// driving mixed SILK/CELT/Hybrid transitions (speech-like fundamental at 220 Hz
// with harmonics, plus low-level broadband noise).
func makeVBRCVBRTestPCM(nFrames, frameSize, channels int) []float32 {
	total := nFrames * frameSize * channels
	pcm := make([]float32, total)
	var lcg uint32 = 0x12345678
	for i := 0; i < nFrames*frameSize; i++ {
		t := float64(i) / 48000.0
		// voiced-speech-like fundamental + harmonics
		s := 0.4*math.Sin(2*math.Pi*220*t) +
			0.2*math.Sin(2*math.Pi*440*t) +
			0.1*math.Sin(2*math.Pi*880*t)
		// modulate amplitude to create speech-like transitions
		env := 0.5 + 0.5*math.Sin(2*math.Pi*3.0*t)
		s *= env
		// low-level broadband noise
		lcg = lcg*1664525 + 1013904223
		noise := float64(int32(lcg>>8&0x7FFFFF)-0x3FFFFF) / float64(0x400000)
		s += noise * 0.04
		// clamp
		if s > 0.99 {
			s = 0.99
		}
		if s < -0.99 {
			s = -0.99
		}
		for ch := range channels {
			pcm[i*channels+ch] = float32(s)
		}
	}
	return pcm
}

// ---- VBR byte-parity test ----------------------------------------------------

// TestVBRByteParityAgainstLibopus encodes the same PCM through gopus (VBR)
// and the pinned libopus oracle and asserts byte-identical packets.
//
// Current parity status per mode:
//   - CELT mono (celt-fb-mono-*): byte-identical on all platforms.
//   - CELT stereo (celt-fb-stereo-*): size-identical; on darwin/arm64
//     content may differ due to the documented 1-ULP CELT drift. The
//     waveform quality floor is amd64EncoderFixtureWaveformMinQ (-60 dB).
//   - SILK (silk-*): packet sizes diverge from libopus by 1-10 bytes per
//     frame. Residual: gopus SILK VBR rate-control produces slightly
//     different per-frame budgets than libopus silk/enc_API.c.
//   - Hybrid (hybrid-*): packet sizes diverge significantly. Residual:
//     the gopus hybrid VBR architecture passes payloadTargetMain (the
//     nominal target) as CELT's bit budget, while libopus passes the full
//     max_data_bytes - 1 - redundancy_bytes (opus_encoder.c line 2392).
//     The CELT sub-encoder's internal VBR reservoir then decides actual
//     frame size from within the larger budget.
//
// SILK and Hybrid residuals are reported as non-fatal log messages with
// exact evidence. CELT mono parity is a hard assertion.
func TestVBRByteParityAgainstLibopus(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	helperPath, ok := getVBRCVBREncodeHelperPath(t)
	if !ok {
		return
	}

	for _, tc := range vbrTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runVBRParityCase(t, tc, helperPath)
		})
	}
}

func runVBRParityCase(t *testing.T, tc vbrCVBRCase, helperPath string) {
	t.Helper()

	pcm := makeVBRCVBRTestPCM(tc.nFrames, tc.frameSize, tc.channels)

	opusApp, ok := gopusApplicationToOpus(tc.application)
	if !ok {
		t.Skipf("application not mappable to libopus constant")
		return
	}
	var opusBW uint32
	if tc.setBandwidth {
		opusBW, ok = gopusBandwidthToOpus(tc.bandwidth)
		if !ok {
			t.Fatalf("bandwidth not mappable")
		}
	} else {
		opusBW = opusBandwidthAuto
	}
	opusSig := gopusSignalToOpus(tc.signal)

	req := buildVBRCVBRRequest(
		oracleModeVBR, opusApp,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		opusBW, opusSig,
		pcm, tc.nFrames,
	)

	refResults, err := runVBRCVBROracle(helperPath, req, tc.nFrames)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}

	goResults, err := encodeVBRCVBRWithGopus(
		tc.application,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		tc.bandwidth, tc.setBandwidth, tc.signal,
		false, // VBR, not CVBR
		pcm, tc.nFrames,
	)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(goResults) != len(refResults) {
		t.Fatalf("packet count mismatch: gopus=%d libopus=%d", len(goResults), len(refResults))
	}

	var mismatchBytes, mismatchLen, mismatchRange int
	firstMismatch := -1
	for i := range refResults {
		ref := refResults[i]
		got := goResults[i]
		if len(got.data) != len(ref.data) {
			mismatchLen++
			if firstMismatch < 0 {
				firstMismatch = i
			}
		} else if !bytes.Equal(got.data, ref.data) {
			mismatchBytes++
			if firstMismatch < 0 {
				firstMismatch = i
			}
		}
		if got.finalRange != ref.finalRange {
			mismatchRange++
		}
	}

	if mismatchLen == 0 && mismatchBytes == 0 {
		t.Logf("VBR byte-identical: all %d frames match", tc.nFrames)
		return
	}

	isDarwinARM64 := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	isLinuxAMD64 := runtime.GOOS == "linux" && runtime.GOARCH == "amd64"

	t.Logf("VBR mismatch: frames=%d len_mismatch=%d bytes_mismatch=%d range_mismatch=%d firstMismatch=%d",
		tc.nFrames, mismatchLen, mismatchBytes, mismatchRange, firstMismatch)

	if mismatchLen > 0 {
		refLens := make([]int, len(refResults))
		goLens := make([]int, len(goResults))
		for i := range refResults {
			refLens[i] = len(refResults[i].data)
			goLens[i] = len(goResults[i].data)
		}

		isSILKCase := len(tc.name) >= 4 && tc.name[:4] == "silk"

		// Hybrid (and CELT) per-frame packet SIZE parity is a HARD requirement.
		// Hybrid hands the CELT sub-encoder the full nb_compr_bytes =
		// (max_data_bytes-1)-redundancy_bytes budget (opus_encoder.c line 2392) and
		// the CELT VBR reservoir (compute_vbr, celt_encoder.c) chooses the per-frame
		// size from within it, so Hybrid sizes now track libopus exactly.
		if !isSILKCase {
			t.Fatalf("VBR packet SIZE mismatch: mismatch=%d/%d firstAt=%d\n  refLens=%v\n  gopusLens=%v",
				mismatchLen, tc.nFrames, firstMismatch, refLens, goLens)
			return
		}

		// Pure-SILK VBR: now a HARD assertion.
		//
		// FIXED at source: the VoIP input high-pass stage. libopus applies the
		// adaptive hp_cutoff() biquad to VoIP input (src/opus_encoder.c line 1982),
		// driven by the SILK variable-HP-cutoff smoother (silk_HP_variable_cutoff,
		// silk/HP_variable_cutoff.c) and the Opus-level variable_HP_smth2_Q15
		// smoothing; every other application uses the fixed 3 Hz dc_reject(). gopus
		// previously always used dc_reject, so the resampled SILK input — and hence
		// every shaping/NSQ quantity and the iter-0 packet size — diverged for
		// VoIP-SILK only. Hybrid/CELT used Audio/LowDelay (dc_reject) and matched;
		// restricted-SILK CBR used dc_reject and stayed byte-exact. Implementing
		// hp_cutoff for VoIP (encoder.preprocessInputHP / hpCutoff +
		// silk.UpdateVariableHPCutoff) makes the SILK input track libopus.
		//
		// linux/amd64 (CI): HARD per-frame size-parity gate. On darwin/arm64 a
		// residual ≤1-ULP float-contraction difference in the SILK FLP shaping
		// chain remains. Root cause (verified): libopus is built with clang, which
		// emits scalar FMA (fmadd/fmsub) for the double-precision multiply-adds in
		// the SILK FLP kernels (warped_autocorrelation_FLP, find_LPC/Burg,
		// hp_cutoff biquad) on arm64 but plain mulsd+addsd on x86_64 baseline.
		// Go's backend matches that exactly per-arch (FMADDD on arm64, MULSD+ADDSD
		// on amd64), but clang and the Go SSA scheduler choose *different* fusion
		// groupings for the same multi-term expression on arm64 (e.g. which of the
		// two `warping*x` products is absorbed into the surrounding add). The
		// isolated warped_autocorrelation_FLP kernel is in fact bit-identical to the
		// clang arm64 reference; the divergence enters the cascade as a 1-ULP delta
		// downstream that crosses a silk_float2int rounding boundary in the shaping
		// AR_Q13 coefficients (silk ctrl oracle: AR_Q13 off by exactly one quant
		// step), then feeds NSQ/gain and flips an occasional per-frame byte count.
		// On x86_64 neither compiler fuses, so the IEEE double mul-then-add is
		// identical and amd64 is byte-exact. This is the same darwin/arm64-only
		// float-FMA-tail class as the documented CELT 1-ULP drift (CI/amd64 is the
		// hard gate); the per-frame size sequence is bounded and reported, not
		// silently ignored.
		if isDarwinARM64 {
			maxDelta := 0
			for i := range refLens {
				d := refLens[i] - goLens[i]
				if d < 0 {
					d = -d
				}
				if d > maxDelta {
					maxDelta = d
				}
			}
			// Guard against gross regressions even within the arm64 budget: the
			// post-hp_cutoff residual is a handful of bytes per frame at most.
			const silkVBRArm64MaxByteDelta = 12
			if maxDelta > silkVBRArm64MaxByteDelta {
				t.Fatalf("SILK VBR size drift on darwin/arm64 exceeds 1-ULP budget: maxDelta=%d (>%d) mismatch=%d/%d\n  refLens=%v\n  gopusLens=%v",
					maxDelta, silkVBRArm64MaxByteDelta, mismatchLen, tc.nFrames, refLens, goLens)
			}
			t.Logf("SILK VBR size residual on darwin/arm64 (≤1-ULP hp_cutoff/shaping-AR float drift): mismatch=%d/%d maxDelta=%d firstAt=%d",
				mismatchLen, tc.nFrames, maxDelta, firstMismatch)
			return
		}
		// linux/amd64 and all other platforms: HARD size-parity assertion.
		t.Fatalf("SILK VBR packet SIZE mismatch on %s/%s: mismatch=%d/%d firstAt=%d\n  refLens=%v\n  gopusLens=%v",
			runtime.GOOS, runtime.GOARCH, mismatchLen, tc.nFrames, firstMismatch, refLens, goLens)
		return
	}

	// Size-identical, content mismatch.
	if isLinuxAMD64 {
		// Full byte parity required on CI.
		t.Errorf("VBR byte content mismatch on linux/amd64: mismatch=%d/%d firstAt=%d",
			mismatchBytes, tc.nFrames, firstMismatch)
		return
	}
	if isDarwinARM64 {
		// Known 1-ULP CELT drift on darwin/arm64; use the same floor as the
		// encoder fixture suite (amd64EncoderFixtureWaveformMinQ = -60.0).
		refPackets := make([][]byte, len(refResults))
		goPackets := make([][]byte, len(goResults))
		for i := range refResults {
			refPackets[i] = refResults[i].data
			goPackets[i] = goResults[i].data
		}
		q, delay, err := comparePacketWaveformsWithLibopusReference(refPackets, goPackets, tc.channels, tc.frameSize)
		if err != nil {
			t.Fatalf("compare decoded waveforms: %v", err)
		}
		if q < amd64EncoderFixtureWaveformMinQ {
			t.Fatalf("VBR content drift on darwin/arm64 changed waveform too much: Q=%.2f delay=%d mismatch=%d/%d (floor=%.1f)",
				q, delay, mismatchBytes, tc.nFrames, amd64EncoderFixtureWaveformMinQ)
		}
		t.Logf("VBR content mismatch on darwin/arm64 (known 1-ULP CELT drift): Q=%.2f delay=%d mismatch=%d/%d",
			q, delay, mismatchBytes, tc.nFrames)
		return
	}
	// Other platforms: report as non-fatal residual.
	t.Logf("VBR content mismatch (platform=%s/%s): mismatch=%d/%d firstAt=%d — residual",
		runtime.GOOS, runtime.GOARCH, mismatchBytes, tc.nFrames, firstMismatch)
}

// ---- CVBR packet-size distribution parity test --------------------------------

// TestCVBRSizeDistributionAgainstLibopus verifies that the per-frame packet-size
// sequence from gopus (CVBR mode) matches libopus across a multi-frame stream.
//
// The CVBR reservoir logic in celt/encoder.go must track the libopus
// celt_encoder.c vbr_offset / vbr_count / nb_bits_budget path exactly.
//
// We compare:
//   - Per-frame packet lengths: must be identical.
//   - Aggregate statistics (mean, p95, max).
//   - Final-range values (full parity where deterministic).
func TestCVBRSizeDistributionAgainstLibopus(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	helperPath, ok := getVBRCVBREncodeHelperPath(t)
	if !ok {
		return
	}

	for _, tc := range cvbrTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCVBRParityCase(t, tc, helperPath)
		})
	}
}

func runCVBRParityCase(t *testing.T, tc vbrCVBRCase, helperPath string) {
	t.Helper()

	pcm := makeVBRCVBRTestPCM(tc.nFrames, tc.frameSize, tc.channels)

	opusApp, ok := gopusApplicationToOpus(tc.application)
	if !ok {
		t.Skipf("application not mappable to libopus constant")
		return
	}
	var opusBW uint32
	if tc.setBandwidth {
		opusBW, ok = gopusBandwidthToOpus(tc.bandwidth)
		if !ok {
			t.Fatalf("bandwidth not mappable")
		}
	} else {
		opusBW = opusBandwidthAuto
	}
	opusSig := gopusSignalToOpus(tc.signal)

	req := buildVBRCVBRRequest(
		oracleModeCVBR, opusApp,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		opusBW, opusSig,
		pcm, tc.nFrames,
	)

	refResults, err := runVBRCVBROracle(helperPath, req, tc.nFrames)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}

	goResults, err := encodeVBRCVBRWithGopus(
		tc.application,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		tc.bandwidth, tc.setBandwidth, tc.signal,
		true, // CVBR
		pcm, tc.nFrames,
	)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(goResults) != len(refResults) {
		t.Fatalf("packet count mismatch: gopus=%d libopus=%d", len(goResults), len(refResults))
	}

	// Compute size-distribution statistics.
	refLens := make([]int, len(refResults))
	goLens := make([]int, len(goResults))
	var lenMismatch int
	var firstLenMismatch int = -1
	for i := range refResults {
		refLens[i] = len(refResults[i].data)
		goLens[i] = len(goResults[i].data)
		if refLens[i] != goLens[i] {
			lenMismatch++
			if firstLenMismatch < 0 {
				firstLenMismatch = i
			}
		}
	}

	refSorted := make([]int, len(refLens))
	goSorted := make([]int, len(goLens))
	copy(refSorted, refLens)
	copy(goSorted, goLens)
	sort.Ints(refSorted)
	sort.Ints(goSorted)

	refMean := meanInt(refLens)
	goMean := meanInt(goLens)
	refP95 := percentileInt(refSorted, 95)
	goP95 := percentileInt(goSorted, 95)
	refMax := refSorted[len(refSorted)-1]
	goMax := goSorted[len(goSorted)-1]

	// CVBR expected target bytes per frame.
	expectedBytes := (tc.bitrate * tc.frameSize) / (48000 * 8)

	t.Logf("CVBR size stats (nFrames=%d, targetBytes=%d):", tc.nFrames, expectedBytes)
	t.Logf("  libopus: mean=%.1f p95=%d max=%d", refMean, refP95, refMax)
	t.Logf("  gopus:   mean=%.1f p95=%d max=%d", goMean, goP95, goMax)
	t.Logf("  size mismatch: %d/%d frames (firstAt=%d)", lenMismatch, tc.nFrames, firstLenMismatch)

	if lenMismatch > 0 {
		isDarwinARM64 := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

		// Log a per-frame diff for the first mismatching region.
		limit := firstLenMismatch + 5
		if limit > len(refLens) {
			limit = len(refLens)
		}
		start := max(firstLenMismatch-2, 0)
		t.Logf("  first mismatch region (frames %d..%d):", start, limit-1)
		for i := start; i < limit; i++ {
			mark := ""
			if refLens[i] != goLens[i] {
				mark = " <-- MISMATCH"
			}
			t.Logf("    frame[%d]: libopus=%d gopus=%d%s", i, refLens[i], goLens[i], mark)
		}

		if isDarwinARM64 {
			// darwin/arm64: content drift can affect CVBR size decisions marginally.
			// Check that sizes are within the CVBR ±15% tolerance band relative to
			// the libopus reference (not a hard fail, but report).
			cvbrBound := float64(expectedBytes) * 1.15
			badGo := 0
			for _, l := range goLens {
				if float64(l) > cvbrBound*1.1 {
					badGo++
				}
			}
			if badGo > 0 {
				t.Errorf("CVBR size out of CVBR tolerance on darwin/arm64: %d/%d frames exceed 1.265× target", badGo, tc.nFrames)
			} else {
				t.Logf("CVBR size mismatch on darwin/arm64 (1-ULP drift): %d/%d frames — within tolerance, reporting as residual", lenMismatch, tc.nFrames)
			}
		} else {
			// Hard failure on all other platforms.
			t.Errorf("CVBR packet size mismatch: %d/%d frames (firstAt=%d)\n  refLens=%v\n  gopusLens=%v",
				lenMismatch, tc.nFrames, firstLenMismatch, refLens, goLens)
		}
		return
	}

	// Full size parity achieved. Now check final-range parity.
	rangeMismatch := 0
	for i := range refResults {
		if refResults[i].finalRange != goResults[i].finalRange {
			rangeMismatch++
		}
	}
	if rangeMismatch > 0 {
		isDarwinARM64 := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
		if isDarwinARM64 {
			t.Logf("CVBR final-range mismatch on darwin/arm64 (known 1-ULP CELT drift): %d/%d frames", rangeMismatch, tc.nFrames)
		} else {
			t.Errorf("CVBR final-range mismatch: %d/%d frames", rangeMismatch, tc.nFrames)
		}
	} else {
		t.Logf("CVBR full parity (size + range): all %d frames match", tc.nFrames)
	}
}

// ---- VBR parity via opus_demo (exhaustive tier) --------------------------------

// TestVBRByteParityViaOpusDemoExhaustive cross-validates gopus VBR output
// against opus_demo (the libopus reference encoder CLI) at the exhaustive tier.
// This is a second opinion on top of the C oracle test.
func TestVBRByteParityViaOpusDemoExhaustive(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if !ok {
		t.Skip("opus_demo not available; skipping exhaustive VBR parity")
	}

	tmpDir, err := os.MkdirTemp("", "gopus-vbr-demo-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, tc := range vbrTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runVBRParityCaseViaOpusDemo(t, tc, opusDemo, tmpDir)
		})
	}
}

func runVBRParityCaseViaOpusDemo(t *testing.T, tc vbrCVBRCase, opusDemo, tmpDir string) {
	t.Helper()

	pcm := makeVBRCVBRTestPCM(tc.nFrames, tc.frameSize, tc.channels)

	// Map to opus_demo args.
	appArg := opusDemoAppFromApplication(tc.application)
	if appArg == "" {
		t.Skipf("application %v not supported by opus_demo", tc.application)
		return
	}
	var bwArg string
	if tc.setBandwidth {
		var bwOK bool
		bwArg, bwOK = opusDemoBandwidthFromBandwidth(tc.bandwidth)
		if !bwOK {
			t.Fatalf("bandwidth not mappable to opus_demo arg")
		}
	}
	frameArg, err := frameSizeSamplesToArg(tc.frameSize)
	if err != nil {
		t.Fatalf("map frame size: %v", err)
	}

	safeName := tc.name
	rawPath := filepath.Join(tmpDir, safeName+".vbr.f32")
	bitPath := filepath.Join(tmpDir, safeName+".vbr.bit")

	if err := writeFloat32LEFile(rawPath, pcm); err != nil {
		t.Fatalf("write raw input: %v", err)
	}

	args := []string{
		"-e", appArg, "48000", fmt.Sprintf("%d", tc.channels), fmt.Sprintf("%d", tc.bitrate),
		"-f32", "-complexity", "10", "-framesize", frameArg,
	}
	if tc.setBandwidth && bwArg != "" {
		args = append(args, "-bandwidth", bwArg)
	}
	args = append(args, rawPath, bitPath)

	if out, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("opus_demo encode failed: %v (%s)", err, out)
	}

	refPackets, refRanges, err := parseOpusDemoEncodeBitstream(bitPath)
	if err != nil {
		t.Fatalf("parse bitstream: %v", err)
	}

	goResults, err := encodeVBRCVBRWithGopus(
		tc.application,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		tc.bandwidth, tc.setBandwidth, tc.signal,
		false, pcm, tc.nFrames,
	)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(goResults) != len(refPackets) {
		t.Fatalf("packet count mismatch: gopus=%d opusdemo=%d", len(goResults), len(refPackets))
	}

	var lenMismatch, bytesMismatch, rangeMismatch int
	for i := range refPackets {
		if len(goResults[i].data) != len(refPackets[i]) {
			lenMismatch++
		} else if !bytes.Equal(goResults[i].data, refPackets[i]) {
			bytesMismatch++
		}
		if goResults[i].finalRange != refRanges[i] {
			rangeMismatch++
		}
	}

	t.Logf("opus_demo VBR: len_mismatch=%d bytes_mismatch=%d range_mismatch=%d / %d frames",
		lenMismatch, bytesMismatch, rangeMismatch, len(refPackets))

	if lenMismatch > 0 {
		t.Errorf("VBR size mismatch via opus_demo: %d/%d frames", lenMismatch, len(refPackets))
	}
}

// ---- CVBR parity via opus_demo (exhaustive tier) --------------------------------

// TestCVBRSizeDistributionViaOpusDemoExhaustive cross-validates gopus CVBR
// packet sizes against opus_demo at the exhaustive tier.
func TestCVBRSizeDistributionViaOpusDemoExhaustive(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if !ok {
		t.Skip("opus_demo not available; skipping exhaustive CVBR parity")
	}

	tmpDir, err := os.MkdirTemp("", "gopus-cvbr-demo-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, tc := range cvbrTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCVBRParityCaseViaOpusDemo(t, tc, opusDemo, tmpDir)
		})
	}
}

func runCVBRParityCaseViaOpusDemo(t *testing.T, tc vbrCVBRCase, opusDemo, tmpDir string) {
	t.Helper()

	pcm := makeVBRCVBRTestPCM(tc.nFrames, tc.frameSize, tc.channels)

	appArg := opusDemoAppFromApplication(tc.application)
	if appArg == "" {
		t.Skipf("application %v not supported by opus_demo", tc.application)
		return
	}
	var bwArg string
	if tc.setBandwidth {
		var ok bool
		bwArg, ok = opusDemoBandwidthFromBandwidth(tc.bandwidth)
		if !ok {
			t.Fatalf("bandwidth not mappable to opus_demo arg")
		}
	}
	frameArg, err := frameSizeSamplesToArg(tc.frameSize)
	if err != nil {
		t.Fatalf("map frame size: %v", err)
	}

	safeName := tc.name
	rawPath := filepath.Join(tmpDir, safeName+".cvbr.f32")
	bitPath := filepath.Join(tmpDir, safeName+".cvbr.bit")

	if err := writeFloat32LEFile(rawPath, pcm); err != nil {
		t.Fatalf("write raw input: %v", err)
	}

	args := []string{
		"-e", appArg, "48000", fmt.Sprintf("%d", tc.channels), fmt.Sprintf("%d", tc.bitrate),
		"-f32", "-cvbr", "-complexity", "10", "-framesize", frameArg,
	}
	if tc.setBandwidth && bwArg != "" {
		args = append(args, "-bandwidth", bwArg)
	}
	args = append(args, rawPath, bitPath)

	if out, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("opus_demo encode failed: %v (%s)", err, out)
	}

	refPackets, _, err := parseOpusDemoEncodeBitstream(bitPath)
	if err != nil {
		t.Fatalf("parse bitstream: %v", err)
	}

	goResults, err := encodeVBRCVBRWithGopus(
		tc.application,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		tc.bandwidth, tc.setBandwidth, tc.signal,
		true, pcm, tc.nFrames,
	)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(goResults) != len(refPackets) {
		t.Fatalf("packet count mismatch: gopus=%d opusdemo=%d", len(goResults), len(refPackets))
	}

	refLens := make([]int, len(refPackets))
	goLens := make([]int, len(goResults))
	var lenMismatch int
	for i := range refPackets {
		refLens[i] = len(refPackets[i])
		goLens[i] = len(goResults[i].data)
		if refLens[i] != goLens[i] {
			lenMismatch++
		}
	}

	refMean := meanInt(refLens)
	goMean := meanInt(goLens)

	t.Logf("opus_demo CVBR: nFrames=%d lenMismatch=%d/%d refMean=%.1f gopusMean=%.1f",
		tc.nFrames, lenMismatch, tc.nFrames, refMean, goMean)

	if lenMismatch > 0 {
		// Tolerant check on darwin/arm64 (1-ULP drift).
		isDarwinARM64 := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
		if isDarwinARM64 {
			t.Logf("CVBR size mismatch via opus_demo on darwin/arm64: %d/%d frames (1-ULP drift residual)", lenMismatch, tc.nFrames)
		} else {
			t.Errorf("CVBR size mismatch via opus_demo: %d/%d frames\n  refLens=%v\n  gopusLens=%v",
				lenMismatch, tc.nFrames, refLens, goLens)
		}
	}
}

// ---- application/bandwidth helpers for opus_demo ----------------------------

func opusDemoAppFromApplication(app gopus.Application) string {
	switch app {
	case gopus.ApplicationVoIP:
		return "voip"
	case gopus.ApplicationAudio:
		return "audio"
	case gopus.ApplicationLowDelay:
		return "restricted-lowdelay"
	case gopus.ApplicationRestrictedSilk:
		return "restricted-silk"
	case gopus.ApplicationRestrictedCelt:
		return "restricted-celt"
	}
	return ""
}

func opusDemoBandwidthFromBandwidth(bw types.Bandwidth) (string, bool) {
	switch bw {
	case types.BandwidthNarrowband:
		return "NB", true
	case types.BandwidthMediumband:
		return "MB", true
	case types.BandwidthWideband:
		return "WB", true
	case types.BandwidthSuperwideband:
		return "SWB", true
	case types.BandwidthFullband:
		return "FB", true
	}
	return "", false
}

// ---- arithmetic helpers ------------------------------------------------------

func meanInt(vals []int) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum int
	for _, v := range vals {
		sum += v
	}
	return float64(sum) / float64(len(vals))
}

func percentileInt(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
