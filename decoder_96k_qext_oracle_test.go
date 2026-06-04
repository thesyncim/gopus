//go:build gopus_qext

package gopus_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// hd96kDecodeArm64Tol bounds the documented darwin/arm64 CELT cosine/rsqrt
// kernel residual (project_arm64_celt_1ulp_drift.md) on the native 96 kHz
// decode output. On amd64 (CI hard gate) the native decode must match the
// QEXT libopus reference sample-for-sample; arm64 logs a bounded residual.
const hd96kDecodeArm64Tol = float32(2e-4)

// compareNative96kDecodeRange compares a half-open sample range of a gopus
// native 96 kHz decode against the QEXT libopus reference, enforcing exact
// equality on amd64 and a bounded residual on arm64. It returns the max abs
// difference observed.
func compareNative96kDecodeRange(t *testing.T, got, want []float32, lo, hi int, strict bool) float32 {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sample count: got %d want %d", len(got), len(want))
	}
	if hi > len(got) {
		hi = len(got)
	}
	isArm64 := runtime.GOARCH == "arm64"
	var maxResidual float32
	maxIdx := -1
	for i := lo; i < hi; i++ {
		if got[i] == want[i] {
			continue
		}
		diff := got[i] - want[i]
		if diff < 0 {
			diff = -diff
		}
		if strict && !isArm64 {
			t.Fatalf("sample[%d]: got %v want %v (diff %v, amd64 must be exact)", i, got[i], want[i], got[i]-want[i])
		}
		if diff > maxResidual {
			maxResidual = diff
			maxIdx = i
		}
	}
	if strict && isArm64 && maxResidual > 0 {
		if maxResidual > hd96kDecodeArm64Tol {
			t.Fatalf("arm64 residual %v at index %d exceeds budget %v", maxResidual, maxIdx, hd96kDecodeArm64Tol)
		}
		t.Logf("RESIDUAL arm64 CELT-kernel drift on native 96k decode: max %v at index %d (<= %v, project_arm64_celt_1ulp_drift.md)", maxResidual, maxIdx, hd96kDecodeArm64Tol)
	}
	return maxResidual
}

// TestNative96kDecodeMatchesQEXTOracleMono drives a real native 96 kHz QEXT
// bitstream through the gopus public decoder at Fs=96000 (native HD96k CELT
// mode, no resample) and requires sample parity with the QEXT-enabled libopus
// reference decoded at Fs=96000.
func TestNative96kDecodeMatchesQEXTOracleMono(t *testing.T) {
	testNative96kDecodeMatchesQEXTOracle(t, 1)
}

// TestNative96kDecodeMatchesQEXTOracleStereo is the stereo counterpart.
func TestNative96kDecodeMatchesQEXTOracleStereo(t *testing.T) {
	testNative96kDecodeMatchesQEXTOracle(t, 2)
}

func testNative96kDecodeMatchesQEXTOracle(t *testing.T, channels int) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	const frames = 6
	pcm96 := native96kSine(channels, frames)
	packets := encodeNative96kQEXTPackets(t, opusDemo, channels, pcm96, 320000)

	ref, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		MaxFrameSize: 1920,
		Packets:      packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "qext decode96k", err)
	}
	if len(ref.PCM) == 0 {
		t.Fatal("oracle returned no PCM")
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatalf("NewDecoder(96000, %d): %v", channels, err)
	}

	out := make([]float32, 0, len(ref.PCM))
	buf := make([]float32, 1920*channels)
	for pi, pkt := range packets {
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("packet %d decode: %v", pi, err)
		}
		out = append(out, buf[:n*channels]...)
	}

	// The first 96 kHz frame exercises the full native decode pipeline (base
	// bands + the >20 kHz QEXT extension bands, the 3840-MDCT long synthesis
	// with overlap=240, and the 2-tap HD de-emphasis) with a clean (zero)
	// comb-filter history.
	firstFrame := 1920 * channels
	compareNative96kDecodeRange(t, out, ref.PCM, 0, firstFrame, true)

	// Remaining frames additionally exercise the cross-frame comb-filter
	// postfilter and the cross-frame QEXT extension-band allocation balance,
	// which carries forward the (signed) leftover ext-coder budget into the
	// next frame's band bit allocation. These frames are a strict sample-parity
	// gate: amd64 must match the QEXT libopus reference exactly, arm64 within
	// the documented CELT-kernel residual budget.
	if len(out) > firstFrame {
		compareNative96kDecodeRange(t, out, ref.PCM, firstFrame, len(out), true)
	}
	t.Logf("native 96k decode parity: %d ch, %d packets, %d samples (all frames strict)", channels, len(packets), len(out))
}

