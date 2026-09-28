//go:build gopus_qext

package multistream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func firstOpusDemoQEXTPacketForMultistreamTest(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, os.ErrInvalid
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n < 0 || len(data) < 8+n {
		return nil, os.ErrInvalid
	}
	return append([]byte(nil), data[8:8+n]...), nil
}

func encodeLibopusQEXTPacketForMultistreamTest(t *testing.T, opusDemo string, channels int, pcm []float32) []byte {
	t.Helper()

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext.f32")
	bitstreamPath := filepath.Join(tmpDir, "qext.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}

	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), "256000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20", "-qext",
		inputPath, bitstreamPath,
	}
	cmd := exec.Command(opusDemo, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo encode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	packet, err := firstOpusDemoQEXTPacketForMultistreamTest(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoQEXTPacketForMultistreamTest: %v", err)
	}
	return packet
}

type qextStreamFrame struct {
	rawFrame    []byte
	qextPayload []byte
	tocBase     byte
	toc         streamTOC
}

func qextMultistreamExpectedDecode(t *testing.T, p libopustest.QEXTDecode96kParams) []float32 {
	t.Helper()
	result, err := libopustest.ProbeQEXTDecodePublic(p)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected public QEXT decoder", err)
		return nil
	}
	return result.PCM
}

func makeQEXTSinePCMForMultistreamTest(channels int, freq, phaseShift float64) []float32 {
	pcm := make([]float32, 960*channels)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * freq * float64(i) / 48000.0
		pcm[i*channels] = float32(0.43 * math.Sin(phase+phaseShift))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.31 * math.Sin(phase+phaseShift+0.41))
		}
	}
	return pcm
}

func parseQEXTStreamFrameForTest(t *testing.T, label string, packet []byte) qextStreamFrame {
	t.Helper()
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		t.Fatalf("parseOpusPacket(%s): %v", label, err)
	}
	if len(parsed.frames) != 1 {
		t.Fatalf("%s frame count=%d want 1", label, len(parsed.frames))
	}
	extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
	if err != nil {
		t.Fatalf("parsePacketExtensionList(%s): %v", label, err)
	}
	for _, ext := range extensions {
		if ext.ID == qextPacketExtensionID && ext.Frame == 0 {
			return qextStreamFrame{
				rawFrame:    parsed.frames[0],
				qextPayload: ext.Data,
				tocBase:     packet[0] &^ 0x03,
				toc:         parseStreamTOC(packet[0]),
			}
		}
	}
	t.Fatalf("%s missing QEXT payload", label)
	return qextStreamFrame{}
}

func makeLibopusQEXTMultiFrameStreamPacketForTest(t *testing.T, opusDemo string, channels int) ([]byte, [][]byte, []qextStreamFrame) {
	t.Helper()
	packetA := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, channels, makeQEXTSinePCMForMultistreamTest(channels, 997, 0.0))
	packetB := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, channels, makeQEXTSinePCMForMultistreamTest(channels, 1237, 0.23))
	frameA := parseQEXTStreamFrameForTest(t, "frameA", packetA)
	frameB := parseQEXTStreamFrameForTest(t, "frameB", packetB)
	if frameA.toc != frameB.toc {
		t.Fatalf("source frames are not repacketizable: A=%+v B=%+v", frameA.toc, frameB.toc)
	}

	dst := make([]byte, len(packetA)+len(packetB)+len(frameA.qextPayload)+len(frameB.qextPayload)+128)
	n, err := buildOpusPacketFromFramesAndExtensions(
		packetA[0]&^byte(0x03),
		[][]byte{frameA.rawFrame, frameB.rawFrame},
		[]packetExtensionData{
			{ID: qextPacketExtensionID, Frame: 0, Data: frameA.qextPayload},
			{ID: qextPacketExtensionID, Frame: 1, Data: frameB.qextPayload},
		},
		false,
		dst,
	)
	if err != nil {
		t.Fatalf("build multi-frame QEXT packet: %v", err)
	}
	packet := dst[:n]
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		t.Fatalf("parseOpusPacket(built): %v", err)
	}
	if len(parsed.frames) != 2 || parsed.paddingFrameCount != 2 {
		t.Fatalf("built packet frames=%d paddingFrameCount=%d want 2", len(parsed.frames), parsed.paddingFrameCount)
	}
	extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
	if err != nil {
		t.Fatalf("parsePacketExtensionList(built): %v", err)
	}
	if len(extensions) != 2 || extensions[0].Frame != 0 || extensions[1].Frame != 1 {
		t.Fatalf("built extensions=%+v want one QEXT payload per frame", extensions)
	}
	return packet, [][]byte{packetA, packetB}, []qextStreamFrame{frameA, frameB}
}

