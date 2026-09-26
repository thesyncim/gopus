//go:build gopus_qext

package gopus

import (
	"bytes"
	"encoding/binary"
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
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 2, 1, 128000, BitrateModeVBR, "")
}

func TestQEXTStateful5msFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 128000, BitrateModeVBR, "")
}

func TestQEXTStatefulStereoFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStateful10msFixedStoragePacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 480, 3, 1, 256000, BitrateModeVBR, "")
}

func testQEXTStatefulPacketsMatchLibopus(t *testing.T, bitrate int, mode BitrateMode, modeArg string) {
	t.Helper()
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 1, bitrate, mode, modeArg)
}

func testQEXTStatefulPacketsWithSizeMatchLibopus(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string) {
	t.Helper()
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired QEXT opus_demo", err)
		return
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
		"-max_payload", "1276", "-qext"}
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
	packet := make([]byte, 1276)
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
		if _, _, _, present, ok := qextParseExtensionRegion(refPacket); !ok || !present {
			t.Fatalf("libopus frame %d lacks a valid QEXT extension", frame)
		}

		for i, sample := range pcm[frame*frameSize*channels : (frame+1)*frameSize*channels] {
			// opus_demo -f32 uses this signed-24 conversion before opus_encode24.
			pcm24[i] = int32(math.Floor(0.5 + float64(sample*8388608)))
		}
		gotLen, err := enc.EncodeInt24(pcm24, packet)
		if err != nil {
			t.Fatalf("Go frame %d encode: %v", frame, err)
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
