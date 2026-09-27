//go:build gopus_qext

package gopus_test

import (
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

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
