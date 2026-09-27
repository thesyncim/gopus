//go:build gopus_qext

package gopus_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// compareNative96kDecodeRange compares a half-open sample range of a gopus
// native 96 kHz decode with every raw float32 bit from the selected QEXT
// libopus decoder.
func compareNative96kDecodeRange(t *testing.T, got, want []float32, lo, hi int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sample count: got %d want %d", len(got), len(want))
	}
	if lo < 0 || hi < lo || hi > len(got) {
		t.Fatalf("invalid comparison range [%d,%d) for %d samples", lo, hi, len(got))
	}
	for i := lo; i < hi; i++ {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample[%d]: got %08x want %08x", i,
				math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
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
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
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
	if len(ref.FinalRanges) != len(packets) || len(ref.PCM) != len(packets)*1920*channels {
		t.Fatalf("oracle output geometry: ranges=%d PCM=%d packets=%d channels=%d",
			len(ref.FinalRanges), len(ref.PCM), len(packets), channels)
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
		if n != 1920 {
			t.Fatalf("packet %d decoded %d samples/channel, want 1920", pi, n)
		}
		if got := dec.FinalRange(); got != ref.FinalRanges[pi] {
			t.Fatalf("packet %d final range: got %08x want %08x", pi, got, ref.FinalRanges[pi])
		}
		out = append(out, buf[:n*channels]...)
	}

	// The first 96 kHz frame exercises the full native decode pipeline (base
	// bands + the >20 kHz QEXT extension bands, the 3840-MDCT long synthesis
	// with overlap=240, and the 2-tap HD de-emphasis) with a clean (zero)
	// comb-filter history.
	firstFrame := 1920 * channels
	compareNative96kDecodeRange(t, out, ref.PCM, 0, firstFrame)

	// Remaining frames additionally exercise the cross-frame comb-filter
	// postfilter and the cross-frame QEXT extension-band allocation balance,
	// which carries forward the (signed) leftover ext-coder budget into the
	// next frame's band bit allocation. These frames are a strict sample-parity
	// gate for every float32 bit and packet final range.
	if len(out) > firstFrame {
		compareNative96kDecodeRange(t, out, ref.PCM, firstFrame, len(out))
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

// native96kPostfilterTone is a periodic stereo-compatible signal whose CELT
// packets carry an active pitch postfilter after the first frame.
func native96kPostfilterTone(channels, frames int) []float32 {
	pcm := make([]float32, 1920*frames*channels)
	for i := 0; i < 1920*frames; i++ {
		v := float32(0.4 * math.Sin(2*math.Pi*6000*float64(i)/96000))
		pcm[i*channels] = v
		if channels == 2 {
			pcm[i*channels+1] = 0.9 * v
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
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
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
// libopus reference bit-for-bit on each selected ISA lane. A comb-filter scale
// defect shows up across frames once a prior frame's pitch comb is active.
func TestNative96kDecodeCrossFramePostfilterParity(t *testing.T) {
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			libopustest.RequireOracle(t)
			opusDemo, err := benchutil.QEXTOpusDemoPath()
			if err != nil {
				libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
			}

			const frames = 6
			pcm96 := native96kPostfilterTone(ch, frames)
			packets := encodeNative96kQEXTPackets(t, opusDemo, ch, pcm96, 320000)

			// Confirm the comb is actually exercised: at least one frame after the
			// first must carry an active postfilter, otherwise this test would pass
			// trivially without touching comb_filter_qext.
			activeAfterFirst := 0
			var activePacket []byte
			for i, pkt := range packets {
				if celtFramePostfilterActive(pkt) {
					if i > 0 {
						activeAfterFirst++
						activePacket = pkt
					}
				}
			}
			if activeAfterFirst == 0 {
				t.Fatalf("no cross-frame active postfilter in %d packets; comb path not exercised", len(packets))
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
			if len(ref.FinalRanges) != len(packets) || len(ref.PCM) != len(packets)*1920*ch {
				t.Fatalf("oracle output geometry: ranges=%d PCM=%d packets=%d channels=%d",
					len(ref.FinalRanges), len(ref.PCM), len(packets), ch)
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
				if n != 1920 {
					t.Fatalf("packet %d decoded %d samples/channel, want 1920", pi, n)
				}
				if got := dec.FinalRange(); got != ref.FinalRanges[pi] {
					t.Fatalf("packet %d final range: got %08x want %08x", pi, got, ref.FinalRanges[pi])
				}
				out = append(out, buf[:n*ch]...)
			}

			firstFrame := 1920 * ch
			if len(out) <= firstFrame {
				t.Fatalf("decoded only %d samples; need cross-frame coverage", len(out))
			}
			compareNative96kDecodeRange(t, out, ref.PCM, firstFrame, len(out))
			// The active QEXT decode path reuses its refinement, IMDCT, and comb
			// scratch after the packet history has warmed the decoder.
			for i := 0; i < 2; i++ {
				if n, err := dec.Decode(activePacket, buf); err != nil || n != 1920 {
					t.Fatalf("warm active packet: n=%d err=%v", n, err)
				}
			}
			var warmN int
			var warmErr error
			allocs := testing.AllocsPerRun(20, func() {
				warmN, warmErr = dec.Decode(activePacket, buf)
			})
			if warmErr != nil || warmN != 1920 {
				t.Fatalf("measured active packet: n=%d err=%v", warmN, warmErr)
			}
			if allocs != 0 {
				t.Fatalf("active native 96k decode allocations: %v", allocs)
			}
			activeSignal := false
			for _, sample := range buf {
				bits := math.Float32bits(sample)
				if bits&0x7f800000 != 0x7f800000 && bits&0x7fffffff != 0 {
					activeSignal = true
					break
				}
			}
			if !activeSignal {
				t.Fatal("measured active QEXT packet produced no finite nonzero PCM")
			}
			t.Logf("cross-frame postfilter parity: %d ch, %d active-comb frames after first",
				ch, activeAfterFirst)
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