func makeMalformedQEXTPaddingStreamPacketForTest(t *testing.T, frame qextStreamFrame, selfDelimited bool) []byte {
	t.Helper()
	padding := []byte{0xFF, 0xFF}
	dst := make([]byte, len(frame.rawFrame)+len(padding)+8)
	n, err := buildOpusPacketFromFramesAndPadding(frame.tocBase, [][]byte{frame.rawFrame}, padding, selfDelimited, dst)
	if err != nil {
		t.Fatalf("build malformed QEXT padding packet: %v", err)
	}
	return dst[:n]
}

func TestDecoderQEXTIgnoreExtensionsToggleMatchesExplicitStreamPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type streamFrame struct {
		packet []byte
		ignore bool
	}

	newSine := func(channels int, freq float64, rightPhase float64, rightGain float64) []float32 {
		pcm := make([]float32, 960*channels)
		for i := 0; i < 960; i++ {
			phase := 2 * math.Pi * freq * float64(i) / 48000.0
			pcm[i*channels] = float32(0.45 * math.Sin(phase))
			if channels == 2 {
				pcm[i*channels+1] = float32(rightGain * math.Sin(phase+rightPhase))
			}
		}
		return pcm
	}

	plans := []struct {
		channels int
		pcm      []float32
		ignore   bool
	}{
		{1, newSine(1, 320.0, 0, 0), false},
		{2, newSine(2, 640.0, 0.37, 0.35), true},
		{1, newSine(1, 800.0, 0, 0), false},
	}

	sequence := make([]streamFrame, 0, len(plans))
	for i, tc := range plans {
		packet := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, tc.channels, tc.pcm)
		parsed, err := parseOpusPacket(packet, false)
		if err != nil {
			t.Fatalf("parseOpusPacket[%d]: %v", i, err)
		}
		if len(parsed.frames) != 1 {
			t.Fatalf("frame count[%d]=%d want 1", i, len(parsed.frames))
		}
		extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
		if err != nil {
			t.Fatalf("parsePacketExtensionList[%d]: %v", i, err)
		}
		var qextPayload []byte
		for _, ext := range extensions {
			if ext.ID == qextPacketExtensionID && ext.Frame == 0 {
				qextPayload = ext.Data
				break
			}
		}
		if len(qextPayload) == 0 {
			t.Fatalf("packet[%d] missing QEXT payload", i)
		}
		sequence = append(sequence, streamFrame{
			packet: packet,
			ignore: tc.ignore,
		})
	}

	packets := make([][]byte, len(sequence))
	ignoreByPacket := make([]bool, len(sequence))
	for i, frame := range sequence {
		packets[i] = frame.packet
		ignoreByPacket[i] = frame.ignore
	}
	want := qextMultistreamExpectedDecode(t, libopustest.QEXTDecode96kParams{
		SampleFormat:             libopustest.QEXTDecode96kFormatFloat32,
		Channels:                 2,
		SampleRate:               48000,
		MaxFrameSize:             960,
		Packets:                  packets,
		IgnoreExtensionsByPacket: ignoreByPacket,
	})
	gotDec, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	for i, tc := range sequence {
		gotDec.SetIgnoreExtensions(tc.ignore)
		got, err := gotDec.Decode(tc.packet, 960)
		if err != nil {
			t.Fatalf("Decode[%d] ignore=%v: %v", i, tc.ignore, err)
		}
		start := i * 960 * 2
		wantFrame := want[start : start+len(got)]
		if len(got) != len(wantFrame) {
			t.Fatalf("Decode[%d] len=%d want %d", i, len(got), len(wantFrame))
		}
		for j := range got {
			if got[j] != wantFrame[j] {
				t.Fatalf("Decode[%d] sample[%d]=%v want %v", i, j, got[j], wantFrame[j])
			}
		}
	}
}

func TestDecoderQEXTMultiFramePacketMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet, sourcePackets, frames := makeLibopusQEXTMultiFrameStreamPacketForTest(t, opusDemo, channels)

			wantCombined, err := libopustest.ProbeQEXTDecodePublic(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     channels,
				SampleRate:   48000,
				MaxFrameSize: 960 * len(frames),
				Packets:      [][]byte{packet},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected public QEXT decoder for combined packet", err)
				return
			}
			want := wantCombined.PCM

			coupledStreams := 0
			mapping := []byte{0}
			if channels == 2 {
				coupledStreams = 1
				mapping = []byte{0, 1}
			}
			gotDec, err := NewDecoder(48000, channels, 1, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got, err := gotDec.Decode(packet, 960*len(frames))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("Decode len=%d want %d", len(got), len(want))
			}
			if len(wantCombined.FinalRanges) != 1 {
				t.Fatalf("combined C final ranges=%d want 1", len(wantCombined.FinalRanges))
			}
			if gotRange := gotDec.FinalRange(); gotRange != wantCombined.FinalRanges[0] {
				t.Fatalf("combined final range Go=%08x selected C=%08x", gotRange, wantCombined.FinalRanges[0])
			}
			wantSeparate, err := libopustest.ProbeQEXTDecodePublic(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     channels,
				SampleRate:   48000,
				MaxFrameSize: 960,
				Packets:      sourcePackets,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected public QEXT decoder for source packet sequence", err)
				return
			}
			if len(wantSeparate.FinalRanges) != len(sourcePackets) {
				t.Fatalf("separate C final ranges=%d want %d", len(wantSeparate.FinalRanges), len(sourcePackets))
			}
			gotSeparateDec, err := NewDecoder(48000, channels, 1, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewDecoder separate sequence: %v", err)
			}
			gotSeparate := make([]float32, 0, len(got))
			for i, sourcePacket := range sourcePackets {
				frame, err := gotSeparateDec.Decode(sourcePacket, 960)
				if err != nil {
					t.Fatalf("Decode separate packet[%d]: %v", i, err)
				}
				if gotRange := gotSeparateDec.FinalRange(); gotRange != wantSeparate.FinalRanges[i] {
					t.Fatalf("separate packet[%d] final range Go=%08x selected C=%08x", i, gotRange, wantSeparate.FinalRanges[i])
				}
				gotSeparate = append(gotSeparate, frame...)
			}
			if len(wantSeparate.PCM) != len(got) || len(gotSeparate) != len(got) {
				t.Fatalf("separate sequence lengths: C=%d Go=%d combined=%d", len(wantSeparate.PCM), len(gotSeparate), len(got))
			}
			for i := range got {
				if math.Float32bits(gotSeparate[i]) != math.Float32bits(wantSeparate.PCM[i]) {
					t.Fatalf("source packet sequence differs at sample[%d]: Go=%08x C=%08x", i, math.Float32bits(gotSeparate[i]), math.Float32bits(wantSeparate.PCM[i]))
				}
			}
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("combined sample[%d]=%08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}

			allocDec, err := NewDecoder(48000, channels, 1, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewDecoder allocation check: %v", err)
			}
			output := make([]float32, 960*len(frames)*channels)
			decodeInto := func() error {
				n, err := allocDec.DecodeIntoFloat32(packet, output, 960*len(frames))
				if err != nil {
					return err
				}
				if n != 960*len(frames) {
					return fmt.Errorf("DecodeIntoFloat32 samples=%d want %d", n, 960*len(frames))
				}
				return nil
			}
			for range 2 {
				if err := decodeInto(); err != nil {
					t.Fatalf("warm DecodeIntoFloat32: %v", err)
				}
			}
			var allocErr error
			allocs := testing.AllocsPerRun(10, func() {
				if err := decodeInto(); err != nil && allocErr == nil {
					allocErr = err
				}
			})
			if allocErr != nil {
				t.Fatalf("allocation DecodeIntoFloat32: %v", allocErr)
			}
			if allocs != 0 {
				t.Fatalf("warm %d-channel QEXT multiframe DecodeIntoFloat32 allocated %g times/op", channels, allocs)
			}
		})
	}
}

