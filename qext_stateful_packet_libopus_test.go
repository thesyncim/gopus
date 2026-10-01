//go:build gopus_qext

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTStatefulCVBRPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 96000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStatefulCVBR128kPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 128000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStatefulVBRPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 128000, BitrateModeVBR, "")
}

func TestQEXTStateful5msCubicPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 2, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStateful5msCBRTargetTruncationMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 256000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful5msFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStatefulStereoFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr", true)
}

func TestQEXTStateful10msFixedStoragePacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 480, 3, 1, 256000, BitrateModeVBR, "", true)
}

func TestQEXTStateful5msStereoThetaRDOPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 2, 128000, BitrateModeCVBR, "-cvbr", false)
}

func TestQEXTStateful40msCBRPaddingMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 96000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful40msVBRSubframeBudgetMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStateful40msVBRSubframeBudgetCapBoundaryMatchesLibopus(t *testing.T) {
	// The two-frame cap puts curr_max below and above the 254-byte divisor.
	for _, capBytes := range []int{510, 514} {
		t.Run(strconv.Itoa(capBytes), func(t *testing.T) {
			testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 128000, BitrateModeVBR, "", true, capBytes)
		})
	}
}

func TestQEXTStateful60msCBRPaddingMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 2880, 3, 1, 96000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful40msCappedCBRPaddingMatchesLibopus(t *testing.T) {
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 96000, BitrateModeCBR, "-cbr", true, 477)
	for frame, packet := range packets {
		if len(packet) != 477 {
			t.Fatalf("capped C frame %d length %d, want 477", frame, len(packet))
		}
	}
}

func TestQEXTStatefulPacketMatrixMatchesLibopus(t *testing.T) {
	rows := []struct {
		frameSize int
		bitrates  []int
	}{
		{240, []int{128000, 256000}},
		{480, []int{128000, 256000}},
		{960, []int{96000, 128000, 256000}},
		{1920, []int{96000, 128000}},
		{2880, []int{96000}},
	}
	modes := []struct {
		mode BitrateMode
		arg  string
	}{
		{BitrateModeCBR, "-cbr"},
		{BitrateModeCVBR, "-cvbr"},
		{BitrateModeVBR, ""},
	}
	configurations := 0
	for _, channels := range []int{1, 2} {
		for _, row := range rows {
			for _, bitrate := range row.bitrates {
				for _, mode := range modes {
					name := fmt.Sprintf("channels%d_frame%d_bitrate%d_mode%d", channels, row.frameSize, bitrate, mode.mode)
					t.Run(name, func(t *testing.T) {
						// Presence may vary by frame; every packet and decoded sample stays exact.
						packets := testQEXTStatefulPacketsWithSizeAndExtensionCheck(t, row.frameSize, 3, channels, bitrate, mode.mode, mode.arg, nil)
						compareQEXTDecodeSequenceWithLibopus(t, channels, row.frameSize, packets)
					})
					configurations++
				}
			}
		}
	}
	if configurations != 60 {
		t.Fatalf("QEXT matrix has %d configurations, want 60", configurations)
	}
}

func testQEXTStatefulPacketsMatchLibopus(t *testing.T, bitrate int, mode BitrateMode, modeArg string) {
	t.Helper()
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 1, bitrate, mode, modeArg, true)
}

func testQEXTStatefulPacketsWithSizeMatchLibopus(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string, expectExtension bool, caps ...int) [][]byte {
	t.Helper()
	return testQEXTStatefulPacketsWithSizeAndExtensionCheck(t, frameSize, frames, channels, bitrate, mode, modeArg, &expectExtension, caps...)
}

