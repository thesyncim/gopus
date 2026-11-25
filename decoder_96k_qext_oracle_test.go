//go:build gopus_qext

package gopus_test

import (
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

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

	ref, err := probeQEXTDecodePublicReference(libopustest.QEXTDecode96kParams{
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

			ref, err := probeQEXTDecodePublicReference(libopustest.QEXTDecode96kParams{
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

func TestPublicQEXTNative96ShortCELTMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}

	formats := []struct {
		name string
		kind uint32
	}{
		{name: "float32", kind: libopustest.QEXTDecode96kFormatFloat32},
		{name: "int16", kind: libopustest.QEXTDecode96kFormatInt16},
		{name: "int24", kind: libopustest.QEXTDecode96kFormatInt24},
	}
	for _, duration := range []struct {
		name      string
		arg       string
		frameSize int
	}{
		{name: "10ms", arg: "10", frameSize: 960},
		{name: "5ms", arg: "5", frameSize: 480},
		{name: "2.5ms", arg: "2.5", frameSize: 240},
	} {
		duration := duration
		for _, packetChannels := range []int{1, 2} {
			packetChannels := packetChannels
			packets := encodeNative96kCELTDurationPackets(t, opusDemo, duration.arg,
				duration.frameSize, packetChannels, 3)
			if packetChannels == 1 {
				assertNative96QEXTPayloadAffectsPCM(t, packets, packetChannels, duration.frameSize)
			}
			for _, outputChannels := range []int{1, 2} {
				outputChannels := outputChannels
				layout := map[[2]int]string{
					{1, 1}: "mono-mono", {1, 2}: "mono-stereo",
					{2, 1}: "stereo-mono", {2, 2}: "stereo-stereo",
				}[[2]int{packetChannels, outputChannels}]
				t.Run(duration.name+"/"+layout, func(t *testing.T) {
					for _, format := range formats {
						format := format
						t.Run(format.name, func(t *testing.T) {
							assertNative96QEXTSequence(t, packets, packetChannels, outputChannels,
								duration.frameSize, format.kind)
						})
					}
				})
			}
		}
	}
}

func TestPublicQEXTNative96Hybrid10msMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, packetChannels := range []int{1, 2} {
		packetChannels := packetChannels
		packets := encodeNative96kHybrid10msPackets(t, packetChannels, 3)
		for _, packet := range packets {
			if mode := gopus.ParseTOC(packet[0]).Mode; mode != gopus.ModeHybrid {
				t.Fatalf("selected C packet mode=%v, want Hybrid", mode)
			}
		}
		for _, outputChannels := range []int{1, 2} {
			outputChannels := outputChannels
			layout := map[[2]int]string{
				{1, 1}: "mono-mono", {1, 2}: "mono-stereo",
				{2, 1}: "stereo-mono", {2, 2}: "stereo-stereo",
			}[[2]int{packetChannels, outputChannels}]
			t.Run(layout, func(t *testing.T) {
				for _, format := range []uint32{
					libopustest.QEXTDecode96kFormatFloat32,
					libopustest.QEXTDecode96kFormatInt16,
					libopustest.QEXTDecode96kFormatInt24,
				} {
					format := format
					t.Run(map[uint32]string{
						libopustest.QEXTDecode96kFormatFloat32: "float32",
						libopustest.QEXTDecode96kFormatInt16:   "int16",
						libopustest.QEXTDecode96kFormatInt24:   "int24",
					}[format], func(t *testing.T) {
						assertNative96QEXTSequence(t, packets, packetChannels, outputChannels,
							960, format)
					})
				}
			})
		}
	}
}

func assertNative96QEXTSequence(t *testing.T, packets [][]byte, packetChannels, outputChannels, frameSize int, format uint32) {
	t.Helper()
	want, err := probeQEXTDecodePublicReference(libopustest.QEXTDecode96kParams{
		SampleFormat: format,
		Channels:     outputChannels,
		SampleRate:   96000,
		MaxFrameSize: frameSize,
		Packets:      packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected native96 QEXT decoder", err)
		return
	}
	if len(want.FinalRanges) != len(packets) {
		t.Fatalf("selected C final ranges=%d packets=%d", len(want.FinalRanges), len(packets))
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(96000, outputChannels))
	if err != nil {
		t.Fatal(err)
	}
	count := frameSize * outputChannels
	outF32 := make([]float32, count)
	out16 := make([]int16, count)
	out24 := make([]int32, count)
	decode := func(packet []byte) (int, error) {
		switch format {
		case libopustest.QEXTDecode96kFormatInt16:
			return dec.DecodeInt16(packet, out16)
		case libopustest.QEXTDecode96kFormatInt24:
			return dec.DecodeInt24(packet, out24)
		default:
			return dec.Decode(packet, outF32)
		}
	}
	compare := func(frame int) {
		start := frame * count
		switch format {
		case libopustest.QEXTDecode96kFormatInt16:
			for i, got := range out16 {
				if expected := want.Int16[start+i]; got != expected {
					t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
				}
			}
		case libopustest.QEXTDecode96kFormatInt24:
			for i, got := range out24 {
				if expected := want.Int24[start+i]; got != expected {
					t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
				}
			}
		default:
			for i, got := range outF32 {
				if expected := want.PCM[start+i]; math.Float32bits(got) != math.Float32bits(expected) {
					t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i,
						math.Float32bits(got), math.Float32bits(expected))
				}
			}
		}
	}
	for frame, packet := range packets {
		n, err := decode(packet)
		if err != nil || n != frameSize {
			t.Fatalf("frame %d packetChannels=%d outputChannels=%d samples=%d err=%v want %d,nil",
				frame, packetChannels, outputChannels, n, err, frameSize)
		}
		if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
			t.Fatalf("frame %d FinalRange=%08x C=%08x", frame, got, expected)
		}
		compare(frame)
	}
	dec.Reset()
	n, err := decode(packets[0])
	if err != nil || n != frameSize || dec.FinalRange() != want.FinalRanges[0] {
		t.Fatalf("reset replay samples=%d range=%08x err=%v", n, dec.FinalRange(), err)
	}
	compare(0)
	var decodeErr error
	allocs := testing.AllocsPerRun(20, func() {
		_, decodeErr = decode(packets[0])
	})
	if decodeErr != nil {
		t.Fatalf("warmed decode: %v", decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed native96 decode allocated %g times/call", allocs)
	}
}

func assertNative96QEXTPayloadAffectsPCM(t *testing.T, packets [][]byte, channels, frameSize int) {
	t.Helper()
	params := libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		SampleRate:   96000,
		MaxFrameSize: frameSize,
		Packets:      packets,
	}
	active, err := libopustest.ProbeQEXTDecodeFixed(params)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed-QEXT extension activity probe", err)
		return
	}
	params.IgnoreExtensions = true
	ignored, err := libopustest.ProbeQEXTDecodeFixed(params)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed-QEXT ignored-extension probe", err)
		return
	}
	if len(active.PCM) != len(ignored.PCM) {
		t.Fatalf("QEXT active PCM=%d ignored PCM=%d", len(active.PCM), len(ignored.PCM))
	}
	for i := range active.PCM {
		if math.Float32bits(active.PCM[i]) != math.Float32bits(ignored.PCM[i]) {
			return
		}
	}
	t.Fatal("selected native96 packet sequence has no decoded QEXT contribution")
}