func TestDecoderQEXTMultiFrameIgnoreExtensionsMatchesInactivePayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet, _, frames := makeLibopusQEXTMultiFrameStreamPacketForTest(t, opusDemo, channels)

			want := qextMultistreamExpectedDecode(t, libopustest.QEXTDecode96kParams{
				SampleFormat:     libopustest.QEXTDecode96kFormatFloat32,
				Channels:         channels,
				SampleRate:       48000,
				IgnoreExtensions: true,
				MaxFrameSize:     960 * len(frames),
				Packets:          [][]byte{packet},
			})

			coupledStreams := 0
			mapping := []byte{0}
			if channels == 2 {
				coupledStreams = 1
				mapping = []byte{0, 1}
			}
			gotDec, err := NewDecoder(48000, channels, 1, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got, err := gotDec.Decode(packet, 960*len(frames))
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("Decode len=%d want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive payload %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecoderQEXTTwoStreamPacketMatchesExplicitStreamPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	stereoPCM := make([]float32, 960*2)
	monoPCM := make([]float32, 960)
	for i := 0; i < 960; i++ {
		tm := float64(i) / 48000.0
		stereoPCM[2*i] = float32(0.45 * math.Sin(2*math.Pi*440*tm))
		stereoPCM[2*i+1] = float32(0.35 * math.Sin(2*math.Pi*660*tm+0.37))
		monoPCM[i] = float32(0.40 * math.Sin(2*math.Pi*550*tm+0.19))
	}

	coupledPacket := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 2, stereoPCM)
	monoPacket := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 1, monoPCM)

	selfDelimitedCoupled, err := makeSelfDelimitedPacket(coupledPacket)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket: %v", err)
	}
	packet := make([]byte, 0, len(selfDelimitedCoupled)+len(monoPacket))
	packet = append(packet, selfDelimitedCoupled...)
	packet = append(packet, monoPacket...)

	for _, ignore := range []bool{false, true} {
		coupledWant := qextMultistreamExpectedDecode(t, libopustest.QEXTDecode96kParams{
			SampleFormat:     libopustest.QEXTDecode96kFormatFloat32,
			Channels:         2,
			SampleRate:       48000,
			IgnoreExtensions: ignore,
			MaxFrameSize:     960,
			Packets:          [][]byte{coupledPacket},
		})
		monoWant := qextMultistreamExpectedDecode(t, libopustest.QEXTDecode96kParams{
			SampleFormat:     libopustest.QEXTDecode96kFormatFloat32,
			Channels:         1,
			SampleRate:       48000,
			IgnoreExtensions: ignore,
			MaxFrameSize:     960,
			Packets:          [][]byte{monoPacket},
		})

		want := make([]float32, 960*3)
		for i := 0; i < 960; i++ {
			want[3*i] = coupledWant[2*i]
			want[3*i+1] = coupledWant[2*i+1]
			want[3*i+2] = monoWant[i]
		}

		dec, err := NewDecoder(48000, 3, 2, 1, []byte{0, 1, 2})
		if err != nil {
			t.Fatalf("NewDecoder(ignore=%v): %v", ignore, err)
		}
		dec.SetIgnoreExtensions(ignore)
		got, err := dec.Decode(packet, 960)
		if err != nil {
			t.Fatalf("Decode(ignore=%v): %v", ignore, err)
		}
		if len(got) != len(want) {
			t.Fatalf("Decode(ignore=%v) len=%d want %d", ignore, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("Decode(ignore=%v) sample[%d]=%v want %v", ignore, i, got[i], want[i])
			}
		}
	}
}