// allOpusDemoPackets parses every packet from an opus_demo bitstream file.
// Each record is: u32 big-endian length, u32 big-endian final range, payload.
func allOpusDemoPackets(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var packets [][]byte
	off := 0
	for off+8 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[off : off+4]))
		off += 8 // skip length + final range
		if n < 0 || off+n > len(data) {
			return nil, os.ErrInvalid
		}
		packets = append(packets, append([]byte(nil), data[off:off+n]...))
		off += n
	}
	if len(packets) == 0 {
		return nil, os.ErrInvalid
	}
	return packets, nil
}

// encodeNative96kQEXTPackets encodes a native 96 kHz QEXT bitstream via the
// QEXT-enabled opus_demo (sampling rate 96000) and returns all packets.
func encodeNative96kQEXTPackets(t *testing.T, opusDemo string, channels int, pcm96 []float32, bitrate int) [][]byte {
	t.Helper()
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "in96.f32")
	bitPath := filepath.Join(tmpDir, "out96.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm96, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}
	args := []string{
		"-e", "restricted-celt", "96000", fmt.Sprint(channels), fmt.Sprint(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
		"-qext", "-cbr", inputPath, bitPath,
	}
	cmd := exec.Command(opusDemo, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opus_demo 96k encode failed: %v (%s)", err, out)
	}
	packets, err := allOpusDemoPackets(bitPath)
	if err != nil {
		t.Fatalf("allOpusDemoPackets: %v", err)
	}
	return packets
}

// native96kSine returns frames worth of a tone above 24 kHz (a frequency that
// only the native 96 kHz path can represent) plus a low tone, so a correct
// native decode and a 2:1-resample-of-48k decode are distinguishable.
func native96kSine(channels, frames int) []float32 {
	n := 1920 * frames
	pcm := make([]float32, n*channels)
	for i := 0; i < n; i++ {
		v := 0.30*math.Sin(2*math.Pi*6000*float64(i)/96000.0) +
			0.25*math.Sin(2*math.Pi*30000*float64(i)/96000.0)
		pcm[i*channels] = float32(v)
		if channels == 2 {
			pcm[i*channels+1] = float32(0.9 * v)
		}
	}
	return pcm
}

// TestQEXTDecode96kOracleProducesNative96k validates the new native 96 kHz QEXT
// full-packet decode oracle end-to-end: a real native 96 kHz QEXT bitstream
// (produced by the QEXT opus_demo at Fs=96000, 1920-sample frames) is decoded
// through the QEXT-enabled libopus reference at Fs=96000 and the oracle returns
// native 96 kHz PCM (1920 samples/frame) carrying real >24 kHz energy.
func TestQEXTDecode96kOracleProducesNative96k(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	const channels = 1
	const frames = 4
	pcm96 := native96kSine(channels, frames)
	packets := encodeNative96kQEXTPackets(t, opusDemo, channels, pcm96, 320000)

	res, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		MaxFrameSize: 1920,
		Packets:      packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "qext decode96k", err)
	}

	if len(res.PCM) == 0 {
		t.Fatal("oracle returned no PCM")
	}
	if len(res.FinalRanges) != len(packets) {
		t.Fatalf("final ranges: got %d want %d", len(res.FinalRanges), len(packets))
	}

	// Native 96 kHz decode must yield 1920 samples/frame/channel.
	samplesPerCh := len(res.PCM) / channels
	if samplesPerCh%1920 != 0 {
		t.Errorf("decoded %d samples/ch, not a multiple of native 1920", samplesPerCh)
	}

	// Verify the decode carries real high-frequency (>24 kHz) energy: a 2:1
	// resample of a 48 kHz decode could not. Use a coarse Goertzel at 30 kHz.
	mono := make([]float64, samplesPerCh)
	for i := 0; i < samplesPerCh; i++ {
		mono[i] = float64(res.PCM[i*channels])
	}
	mag := goertzelMag(mono, 30000.0, 96000.0)
	total := 0.0
	for _, v := range mono {
		total += v * v
	}
	if total == 0 {
		t.Fatal("decoded audio is all zero")
	}
	if mag <= 0 {
		t.Errorf("no measurable 30 kHz energy in native 96 kHz decode (mag=%g)", mag)
	}
	t.Logf("native 96k decode: %d frames, %d samples/ch, 30kHz mag=%.4g, finalRanges=%v",
		samplesPerCh/1920, samplesPerCh, mag, res.FinalRanges)
}