func encodeNative96kCELTDurationPackets(t *testing.T, opusDemo, duration string, frameSize, channels, frames int) [][]byte {
	t.Helper()
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "input.f32")
	bitPath := filepath.Join(tmpDir, "output.bit")
	pcm := native96DurationSignal(frameSize, channels, frames)
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatalf("write native96 CELT input: %v", err)
	}
	args := []string{
		"-e", "restricted-celt", "96000", itoa(channels), "320000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", duration,
		"-qext", "-cbr", inputPath, bitPath,
	}
	cmd := exec.Command(opusDemo, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opus_demo native96 %s CELT encode failed: %v (%s)", duration, err, out)
	}
	packets, err := allOpusDemoPackets(bitPath)
	if err != nil {
		t.Fatalf("read native96 %s CELT packets: %v", duration, err)
	}
	if len(packets) < frames {
		t.Fatalf("native96 %s CELT packets=%d want at least %d", duration, len(packets), frames)
	}
	for i, packet := range packets {
		if gopus.ParseTOC(packet[0]).Mode != gopus.ModeCELT {
			t.Fatalf("native96 %s C packet %d mode=%v want CELT", duration, i, gopus.ParseTOC(packet[0]).Mode)
		}
	}
	return packets
}

func encodeNative96kHybrid10msPackets(t *testing.T, channels, frames int) [][]byte {
	t.Helper()
	inputFrames := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
	for frame := range frames {
		inputFrames[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: native96DurationSignal(960, channels, 1),
			ForceMode: libopustest.OpusForceModeHybrid, Bandwidth: libopustest.OpusBandwidthFullband,
		}
	}
	records, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 96000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, Bitrate: 320000, Complexity: 10, ForceChannels: channels,
		FrameSize: 960, FrameCount: frames,
	}, inputFrames)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed-QEXT native96 Hybrid encoder", err)
		return nil
	}
	packets := make([][]byte, len(records))
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) == 0 {
			t.Fatalf("selected C Hybrid frame %d status=%d packet length=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	return packets
}

func native96DurationSignal(frameSize, channels, frames int) []float32 {
	pcm := make([]float32, frameSize*channels*frames)
	for frame := range frames {
		for i := range frameSize {
			tm := float64(frame*frameSize+i) / 96000
			left := float32(0.28*math.Sin(2*math.Pi*1600*tm+0.03*float64(frame)) +
				0.21*math.Sin(2*math.Pi*27000*tm+0.11))
			pcm[(frame*frameSize+i)*channels] = left
			if channels == 2 {
				pcm[(frame*frameSize+i)*channels+1] = float32(float64(left)*0.87 +
					0.04*math.Sin(2*math.Pi*19000*tm+0.17))
			}
		}
	}
	return pcm
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func TestEncoder100msBudgetMatchesLibopusAt96kQEXT(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 96000
		bitrate    = 64000
	)
	configure := func(enc *gopus.Encoder) error { return enc.SetQEXT(true) }
	for _, channels := range []int{1, 2} {
		for api := encoder100msFloatAPI; api <= encoder100msInt24API; api++ {
			t.Run(fmt.Sprintf("%dch_api%d", channels, api), func(t *testing.T) {
				ops := []encoder100msBudgetOperation{
					{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
					{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 1},
					{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
				}
				got := compareEncoder100msBudgetSequence(t, sampleRate, channels, api, bitrate, ops, configure)
				if got[0].rangeV == 0 {
					t.Fatal("priming packet has zero final range; rejection would not prove range clearing")
				}
				if got[1].status != -2 {
					t.Fatalf("96 kHz 100 ms one-byte status=%d, want OPUS_BUFFER_TOO_SMALL (-2)", got[1].status)
				}
			})
		}
	}

	for api := encoder100msFloatAPI; api <= encoder100msInt24API; api++ {
		t.Run(fmt.Sprintf("budget2_api%d", api), func(t *testing.T) {
			ops := []encoder100msBudgetOperation{
				{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 2},
				{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
			}
			got := compareEncoder100msBudgetSequence(t, sampleRate, 1, api, bitrate, ops, configure)
			if len(got) == len(ops) && (got[0].status < 0 || got[0].status == 0) {
				t.Fatalf("96 kHz 100 ms two-byte status=%d, want a non-empty packet", got[0].status)
			}
		})
	}

	for _, tc := range []struct {
		name           string
		frameSize      int
		expertDuration gopus.ExpertFrameDuration
		wantReject     bool
	}{
		{name: "caller120_selected100", frameSize: 11520, expertDuration: gopus.ExpertFrameDuration100Ms, wantReject: true},
		{name: "caller100_selected20", frameSize: 9600, expertDuration: gopus.ExpertFrameDuration20Ms},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := []encoder100msBudgetOperation{{frameSize: tc.frameSize, expertDuration: tc.expertDuration, budget: 1}}
			got := compareEncoder100msBudgetSequence(t, sampleRate, 1, encoder100msFloatAPI, bitrate, ops, configure)
			if len(got) != 1 {
				return
			}
			if tc.wantReject && got[0].status != -2 {
				t.Fatalf("96 kHz selected 100 ms one-byte status=%d, want -2", got[0].status)
			}
			if !tc.wantReject && (got[0].status < 0 || len(got[0].packet) == 0) {
				t.Fatalf("96 kHz selected 20 ms one-byte status=%d packet=%x, want packet", got[0].status, got[0].packet)
			}
		})
	}
}

func hd96kEncodeSine(channels, frameSize, frameCount int) []float32 {
	const fs = 96000.0
	n := frameSize * channels * frameCount
	pcm := make([]float32, n)
	for f := 0; f < frameCount; f++ {
		for i := 0; i < frameSize; i++ {
			idx := f*frameSize + i
			phase := 2 * math.Pi * 1000 * float64(idx) / fs
			base := (f*frameSize + i) * channels
			pcm[base] = float32(0.4 * math.Sin(phase))
			if channels == 2 {
				pcm[base+1] = float32(0.3 * math.Sin(phase+0.4))
			}
		}
	}
	return pcm
}

// TestQEXTEncode96kOracleAvailable confirms the native 96 kHz QEXT encode oracle
// (opus_encoder_create(96000) + OPUS_SET_QEXT) builds, runs, and produces a
// native code-3 CELT-only fullband packet for both mono and stereo. This pins
// the reference that the native HD96k encode routing is validated against.
func TestQEXTEncode96kOracleAvailable(t *testing.T) {
	const frameSize = 1920
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kEncodeSine(ch, frameSize, 1)
			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       256000,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 4000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			if len(res.Packets) != 1 || len(res.Packets[0]) == 0 {
				t.Fatalf("oracle produced no packet")
			}
			pkt := res.Packets[0]
			toc := pkt[0]
			// CELT-only fullband 20 ms -> config 31, code 3: TOC 0xfb.
			if toc&0x03 != 3 {
				t.Errorf("native 96k packet TOC code = %d, want 3 (got toc=0x%02x)", toc&0x03, toc)
			}
			t.Logf("native 96k QEXT %d-ch packet: len=%d toc=0x%02x finalRange=%d",
				ch, len(pkt), toc, res.FinalRanges[0])
		})
	}
}