func testQEXTStatefulPacketsWithSizeAndExtensionCheck(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string, expectedExtension *bool, caps ...int) [][]byte {
	t.Helper()
	maxPayload := 1276
	if len(caps) > 0 {
		maxPayload = caps[0]
	}
	libopustest.RequireOracle(t)
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired QEXT opus_demo", err)
		return nil
	}

	pcm := make([]float32, frameSize*frames*channels)
	state := uint32(0xadd44317)
	for i := range frameSize * frames {
		phase := 2 * math.Pi * (697*float64(i)/48000 + 101*float64(i*i)/(48000*48000))
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		noise := (float64(state&0xffff)/32768 - 1) * 0.08
		for c := range channels {
			pcm[i*channels+c] = float32(0.29*math.Sin(phase+float64(c)*0.43) + 0.16*math.Sin(phase*2.7+float64(c)*0.19) + noise)
		}
	}

	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.f32")
	bitstreamPath := filepath.Join(dir, "output.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatal(err)
	}
	args := []string{"-e", "restricted-celt", "48000", strconv.Itoa(channels), strconv.Itoa(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", strconv.Itoa(frameSize / 48),
		"-max_payload", strconv.Itoa(maxPayload), "-qext"}
	if modeArg != "" {
		args = append(args, modeArg)
	}
	args = append(args, inputPath, bitstreamPath)
	if out, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("libopus QEXT encode: %v (%s)", err, out)
	}
	bitstream, err := os.ReadFile(bitstreamPath)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(mode); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm24 := make([]int32, frameSize*channels)
	packet := make([]byte, maxPayload)
	refPackets := make([][]byte, 0, frames)
	offset := 0
	for frame := range frames {
		if len(bitstream)-offset < 8 {
			t.Fatalf("libopus has fewer than %d packet records", frames)
		}
		refLen := int(binary.BigEndian.Uint32(bitstream[offset:]))
		refRange := binary.BigEndian.Uint32(bitstream[offset+4:])
		offset += 8
		if refLen <= 0 || refLen > len(bitstream)-offset {
			t.Fatalf("libopus frame %d has invalid length %d", frame, refLen)
		}
		refPacket := bitstream[offset : offset+refLen]
		offset += refLen
		refPackets = append(refPackets, append([]byte(nil), refPacket...))
		if frameSize > 960 {
			_, _, padding, packetFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("libopus frame %d framing: %v", frame, err)
			}
			_, present, err := findPacketExtension(padding, packetFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("libopus frame %d extension: %v", frame, err)
			}
			if expectedExtension != nil && present != *expectedExtension {
				t.Fatalf("libopus frame %d extension present=%t, want %t", frame, present, *expectedExtension)
			}
		} else if _, _, _, present, ok := qextParseExtensionRegion(refPacket); !ok {
			t.Fatalf("libopus frame %d extension region invalid", frame)
		} else if expectedExtension != nil && present != *expectedExtension {
			t.Fatalf("libopus frame %d extension present=%t, want %t", frame, present, *expectedExtension)
		}

		for i, sample := range pcm[frame*frameSize*channels : (frame+1)*frameSize*channels] {
			// opus_demo -f32 uses this signed-24 conversion before opus_encode24.
			pcm24[i] = int32(math.Floor(0.5 + float64(sample*8388608)))
		}
		gotLen, err := enc.EncodeInt24(pcm24, packet)
		if err != nil {
			t.Fatalf("Go frame %d encode: %v", frame, err)
		}
		if gotLen < 0 || gotLen > len(packet) {
			t.Fatalf("Go frame %d length %d exceeds caller cap %d", frame, gotLen, len(packet))
		}
		if gotLen != refLen || !bytes.Equal(packet[:gotLen], refPacket) {
			t.Errorf("frame %d packet: first difference %d, Go length %d, C length %d", frame, firstDiffByte(packet[:gotLen], refPacket), gotLen, refLen)
		}
		if gotRange := enc.FinalRange(); gotRange != refRange {
			t.Errorf("frame %d final range: Go %08x, C %08x", frame, gotRange, refRange)
		}
	}
	if !t.Failed() {
		t.Logf("%d complete QEXT packets and final ranges match paired libopus", frames)
	}
	return refPackets
}

func TestQEXTStatefulEncodeInt24SteadyAllocations(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(BitrateModeCVBR); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int32, 960)
	for i := range pcm {
		pcm[i] = int32(math.Sin(2*math.Pi*997*float64(i)/48000) * 0.4 * 8388608)
	}
	packet := make([]byte, 1276)
	for range 4 {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, present, ok := qextParseExtensionRegion(packet[:n]); !ok || !present {
			t.Fatal("QEXT extension inactive during allocation warmup")
		}
	}
	var encodeErr error
	lastLen := 0
	allocs := testing.AllocsPerRun(100, func() {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil && encodeErr == nil {
			encodeErr = err
		}
		lastLen = n
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed QEXT EncodeInt24 allocated %.1f times", allocs)
	}
	if _, _, _, present, ok := qextParseExtensionRegion(packet[:lastLen]); !ok || !present {
		t.Fatal("QEXT extension inactive after allocation measurement")
	}
}

func TestQEXTStateful40msCBRCallerBufferSteadyAllocations(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(1920); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int32, 1920)
	for i := range pcm {
		pcm[i] = int32(math.Sin(2*math.Pi*697*float64(i)/48000) * 0.38 * 8388608)
	}
	packet := make([]byte, 1276)
	assertPacket := func(n int) {
		t.Helper()
		if n != 480 {
			t.Fatalf("40 ms CBR packet length %d, want 480", n)
		}
		_, _, padding, frames, err := parsePacketFramesAndPadding(packet[:n])
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := findPacketExtension(padding, frames, qextPacketExtensionID)
		if err != nil || !present {
			t.Fatalf("active QEXT extension: present=%t err=%v", present, err)
		}
	}
	for range 6 {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil {
			t.Fatal(err)
		}
		assertPacket(n)
	}
	var encodeErr error
	lastLen := 0
	allocs := testing.AllocsPerRun(100, func() {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil && encodeErr == nil {
			encodeErr = err
		}
		lastLen = n
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed 40 ms QEXT CBR EncodeInt24 allocated %.1f times", allocs)
	}
	assertPacket(lastLen)
}
