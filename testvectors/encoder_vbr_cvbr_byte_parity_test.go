// VBR and CVBR parity tests against a libopus build selected for the active
// feature set and instruction lane. The direct C gates compare packet bytes
// and final ranges. The opus_demo CVBR gate reproduces its float-input
// conversion to int24 before comparing the public encoders.
package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
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
	path, err := vbrCVBREncodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "vbr-cvbr encode",
			OutputBase: "gopus_libopus_vbr_cvbr_encode",
			SourceFile: "libopus_vbr_cvbr_encode_info.c",
			CFlags:     []string{"-O2", "-DNDEBUG"},
		})
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
	enc, err := newVBRCVBREncoder(application, sampleRate, channels, frameSize, bitrate,
		bandwidth, setBandwidth, signal, vbrConstraint, false)
	if err != nil {
		return nil, err
	}

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

func newVBRCVBREncoder(
	application gopus.Application,
	sampleRate, channels, frameSize, bitrate int,
	bandwidth types.Bandwidth,
	setBandwidth bool,
	signal types.Signal,
	vbrConstraint bool,
	opusDemoInput bool,
) (*gopus.Encoder, error) {
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
	if opusDemoInput {
		// opus_demo.c sets OPUS_SET_LSB_DEPTH(24) for FORMAT_F32_LE before it
		// quantizes the input and calls opus_encode24.
		if err := enc.SetLSBDepth(24); err != nil {
			return nil, fmt.Errorf("set input LSB depth: %w", err)
		}
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(vbrConstraint)
	return enc, nil
}

func quantizeOpusDemoFloatInputToInt24(pcm []float32) []int32 {
	pcm24 := make([]int32, len(pcm))
	for i, sample := range pcm {
		// src/opus_demo.c FORMAT_F32_LE computes floor(.5 + sample*8388608)
		// and passes the result to opus_encode24.
		pcm24[i] = int32(math.Floor(0.5 + float64(sample)*8388608.0))
	}
	return pcm24
}

func encodeOpusDemoInt24(
	application gopus.Application,
	sampleRate, channels, frameSize, bitrate int,
	bandwidth types.Bandwidth,
	setBandwidth bool,
	vbrConstraint bool,
	pcm []float32,
	nFrames int,
) ([]oracleResult, error) {
	enc, err := newVBRCVBREncoder(application, sampleRate, channels, frameSize, bitrate,
		bandwidth, setBandwidth, types.SignalAuto, vbrConstraint, true)
	if err != nil {
		return nil, err
	}
	pcm24 := quantizeOpusDemoFloatInputToInt24(pcm)
	samplesPerFrame := frameSize * channels
	buf := make([]byte, 4000)
	results := make([]oracleResult, 0, nFrames)
	for i := range nFrames {
		frame := pcm24[i*samplesPerFrame : (i+1)*samplesPerFrame]
		n, err := enc.EncodeInt24(frame, buf)
		if err != nil {
			return nil, fmt.Errorf("encode int24 frame %d: %w", i, err)
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
// and the feature/ISA-paired libopus oracle. Every packet and final-range value
// must match exactly.
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

	if mismatchLen > 0 {
		refLens := make([]int, len(refResults))
		goLens := make([]int, len(goResults))
		for i := range refResults {
			refLens[i] = len(refResults[i].data)
			goLens[i] = len(goResults[i].data)
		}

		t.Fatalf("VBR packet length mismatch: mismatch=%d/%d firstAtFrame=%d\n  refLens=%v\n  gopusLens=%v",
			mismatchLen, tc.nFrames, firstMismatch, refLens, goLens)
	}

	if mismatchBytes > 0 {
		t.Fatalf("VBR packet bytes mismatch: mismatch=%d/%d firstAtFrame=%d", mismatchBytes, tc.nFrames, firstMismatch)
	}
	if mismatchRange > 0 {
		t.Fatalf("VBR final-range mismatch: %d/%d frames", mismatchRange, tc.nFrames)
	}
	t.Logf("VBR packets and final ranges match: all %d frames", tc.nFrames)
}

// ---- CVBR packet-size distribution parity test --------------------------------

// TestCVBRSizeDistributionAgainstLibopus verifies that the per-frame packet-size
// sequence from gopus (CVBR mode) matches libopus across a multi-frame stream.
//
// The CVBR reservoir logic in celt/encoder.go must track the libopus
// celt_encoder.c vbr_offset / vbr_count / nb_bits_budget path exactly.
//
// The test compares per-frame packet bytes, lengths, and final ranges.
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
	var lenMismatch, bytesMismatch, rangeMismatch int
	firstLenMismatch, firstBytesMismatch, firstRangeMismatch := -1, -1, -1
	for i := range refResults {
		refLens[i] = len(refResults[i].data)
		goLens[i] = len(goResults[i].data)
		if refLens[i] != goLens[i] {
			lenMismatch++
			if firstLenMismatch == -1 {
				firstLenMismatch = i
			}
		} else if !bytes.Equal(refResults[i].data, goResults[i].data) {
			bytesMismatch++
			if firstBytesMismatch == -1 {
				firstBytesMismatch = i
			}
		}
		if refResults[i].finalRange != goResults[i].finalRange {
			rangeMismatch++
			if firstRangeMismatch == -1 {
				firstRangeMismatch = i
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

	if lenMismatch > 0 || bytesMismatch > 0 || rangeMismatch > 0 {
		// Log a per-frame diff for the first mismatching region.
		firstMismatch := firstLenMismatch
		if firstMismatch < 0 || (firstBytesMismatch >= 0 && firstBytesMismatch < firstMismatch) {
			firstMismatch = firstBytesMismatch
		}
		if firstMismatch < 0 || (firstRangeMismatch >= 0 && firstRangeMismatch < firstMismatch) {
			firstMismatch = firstRangeMismatch
		}
		limit := min(firstMismatch+5, len(refLens))
		start := max(firstMismatch-2, 0)
		t.Logf("  first mismatch region (frames %d..%d):", start, limit-1)
		for i := start; i < limit; i++ {
			mark := ""
			if refLens[i] != goLens[i] || !bytes.Equal(refResults[i].data, goResults[i].data) ||
				refResults[i].finalRange != goResults[i].finalRange {
				mark = fmt.Sprintf(" <-- MISMATCH len=%t bytes=%t range=%t",
					refLens[i] != goLens[i], !bytes.Equal(refResults[i].data, goResults[i].data),
					refResults[i].finalRange != goResults[i].finalRange)
			}
			t.Logf("    frame[%d]: libopus=%d gopus=%d%s", i, refLens[i], goLens[i], mark)
		}

		t.Errorf("CVBR exact parity failed: size=%d/%d first=%d bytes=%d/%d first=%d finalRange=%d/%d first=%d\n  refLens=%v\n  gopusLens=%v",
			lenMismatch, tc.nFrames, firstLenMismatch, bytesMismatch, tc.nFrames, firstBytesMismatch,
			rangeMismatch, tc.nFrames, firstRangeMismatch, refLens, goLens)
		return
	}

	t.Logf("CVBR packets and final ranges match: all %d frames", tc.nFrames)
}

// ---- VBR parity via opus_demo (exhaustive tier) --------------------------------

// TestVBRByteParityViaOpusDemoExhaustive compares public gopus VBR packets and
// final ranges with the feature/ISA-matched public opus_demo. Both sides use
// the int24 samples that opus_demo derives from its -f32 input.
func TestVBRByteParityViaOpusDemoExhaustive(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierExhaustive)
	libopustest.RequireOracle(t)

	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "public API opus_demo", err)
		return
	}

	for _, tc := range vbrTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runVBRParityCaseViaOpusDemo(t, tc, opusDemo, t.TempDir())
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

	goPCM := make([]float32, len(pcm)+tc.frameSize*tc.channels)
	copy(goPCM, pcm)
	goResults, err := encodeOpusDemoInt24(
		tc.application,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		tc.bandwidth, tc.setBandwidth,
		false, goPCM, tc.nFrames+1,
	)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(goResults) != len(refPackets) {
		t.Fatalf("packet count mismatch: gopus=%d opusdemo=%d", len(goResults), len(refPackets))
	}
	var lenMismatch, bytesMismatch, rangeMismatch int
	firstLenMismatch, firstBytesMismatch, firstRangeMismatch := -1, -1, -1
	for i := range refPackets {
		if len(goResults[i].data) != len(refPackets[i]) {
			lenMismatch++
			if firstLenMismatch < 0 {
				firstLenMismatch = i
			}
		} else if !bytes.Equal(goResults[i].data, refPackets[i]) {
			bytesMismatch++
			if firstBytesMismatch < 0 {
				firstBytesMismatch = i
			}
		}
		if goResults[i].finalRange != refRanges[i] {
			rangeMismatch++
			if firstRangeMismatch < 0 {
				firstRangeMismatch = i
			}
		}
	}

	t.Logf("opus_demo VBR: inputFrames=%d encodedFrames=%d lenMismatch=%d bytesMismatch=%d rangeMismatch=%d",
		tc.nFrames, len(refPackets), lenMismatch, bytesMismatch, rangeMismatch)
	if lenMismatch > 0 || bytesMismatch > 0 || rangeMismatch > 0 {
		t.Errorf("VBR exact parity failed: size=%d/%d first=%d bytes=%d/%d first=%d finalRange=%d/%d first=%d",
			lenMismatch, len(refPackets), firstLenMismatch, bytesMismatch, len(refPackets), firstBytesMismatch,
			rangeMismatch, len(refPackets), firstRangeMismatch)
	}
}

// ---- CVBR parity via opus_demo (exhaustive tier) --------------------------------

// TestCVBRSizeDistributionViaOpusDemoExhaustive compares gopus CVBR packets,
// final ranges, and sizes with the feature/ISA-matched public opus_demo. Both
// sides receive the same int24 samples that opus_demo derives from its -f32 input.
func TestCVBRSizeDistributionViaOpusDemoExhaustive(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierExhaustive)
	libopustest.RequireOracle(t)

	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "public API opus_demo", err)
		return
	}

	for _, tc := range cvbrTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCVBRParityCaseViaOpusDemo(t, tc, opusDemo, t.TempDir())
		})
	}
}