// TestNative96kDecodeCrossFramePostfilterParity pins the native 96 kHz
// comb-filter postfilter (libopus comb_filter_qext) across frames. It requires
// the decoded stream to genuinely exercise an active pitch comb (postfilter flag
// set on at least one frame after the first, so the cross-frame comb history is
// loaded) and then enforces that every frame after the first matches the QEXT
// libopus reference sample-for-sample on amd64 (the documented arm64 CELT-kernel
// budget otherwise). A non-zero comb-filter scale defect shows up here as a
// large cross-frame residual once a prior frame's pitch comb is active.
func TestNative96kDecodeCrossFramePostfilterParity(t *testing.T) {
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			libopustest.RequireOracle(t)
			opusDemo, err := benchutil.QEXTOpusDemoPath()
			if err != nil {
				t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
			}

			const frames = 6
			pcm96 := native96kSine(ch, frames)
			packets := encodeNative96kQEXTPackets(t, opusDemo, ch, pcm96, 320000)

			// Confirm the comb is actually exercised: at least one frame after the
			// first must carry an active postfilter, otherwise this test would pass
			// trivially without touching comb_filter_qext.
			activeAfterFirst := 0
			for i, pkt := range packets {
				if celtFramePostfilterActive(pkt) {
					if i > 0 {
						activeAfterFirst++
					}
				}
			}
			if activeAfterFirst == 0 {
				t.Skipf("no cross-frame active postfilter in %d packets; comb path not exercised", len(packets))
			}

			ref, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     ch,
				MaxFrameSize: 1920,
				Packets:      packets,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "qext decode96k", err)
			}
			if len(ref.PCM) == 0 {
				t.Fatal("oracle returned no PCM")
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, ch))
			if err != nil {
				t.Fatalf("NewDecoder(96000, %d): %v", ch, err)
			}
			out := make([]float32, 0, len(ref.PCM))
			buf := make([]float32, 1920*ch)
			for pi, pkt := range packets {
				n, derr := dec.Decode(pkt, buf)
				if derr != nil {
					t.Fatalf("packet %d decode: %v", pi, derr)
				}
				out = append(out, buf[:n*ch]...)
			}

			firstFrame := 1920 * ch
			if len(out) <= firstFrame {
				t.Fatalf("decoded only %d samples; need cross-frame coverage", len(out))
			}
			res := compareNative96kDecodeRange(t, out, ref.PCM, firstFrame, len(out), true)
			t.Logf("cross-frame postfilter parity: %d ch, %d active-comb frames after first, max residual %v",
				ch, activeAfterFirst, res)
		})
	}
}

// celtFramePostfilterActive reports whether the CELT main payload of a native
// 96 kHz code-3 single-frame packet has its postfilter flag set (silence=0,
// postfilter=1). It mirrors the leading CELT header bit decode.
func celtFramePostfilterActive(pkt []byte) bool {
	if len(pkt) < 2 || pkt[0]&0x03 != 3 {
		return false
	}
	fc := pkt[1]
	hasPad := fc&0x40 != 0
	if int(fc&0x3f) != 1 {
		return false
	}
	offset := 2
	padding := 0
	if hasPad {
		for offset < len(pkt) {
			b := int(pkt[offset])
			offset++
			if b == 255 {
				padding += 254
				continue
			}
			padding += b
			break
		}
	}
	end := len(pkt) - padding
	if end <= offset {
		return false
	}
	main := pkt[offset:end]
	var d rangecoding.Decoder
	d.Init(main)
	if d.DecodeBit(15) != 0 { // silence
		return false
	}
	return d.DecodeBit(1) != 0 // postfilter
}

func goertzelMag(x []float64, freq, fs float64) float64 {
	if len(x) == 0 {
		return 0
	}
	w := 2 * math.Pi * freq / fs
	c := 2 * math.Cos(w)
	var s0, s1, s2 float64
	for _, v := range x {
		s0 = v + c*s1 - s2
		s2 = s1
		s1 = s0
	}
	return math.Sqrt(s1*s1 + s2*s2 - c*s1*s2)
}