func TestDecoderQEXTTwoStreamOpaquePaddingMatchesExplicitStreamPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	stereoPCM := makeQEXTSinePCMForMultistreamTest(2, 440, 0.0)
	monoPCM := makeQEXTSinePCMForMultistreamTest(1, 550, 0.19)
	coupledPacket := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 2, stereoPCM)
	monoPacket := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 1, monoPCM)
	coupledFrame := parseQEXTStreamFrameForTest(t, "coupled", coupledPacket)
	monoFrame := parseQEXTStreamFrameForTest(t, "mono", monoPacket)

	coupledSelfDelimited, err := makeSelfDelimitedPacket(coupledPacket)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket: %v", err)
	}
	coupledMalformedSelfDelimited := makeMalformedQEXTPaddingStreamPacketForTest(t, coupledFrame, true)
	monoMalformed := makeMalformedQEXTPaddingStreamPacketForTest(t, monoFrame, false)

	cases := []struct {
		name           string
		packet         []byte
		coupledPayload []byte
		monoPayload    []byte
	}{
		{
			name:           "self_delimited_coupled",
			packet:         append(append([]byte(nil), coupledMalformedSelfDelimited...), monoPacket...),
			coupledPayload: nil,
			monoPayload:    monoFrame.qextPayload,
		},
		{
			name:           "last_mono",
			packet:         append(append([]byte(nil), coupledSelfDelimited...), monoMalformed...),
			coupledPayload: coupledFrame.qextPayload,
			monoPayload:    nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, ignore := range []bool{false, true} {
				coupledIgnore := ignore || len(tc.coupledPayload) == 0
				coupledWant := qextMultistreamExpectedDecode(t, libopustest.QEXTDecode96kParams{
					SampleFormat:     libopustest.QEXTDecode96kFormatFloat32,
					Channels:         2,
					SampleRate:       48000,
					IgnoreExtensions: coupledIgnore,
					MaxFrameSize:     960,
					Packets:          [][]byte{coupledPacket},
				})
				monoIgnore := ignore || len(tc.monoPayload) == 0
				monoWant := qextMultistreamExpectedDecode(t, libopustest.QEXTDecode96kParams{
					SampleFormat:     libopustest.QEXTDecode96kFormatFloat32,
					Channels:         1,
					SampleRate:       48000,
					IgnoreExtensions: monoIgnore,
					MaxFrameSize:     960,
					Packets:          [][]byte{monoPacket},
				})

				want := make([]float32, 960*3)
				for i := 0; i < 960; i++ {
					want[3*i] = coupledWant[2*i]
					want[3*i+1] = coupledWant[2*i+1]
					want[3*i+2] = monoWant[i]
				}

				dec, err := NewDecoder(48000, 3, 2, 1, []byte{0, 1, 2})
				if err != nil {
					t.Fatalf("NewDecoder(ignore=%v): %v", ignore, err)
				}
				dec.SetIgnoreExtensions(ignore)
				got, err := dec.Decode(tc.packet, 960)
				if err != nil {
					t.Fatalf("Decode(ignore=%v): %v", ignore, err)
				}
				if len(got) != len(want) {
					t.Fatalf("Decode(ignore=%v) len=%d want %d", ignore, len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("Decode(ignore=%v) sample[%d]=%v want %v", ignore, i, got[i], want[i])
					}
				}
			}
		})
	}
}