func runCVBRParityCaseViaOpusDemo(t *testing.T, tc vbrCVBRCase, opusDemo, tmpDir string) {
	t.Helper()

	pcm := makeVBRCVBRTestPCM(tc.nFrames, tc.frameSize, tc.channels)
	// opus_demo.c emits one final zero-padded frame when fread reaches EOF after
	// an exact number of frames. Include that frame in Go's stream so the packet
	// and reservoir comparison covers the same encoder calls.
	goPCM := make([]float32, len(pcm)+tc.frameSize*tc.channels)
	copy(goPCM, pcm)

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

	refPackets, refRanges, err := parseOpusDemoEncodeBitstream(bitPath)
	if err != nil {
		t.Fatalf("parse bitstream: %v", err)
	}

	goResults, err := encodeOpusDemoInt24(
		tc.application,
		48000, tc.channels, tc.frameSize, tc.bitrate,
		tc.bandwidth, tc.setBandwidth,
		true, goPCM, tc.nFrames+1,
	)
	if err != nil {
		t.Fatalf("gopus encode: %v", err)
	}

	if len(goResults) != len(refPackets) {
		t.Fatalf("packet count mismatch: gopus=%d opusdemo=%d", len(goResults), len(refPackets))
	}
	encodedFrames := len(refPackets)

	refLens := make([]int, len(refPackets))
	goLens := make([]int, len(goResults))
	var lenMismatch, bytesMismatch, rangeMismatch int
	firstLenMismatch, firstBytesMismatch, firstRangeMismatch := -1, -1, -1
	for i := range refPackets {
		refLens[i] = len(refPackets[i])
		goLens[i] = len(goResults[i].data)
		if refLens[i] != goLens[i] {
			lenMismatch++
			if firstLenMismatch < 0 {
				firstLenMismatch = i
			}
		} else if !bytes.Equal(refPackets[i], goResults[i].data) {
			bytesMismatch++
			if firstBytesMismatch < 0 {
				firstBytesMismatch = i
			}
		}
		if refRanges[i] != goResults[i].finalRange {
			rangeMismatch++
			if firstRangeMismatch < 0 {
				firstRangeMismatch = i
			}
		}
	}

	refMean := meanInt(refLens)
	goMean := meanInt(goLens)

	t.Logf("opus_demo CVBR: inputFrames=%d encodedFrames=%d lenMismatch=%d/%d bytesMismatch=%d/%d rangeMismatch=%d/%d refMean=%.1f gopusMean=%.1f",
		tc.nFrames, encodedFrames, lenMismatch, encodedFrames, bytesMismatch, encodedFrames, rangeMismatch, encodedFrames, refMean, goMean)
	if lenMismatch > 0 || bytesMismatch > 0 || rangeMismatch > 0 {
		t.Errorf("CVBR exact parity failed: size=%d/%d first=%d bytes=%d/%d first=%d finalRange=%d/%d first=%d\n  refLens=%v\n  gopusLens=%v",
			lenMismatch, encodedFrames, firstLenMismatch, bytesMismatch, encodedFrames, firstBytesMismatch,
			rangeMismatch, encodedFrames, firstRangeMismatch, refLens, goLens)
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
