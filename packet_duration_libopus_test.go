package gopus

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

type libopusPacketDurationCase struct {
	name       string
	packet     []byte
	sampleRate int
}

type libopusPacketDurationResult struct {
	samplesPerFrame int
	frameCount      int
	samples         int
}

var libopusPacketDurationHelper libopustest.HelperCache

func getLibopusPacketDurationHelperPath() (string, error) {
	return libopusPacketDurationHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "packet duration",
		OutputBase: "gopus_libopus_packet_duration",
		SourceFile: "libopus_packet_duration_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

func probeLibopusPacketDurations(cases []libopusPacketDurationCase) ([]libopusPacketDurationResult, error) {
	binPath, err := getLibopusPacketDurationHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GPDI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.sampleRate))
		payload.U32(uint32(len(tc.packet)))
		payload.Raw(tc.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "packet duration", "GPDO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(12 * count)
	out := make([]libopusPacketDurationResult, count)
	for i := range out {
		out[i] = libopusPacketDurationResult{
			samplesPerFrame: int(reader.I32()),
			frameCount:      int(reader.I32()),
			samples:         int(reader.I32()),
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestPacketDurationAtRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := packetDurationOracleCases()
	want, err := probeLibopusPacketDurations(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet duration", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotFrame, err := packetSamplesPerFrameAtRate(tc.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesPerFrameAtRate error: %v", err)
			}
			if gotFrame != want[i].samplesPerFrame {
				t.Fatalf("samples/frame=%d want %d", gotFrame, want[i].samplesPerFrame)
			}
			gotCount, err := packetFrameCountLibopus(tc.packet)
			if err != nil {
				t.Fatalf("packetFrameCountLibopus error: %v", err)
			}
			if gotCount != want[i].frameCount {
				t.Fatalf("frame count=%d want %d", gotCount, want[i].frameCount)
			}
			gotSamples, err := packetSamplesAtRate(tc.packet, tc.sampleRate)
			if want[i].samples < 0 {
				if err == nil {
					t.Fatalf("packetSamplesAtRate error=nil want libopus error %d", want[i].samples)
				}
				return
			}
			if err != nil {
				t.Fatalf("packetSamplesAtRate error: %v", err)
			}
			if gotSamples != want[i].samples {
				t.Fatalf("samples=%d want %d", gotSamples, want[i].samples)
			}
		})
	}
}

func packetDurationOracleCases() []libopusPacketDurationCase {
	var cases []libopusPacketDurationCase
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, config := range []uint8{0, 1, 2, 3, 12, 13, 14, 15, 16, 17, 18, 19, 28, 29, 30, 31} {
			for _, code := range []uint8{0, 1, 2} {
				toc := GenerateTOC(config, false, code)
				cases = append(cases, libopusPacketDurationCase{
					name:       durationCaseName(rate, config, code, 1),
					packet:     []byte{toc, 0, 0, 0},
					sampleRate: rate,
				})
			}
			for _, count := range []byte{1, 2, 3, 6} {
				toc := GenerateTOC(config, false, 3)
				cases = append(cases, libopusPacketDurationCase{
					name:       durationCaseName(rate, config, 3, int(count)),
					packet:     []byte{toc, count, 0, 0, 0, 0, 0, 0},
					sampleRate: rate,
				})
			}
		}
	}
	return cases
}

func durationCaseName(rate int, config, code uint8, count int) string {
	return "fs_" + itoaSmall(rate) + "_cfg_" + itoaSmall(int(config)) + "_code_" + itoaSmall(int(code)) + "_m_" + itoaSmall(count)
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func maxFuzzPacketExtensionCount(dataLen, nbFrames int) int {
	if dataLen <= 0 || nbFrames <= 0 {
		return 0
	}
	// Repeat markers can replay a compact extension run across every frame.
	return dataLen * nbFrames
}

func FuzzPacketExtensionIterator_NoPanic(f *testing.F) {
	f.Add([]byte{0x41, 0x02, 'A', 'B'}, uint8(1), uint8(32))
	f.Add([]byte{0x06, 0x04}, uint8(3), uint8(3))
	f.Add([]byte{0x01, 0x01, 0x06}, uint8(1), uint8(3))
	f.Add([]byte{0x02, 0x42, 'x', 'y', 'z'}, uint8(3), uint8(33))
	f.Add([]byte{0xff, 0x00, 0x03, 0x02}, uint8(2), uint8(64))

	f.Fuzz(func(t *testing.T, data []byte, rawFrames uint8, rawID uint8) {
		if len(data) > 512 {
			data = data[:512]
		}
		nbFrames := int(rawFrames%maxRepacketizerFrames) + 1
		id := 3 + int(rawID%125)

		count, err := countPacketExtensions(data, nbFrames)
		if err != nil {
			return
		}
		maxCount := maxFuzzPacketExtensionCount(len(data), nbFrames)
		if count < 0 || count > maxCount {
			t.Fatalf("extension count out of range: %d > %d", count, maxCount)
		}

		counts := make([]int, nbFrames)
		countByFrame, err := countPacketExtensionsByFrame(data, nbFrames, counts)
		if err != nil {
			t.Fatalf("countPacketExtensionsByFrame after count success: %v", err)
		}
		if countByFrame != count {
			t.Fatalf("extension count mismatch: total=%d byFrame=%d", count, countByFrame)
		}

		extensions := make([]packetExtensionData, count)
		parsed, err := parsePacketExtensions(data, nbFrames, extensions)
		if err != nil {
			t.Fatalf("parsePacketExtensions after count success: %v", err)
		}
		if parsed != count {
			t.Fatalf("parsePacketExtensions count=%d want %d", parsed, count)
		}

		frameOrder := make([]packetExtensionData, count)
		ordered, err := parsePacketExtensionsFrameOrder(data, nbFrames, counts, frameOrder)
		if err != nil {
			t.Fatalf("parsePacketExtensionsFrameOrder after count success: %v", err)
		}
		if ordered != count {
			t.Fatalf("frame-order extension count=%d want %d", ordered, count)
		}

		if ext, ok, err := findPacketExtension(data, nbFrames, id); err != nil {
			t.Fatalf("findPacketExtension after count success: %v", err)
		} else if ok && ext.ID != id {
			t.Fatalf("findPacketExtension id=%d want %d", ext.ID, id)
		}

		if count == 0 {
			return
		}
		n, err := generatePacketExtensions(nil, len(data), extensions[:parsed], nbFrames, false)
		if err != nil {
			return
		}
		if n < 0 || n > len(data) {
			t.Fatalf("generatePacketExtensions size=%d outside input bound %d", n, len(data))
		}
		out := make([]byte, n)
		written, err := generatePacketExtensions(out, n, extensions[:parsed], nbFrames, false)
		if err != nil {
			t.Fatalf("generatePacketExtensions write after size success: %v", err)
		}
		if written != n {
			t.Fatalf("generatePacketExtensions wrote=%d want %d", written, n)
		}
		regeneratedCount, err := countPacketExtensions(out, nbFrames)
		if err != nil {
			t.Fatalf("countPacketExtensions regenerated output: %v", err)
		}
		if regeneratedCount != count {
			t.Fatalf("regenerated extension count=%d want %d", regeneratedCount, count)
		}
	})
}

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("DecodeString(%q): %v", s, err)
	}
	return b
}

func TestGeneratePacketExtensionsMatchesLibopusCases(t *testing.T) {
	tests := []struct {
		name       string
		extensions []packetExtensionData
		nbFrames   int
		length     int
		pad        bool
		wantHex    string
	}{
		{
			name: "mixed",
			extensions: []packetExtensionData{
				{ID: 32, Frame: 0, Data: []byte("AB")},
				{ID: 32, Frame: 1, Data: []byte("CD")},
				{ID: 3, Frame: 1},
				{ID: 33, Frame: 2, Data: []byte("xyz")},
			},
			nbFrames: 3,
			length:   15,
			wantHex:  "41024142024102434406024278797a",
		},
		{
			name: "repeat_short",
			extensions: []packetExtensionData{
				{ID: 3, Frame: 0},
				{ID: 3, Frame: 1},
				{ID: 3, Frame: 2},
			},
			nbFrames: 3,
			length:   2,
			wantHex:  "0604",
		},
		{
			name: "repeat_long",
			extensions: []packetExtensionData{
				{ID: 32, Frame: 0, Data: []byte("AB")},
				{ID: 32, Frame: 1, Data: []byte("CD")},
				{ID: 32, Frame: 2, Data: []byte("EF")},
			},
			nbFrames: 3,
			length:   10,
			wantHex:  "41024142040243444546",
		},
		{
			name:       "pad_front",
			extensions: []packetExtensionData{{ID: 3, Frame: 0}},
			nbFrames:   1,
			length:     20,
			pad:        true,
			wantHex:    "0101010101010101010101010101010101010106",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := make([]byte, tc.length)
			n, err := generatePacketExtensions(dst, tc.length, tc.extensions, tc.nbFrames, tc.pad)
			if err != nil {
				t.Fatalf("generatePacketExtensions() error = %v", err)
			}
			if n != tc.length {
				t.Fatalf("generatePacketExtensions() length = %d want %d", n, tc.length)
			}
			if gotHex := hex.EncodeToString(dst[:n]); gotHex != tc.wantHex {
				t.Fatalf("generatePacketExtensions() = %s want %s", gotHex, tc.wantHex)
			}

			sizeOnly, err := generatePacketExtensions(nil, tc.length, tc.extensions, tc.nbFrames, tc.pad)
			if err != nil {
				t.Fatalf("generatePacketExtensions(nil) error = %v", err)
			}
			if sizeOnly != tc.length {
				t.Fatalf("generatePacketExtensions(nil) = %d want %d", sizeOnly, tc.length)
			}
		})
	}
}

func TestPacketExtensionIteratorParseAndCount(t *testing.T) {
	data := mustHexBytes(t, "41024142024102434406024278797a")

	count, err := countPacketExtensions(data, 3)
	if err != nil {
		t.Fatalf("countPacketExtensions() error = %v", err)
	}
	if count != 4 {
		t.Fatalf("countPacketExtensions() = %d want 4", count)
	}

	counts := make([]int, 3)
	count, err = countPacketExtensionsByFrame(data, 3, counts)
	if err != nil {
		t.Fatalf("countPacketExtensionsByFrame() error = %v", err)
	}
	if count != 4 {
		t.Fatalf("countPacketExtensionsByFrame() total = %d want 4", count)
	}
	if !reflect.DeepEqual(counts, []int{1, 2, 1}) {
		t.Fatalf("countPacketExtensionsByFrame() = %v want [1 2 1]", counts)
	}

	got := make([]packetExtensionData, 4)
	n, err := parsePacketExtensions(data, 3, got)
	if err != nil {
		t.Fatalf("parsePacketExtensions() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("parsePacketExtensions() count = %d want 4", n)
	}

	want := []packetExtensionData{
		{ID: 32, Frame: 0, Data: []byte("AB")},
		{ID: 32, Frame: 1, Data: []byte("CD")},
		{ID: 3, Frame: 1, Data: nil},
		{ID: 33, Frame: 2, Data: []byte("xyz")},
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Frame != want[i].Frame || string(got[i].Data) != string(want[i].Data) {
			t.Fatalf("parsePacketExtensions()[%d] = %+v want %+v", i, got[i], want[i])
		}
	}

	frameOrder := make([]packetExtensionData, 4)
	n, err = parsePacketExtensionsFrameOrder(data, 3, counts, frameOrder)
	if err != nil {
		t.Fatalf("parsePacketExtensionsFrameOrder() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("parsePacketExtensionsFrameOrder() count = %d want 4", n)
	}
	for i := range want {
		if frameOrder[i].ID != want[i].ID || frameOrder[i].Frame != want[i].Frame || string(frameOrder[i].Data) != string(want[i].Data) {
			t.Fatalf("parsePacketExtensionsFrameOrder()[%d] = %+v want %+v", i, frameOrder[i], want[i])
		}
	}

	ext, ok, err := findPacketExtension(data, 3, 33)
	if err != nil {
		t.Fatalf("findPacketExtension() error = %v", err)
	}
	if !ok {
		t.Fatal("findPacketExtension() did not find id 33")
	}
	if ext.Frame != 2 || string(ext.Data) != "xyz" {
		t.Fatalf("findPacketExtension() = %+v want frame=2 data=xyz", ext)
	}
}

func TestPacketExtensionIteratorRepeatExpansion(t *testing.T) {
	data := mustHexBytes(t, "0604")
	got := make([]packetExtensionData, 3)
	n, err := parsePacketExtensions(data, 3, got)
	if err != nil {
		t.Fatalf("parsePacketExtensions() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("parsePacketExtensions() count = %d want 3", n)
	}
	for i := range 3 {
		if got[i].ID != 3 || got[i].Frame != i || len(got[i].Data) != 0 {
			t.Fatalf("parsePacketExtensions()[%d] = %+v want id=3 frame=%d", i, got[i], i)
		}
	}
}

func TestPacketExtensionIteratorRejectsInvalidSeparator(t *testing.T) {
	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, []byte{0x03, 0x02}, 1)
	if ok, err := iter.next(nil); err == nil || ok {
		t.Fatalf("iter.next() = (%v,%v) want invalid packet error", ok, err)
	}
}

func TestValidatePacketFramingMatchesParsePacket(t *testing.T) {
	packets := [][]byte{
		nil,
		{0}, {0, 1},
		{1}, {1, 1}, {1, 1, 2},
		{2}, {2, 0}, {2, 2, 1}, {2, 252}, {2, 252, 0},
		{3}, {3, 0}, {3, 1}, {3, 2, 1, 2},
		{3, 0x41}, {3, 0x41, 2, 1, 2},
		{3, 0x82}, {3, 0x82, 1}, {3, 0x82, 1, 1, 2},
		{3, 0xc2}, {3, 0xc2, 0, 1, 1, 2, 3},
		{3, 0x3f},
		{0x83, 0x30}, // 48 CELT 2.5 ms frames = 120 ms.
		{3, 0x41, 255},
	}
	chainedPadding := make([]byte, 2+2+255)
	chainedPadding[0], chainedPadding[1] = 3, 0x41
	chainedPadding[2], chainedPadding[3] = 255, 1
	packets = append(packets, chainedPadding)
	for _, n := range []int{1275, 1276, 2550, 2551} {
		for code := byte(0); code < 4; code++ {
			packet := make([]byte, n+1)
			packet[0] = code
			packets = append(packets, packet)
		}
	}
	rng := rand.New(rand.NewSource(6716))
	for i := range 2000 {
		packet := make([]byte, rng.Intn(80))
		for j := range packet {
			packet[j] = byte(rng.Intn(256))
		}
		if len(packet) != 0 {
			packet[0] = packet[0]&^3 | byte(i&3)
		}
		packets = append(packets, packet)
	}
	for i, packet := range packets {
		_, want := ParsePacket(packet)
		if got := validatePacketFraming(packet); got != want {
			t.Fatalf("packet %d (%x): validator error=%v, parser error=%v", i, packet, got, want)
		}
	}
	for _, packet := range [][]byte{{0, 1}, {1, 1, 2}, {3, 0x82, 1, 1, 2}, {3, 0}, {2, 252}} {
		if allocs := testing.AllocsPerRun(100, func() { _ = validatePacketFraming(packet) }); allocs != 0 {
			t.Fatalf("validator allocations for %x: %g", packet, allocs)
		}
	}
}

func TestDecodeWithFECMalformedPacketPreservesState(t *testing.T) {
	seed := encodeAPIRateCELTPacket(t, 1)
	for _, packet := range [][]byte{{1, 1}, {2}, {2, 252}, {3, 0}, {3, 0x82}, {3, 0x41, 255}} {
		_, wantErr := ParsePacket(packet)
		if wantErr == nil {
			t.Fatalf("packet %x unexpectedly valid", packet)
		}
		dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatal(err)
		}
		got := make([]float32, 960)
		want := make([]float32, 960)
		if n, err := dec.DecodeWithFEC(packet, got, true); n != 0 || err != wantErr {
			t.Fatalf("packet %x FEC decode=(%d,%v), want (0,%v)", packet, n, err, wantErr)
		}
		gotN, gotErr := dec.Decode(seed, got)
		wantN, freshErr := fresh.Decode(seed, want)
		if gotErr != nil || freshErr != nil || gotN != wantN || dec.FinalRange() != fresh.FinalRange() {
			t.Fatalf("packet %x changed decoder state: recovered=(%d,%v,%08x) fresh=(%d,%v,%08x)",
				packet, gotN, gotErr, dec.FinalRange(), wantN, freshErr, fresh.FinalRange())
		}
		for i := range got[:gotN] {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("packet %x changed decoder state at sample %d", packet, i)
			}
		}
	}
}

type libopusPacketLBRRCase struct {
	name   string
	packet []byte
	group  string
}

var libopusPacketLBRRHelper libopustest.HelperCache

func getLibopusPacketLBRRHelperPath() (string, error) {
	return libopusPacketLBRRHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "packet LBRR",
		OutputBase: "gopus_libopus_packet_lbrr",
		SourceFile: "libopus_packet_lbrr_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

func probeLibopusPacketLBRR(cases []libopusPacketLBRRCase) ([]int, error) {
	binPath, err := getLibopusPacketLBRRHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GPLI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packet)))
		payload.Raw(tc.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "packet LBRR", "GPLO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(4 * count)
	out := make([]int, count)
	for i := range out {
		out[i] = int(reader.I32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestPacketHasLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := packetLBRROracleCases(t)
	want, err := probeLibopusPacketLBRR(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet LBRR", err)
	}

	positiveByGroup := make(map[string]bool)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantLBRR := want[i] > 0
			if got := PacketHasLBRR(tc.packet); got != wantLBRR {
				t.Fatalf("PacketHasLBRR=%v want %v (libopus ret=%d)", got, wantLBRR, want[i])
			}
			toc, _, err := packetFrameCount(tc.packet)
			if err != nil || len(tc.packet) == 0 {
				return
			}
			firstFrameData, err := extractFirstFramePayload(tc.packet, toc)
			if err != nil {
				return
			}
			if got := packetHasLBRR(firstFrameData, toc); got != wantLBRR {
				t.Fatalf("packetHasLBRR=%v want %v (libopus ret=%d)", got, wantLBRR, want[i])
			}
		})
		if want[i] > 0 {
			positiveByGroup[tc.group] = true
		}
	}

	for _, group := range []string{"silk_fec_mono", "silk_fec_stereo"} {
		if !positiveByGroup[group] {
			t.Fatalf("libopus did not report LBRR for %s oracle cases", group)
		}
	}
}

func packetLBRROracleCases(t *testing.T) []libopusPacketLBRRCase {
	t.Helper()
	cases := []libopusPacketLBRRCase{
		{name: "empty", packet: nil, group: "malformed"},
		{name: "toc_only_silk", packet: []byte{GenerateTOC(0, false, 0)}, group: "malformed"},
		{name: "code3_zero_frames", packet: []byte{GenerateTOC(0, false, 3), 0}, group: "malformed"},
		{name: "code2_truncated_length", packet: []byte{GenerateTOC(0, false, 2), 255}, group: "malformed"},
	}

	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960, 1920, 2880} {
			group := "silk_no_fec_mono"
			if channels == 2 {
				group = "silk_no_fec_stereo"
			}
			cases = append(cases, libopusPacketLBRRCase{
				name:   "silk_no_fec_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize),
				packet: encodeAPIRateSILKPacketFrameSize(t, channels, frameSize),
				group:  group,
			})
		}
	}
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960} {
			cases = append(cases, libopusPacketLBRRCase{
				name:   "hybrid_no_fec_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize),
				packet: encodeAPIRateHybridPacketFrameSize(t, channels, frameSize),
				group:  "hybrid_no_fec",
			})
		}
	}
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960, 1920, 2880} {
			cases = append(cases, libopusPacketLBRRCase{
				name:   "celt_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize),
				packet: encodeAPIRateCELTPacketFrameSize(t, channels, frameSize),
				group:  "celt",
			})
		}
	}

	cases = append(cases, paddedLBRROracleCases(t)...)
	cases = append(cases, fecLBRROracleCases(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, "silk_fec")...)
	cases = append(cases, fecLBRROracleCases(t, EncoderModeHybrid, ModeHybrid, BandwidthSuperwideband, 32000, "hybrid_fec")...)
	return cases
}

func paddedLBRROracleCases(t *testing.T) []libopusPacketLBRRCase {
	t.Helper()
	first := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	second := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	code3CBR := buildPaddedLBRROraclePacket(t, first, [][]byte{
		firstAPIRateFramePayload(t, first),
		firstAPIRateFramePayload(t, second),
	}, len(first)+len(second)+12)

	celtVBR := encodeAPIRateCELTPacketVariants(t, 1, 960, []int{32000, 48000, 64000}, 3)
	code3VBR := buildPaddedLBRROraclePacket(t, celtVBR[0], firstAPIRateFramePayloads(t, celtVBR), 0)

	return []libopusPacketLBRRCase{
		{name: "silk_code3_cbr_padding_no_fec", packet: code3CBR, group: "silk_no_fec_padded"},
		{name: "celt_code3_vbr_no_fec", packet: code3VBR, group: "celt_vbr"},
	}
}

func buildPaddedLBRROraclePacket(t *testing.T, basePacket []byte, frames [][]byte, targetLen int) []byte {
	t.Helper()
	data := make([]byte, maxPacketBytesPerStream)
	withPadding := targetLen > 0
	n, err := buildRepacketizedPacketWithOptions(basePacket[0]&0xFC, frames, data, targetLen, withPadding, nil)
	if err != nil {
		t.Fatalf("buildRepacketizedPacketWithOptions: %v", err)
	}
	return append([]byte(nil), data[:n]...)
}

func fecLBRROracleCases(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate int, prefix string) []libopusPacketLBRRCase {
	t.Helper()
	var cases []libopusPacketLBRRCase
	for _, channels := range []int{1, 2} {
		group := prefix + "_mono"
		if channels == 2 {
			group = prefix + "_stereo"
		}
		for _, frameSize := range []int{960, 1920, 2880} {
			packets := encodeLBRROracleFECSequence(t, mode, wantMode, bandwidth, bitrate, channels, frameSize)
			for i, packet := range packets {
				cases = append(cases, libopusPacketLBRRCase{
					name:   prefix + "_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize) + "_pkt" + itoaSmall(i),
					packet: packet,
					group:  group,
				})
			}
		}
	}
	return cases
}

func encodeLBRROracleFECSequence(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate, channels, frameSize int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	app := ApplicationVoIP
	if mode == EncoderModeSILK {
		app = ApplicationRestrictedSilk
	}
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: app,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, 8)
	for frameIndex := range 8 {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			pcm[i*channels] = 0.38*float32(math.Sin(2*math.Pi*220*tm)) +
				0.14*float32(math.Sin(2*math.Pi*440*tm+0.11))
			if channels == 2 {
				pcm[i*channels+1] = 0.33*float32(math.Sin(2*math.Pi*330*tm+0.07)) +
					0.12*float32(math.Sin(2*math.Pi*660*tm+0.19))
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != wantMode {
			t.Fatalf("Encode frame %d mode=%v want %v", frameIndex, toc.Mode, wantMode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

func TestMultistreamPacketPadUnpadSelfDelimitedRoundTrip(t *testing.T) {
	stream0 := []byte{GenerateTOC(31, false, 0), 0x11, 0x22, 0x33, 0x44}
	stream1 := []byte{GenerateTOC(30, false, 0), 0x55, 0x66, 0x77}

	self0, err := makeSelfDelimitedPacket(stream0)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream0): %v", err)
	}

	orig := append(append([]byte{}, self0...), stream1...)
	newLen := len(orig) + 17

	buf := make([]byte, newLen)
	copy(buf, orig)

	if err := MultistreamPacketPad(buf, len(orig), newLen, 2); err != nil {
		t.Fatalf("MultistreamPacketPad: %v", err)
	}

	decoded0, consumed0, err := decodeSelfDelimitedPacket(buf[:newLen])
	if err != nil {
		t.Fatalf("decodeSelfDelimitedPacket(padded): %v", err)
	}
	if !bytes.Equal(decoded0, stream0) {
		t.Fatalf("stream0 changed after pad: got=%v want=%v", decoded0, stream0)
	}
	if consumed0 >= newLen {
		t.Fatalf("invalid consumed0=%d for newLen=%d", consumed0, newLen)
	}
	if _, err := ParsePacket(buf[consumed0:newLen]); err != nil {
		t.Fatalf("last stream parse after pad: %v", err)
	}

	unpaddedLen, err := MultistreamPacketUnpad(buf, newLen, 2)
	if err != nil {
		t.Fatalf("MultistreamPacketUnpad: %v", err)
	}
	if unpaddedLen != len(orig) {
		t.Fatalf("unpaddedLen=%d want=%d", unpaddedLen, len(orig))
	}
	if !bytes.Equal(buf[:unpaddedLen], orig) {
		t.Fatalf("round-trip mismatch: got=%v want=%v", buf[:unpaddedLen], orig)
	}
}

func TestMultistreamPacketPadUnpadThreeStreamsRoundTrip(t *testing.T) {
	stream0 := []byte{GenerateTOC(31, false, 0), 0x10, 0x11, 0x12}
	stream1 := []byte{GenerateTOC(31, false, 2), 0x02, 0x21, 0x22, 0x23}
	stream2 := []byte{GenerateTOC(29, false, 0), 0x31, 0x32, 0x33, 0x34}

	self0, err := makeSelfDelimitedPacket(stream0)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream0): %v", err)
	}
	self1, err := makeSelfDelimitedPacket(stream1)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream1): %v", err)
	}

	orig := append(append(append([]byte{}, self0...), self1...), stream2...)
	newLen := len(orig) + 9

	buf := make([]byte, newLen)
	copy(buf, orig)

	if err := MultistreamPacketPad(buf, len(orig), newLen, 3); err != nil {
		t.Fatalf("MultistreamPacketPad: %v", err)
	}

	unpaddedLen, err := MultistreamPacketUnpad(buf, newLen, 3)
	if err != nil {
		t.Fatalf("MultistreamPacketUnpad: %v", err)
	}
	if unpaddedLen != len(orig) {
		t.Fatalf("unpaddedLen=%d want=%d", unpaddedLen, len(orig))
	}
	if !bytes.Equal(buf[:unpaddedLen], orig) {
		t.Fatalf("round-trip mismatch: got=%v want=%v", buf[:unpaddedLen], orig)
	}
}

func TestMultistreamPacketPadRejectsInvalidSelfDelimited(t *testing.T) {
	// stream0 claims a large self-delimited frame size but doesn't provide it.
	invalid := []byte{GenerateTOC(31, false, 0), 10, 0xAA, GenerateTOC(31, false, 0), 0xBB}
	buf := make([]byte, len(invalid)+8)
	copy(buf, invalid)

	err := MultistreamPacketPad(buf, len(invalid), len(invalid)+8, 2)
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("MultistreamPacketPad err=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestMultistreamPacketUnpadRejectsInvalidSelfDelimited(t *testing.T) {
	invalid := []byte{GenerateTOC(31, false, 0), 10, 0xAA, GenerateTOC(31, false, 0), 0xBB}
	_, err := MultistreamPacketUnpad(invalid, len(invalid), 2)
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("MultistreamPacketUnpad err=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestDecodeSelfDelimitedPacketRejectsLibopusInvalidLengthFraming(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "code0_declared_length_overrun",
			data: []byte{GenerateTOC(31, false, 0), 10, 0xAA},
		},
		{
			name: "code0_truncated_two_byte_length",
			data: []byte{GenerateTOC(31, false, 0), 252},
		},
		{
			name: "code3_missing_frame_count",
			data: []byte{GenerateTOC(31, false, 3)},
		},
		{
			name: "code3_padding_overrun",
			data: []byte{GenerateTOC(31, false, 3), 0x41, 255},
		},
		{
			name: "code3_missing_last_frame_length",
			data: []byte{GenerateTOC(31, false, 3), 0x01},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeSelfDelimitedPacket(tt.data)
			if !errors.Is(err, ErrInvalidPacket) {
				t.Fatalf("decodeSelfDelimitedPacket err=%v want=%v", err, ErrInvalidPacket)
			}
		})
	}
}

func TestMultistreamPacketPadRejectsLibopusParserEnvelopeViolation(t *testing.T) {
	invalid := append([]byte{0x00}, make([]byte, maxOpusFrameBytes+1)...)
	buf := make([]byte, len(invalid)+8)
	copy(buf, invalid)

	err := MultistreamPacketPad(buf, len(invalid), len(invalid)+8, 1)
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("MultistreamPacketPad err=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestMultistreamPacketUnpadRejectsLibopusParserEnvelopeViolation(t *testing.T) {
	invalid := []byte{GenerateTOC(31, false, 3), 0x07}
	_, err := MultistreamPacketUnpad(invalid, len(invalid), 1)
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("MultistreamPacketUnpad err=%v want=%v", err, ErrInvalidPacket)
	}
}

func addPacketMutationFuzzSeeds(f *testing.F) {
	f.Add([]byte{0xF8, 0x11, 0x22, 0x33}, uint8(0), uint8(1))
	f.Add([]byte{GenerateTOC(31, false, 0), 0xAA, 0xBB, 0xCC}, uint8(8), uint8(1))

	if selfDelimited, err := makeSelfDelimitedPacket(testCELTPacket()); err == nil {
		f.Add(append([]byte(nil), selfDelimited...), uint8(12), uint8(2))

		multistream := append(append([]byte{}, selfDelimited...), testStereoCELTPacket()...)
		f.Add(multistream, uint8(16), uint8(2))
	}
}

func FuzzPacketMutationHelpers_NoPanic(f *testing.F) {
	addPacketMutationFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, extra uint8, streamHint uint8) {
		if len(data) == 0 {
			return
		}
		if len(data) > 4096 {
			data = data[:4096]
		}

		padDelta := int(extra % 32)
		if len(data)+padDelta > 4096 {
			padDelta = 4096 - len(data)
		}
		newLen := len(data) + padDelta
		numStreams := int(streamHint%4) + 1

		packetBuf := make([]byte, newLen)
		copy(packetBuf, data)
		_ = PacketPad(packetBuf, len(data), newLen)

		packetCopy := append([]byte(nil), data...)
		_, _ = PacketUnpad(packetCopy, len(packetCopy))

		multistreamBuf := make([]byte, newLen)
		copy(multistreamBuf, data)
		_ = MultistreamPacketPad(multistreamBuf, len(data), newLen, numStreams)

		multistreamCopy := append([]byte(nil), data...)
		_, _ = MultistreamPacketUnpad(multistreamCopy, len(multistreamCopy), numStreams)

		repacketizer := NewRepacketizer()
		_ = repacketizer.Cat(data)
		if frames := repacketizer.NumFrames(); frames > 0 {
			out := make([]byte, 4096)
			_, _ = repacketizer.Out(out)
			_, _ = repacketizer.OutRange(0, frames, out)
		}

		_, _, _, _, _, _ = parseSelfDelimitedPacketAndPadding(data)
	})
}

func TestParseTOC(t *testing.T) {
	tests := []struct {
		name      string
		toc       byte
		config    uint8
		mode      Mode
		bandwidth Bandwidth
		frameSize int
		stereo    bool
		frameCode uint8
	}{
		// Basic config 0 variations
		{"config0_mono_code0", 0x00, 0, ModeSILK, BandwidthNarrowband, 480, false, 0},
		{"config0_stereo_code0", 0x04, 0, ModeSILK, BandwidthNarrowband, 480, true, 0},
		{"config0_mono_code1", 0x01, 0, ModeSILK, BandwidthNarrowband, 480, false, 1},
		{"config0_mono_code2", 0x02, 0, ModeSILK, BandwidthNarrowband, 480, false, 2},
		{"config0_mono_code3", 0x03, 0, ModeSILK, BandwidthNarrowband, 480, false, 3},
		{"config0_stereo_code3", 0x07, 0, ModeSILK, BandwidthNarrowband, 480, true, 3},

		// SILK NB configs 0-3
		{"silk_nb_10ms", 0x00, 0, ModeSILK, BandwidthNarrowband, 480, false, 0},
		{"silk_nb_20ms", 0x08, 1, ModeSILK, BandwidthNarrowband, 960, false, 0},
		{"silk_nb_40ms", 0x10, 2, ModeSILK, BandwidthNarrowband, 1920, false, 0},
		{"silk_nb_60ms", 0x18, 3, ModeSILK, BandwidthNarrowband, 2880, false, 0},

		// SILK MB configs 4-7
		{"silk_mb_10ms", 0x20, 4, ModeSILK, BandwidthMediumband, 480, false, 0},
		{"silk_mb_20ms", 0x28, 5, ModeSILK, BandwidthMediumband, 960, false, 0},
		{"silk_mb_40ms", 0x30, 6, ModeSILK, BandwidthMediumband, 1920, false, 0},
		{"silk_mb_60ms", 0x38, 7, ModeSILK, BandwidthMediumband, 2880, false, 0},

		// SILK WB configs 8-11
		{"silk_wb_10ms", 0x40, 8, ModeSILK, BandwidthWideband, 480, false, 0},
		{"silk_wb_20ms", 0x48, 9, ModeSILK, BandwidthWideband, 960, false, 0},
		{"silk_wb_40ms", 0x50, 10, ModeSILK, BandwidthWideband, 1920, false, 0},
		{"silk_wb_60ms", 0x58, 11, ModeSILK, BandwidthWideband, 2880, false, 0},

		// Hybrid SWB configs 12-13
		{"hybrid_swb_10ms", 0x60, 12, ModeHybrid, BandwidthSuperwideband, 480, false, 0},
		{"hybrid_swb_20ms", 0x68, 13, ModeHybrid, BandwidthSuperwideband, 960, false, 0},

		// Hybrid FB configs 14-15
		{"hybrid_fb_10ms", 0x70, 14, ModeHybrid, BandwidthFullband, 480, false, 0},
		{"hybrid_fb_20ms", 0x78, 15, ModeHybrid, BandwidthFullband, 960, false, 0},

		// CELT NB configs 16-19
		{"celt_nb_2.5ms", 0x80, 16, ModeCELT, BandwidthNarrowband, 120, false, 0},
		{"celt_nb_5ms", 0x88, 17, ModeCELT, BandwidthNarrowband, 240, false, 0},
		{"celt_nb_10ms", 0x90, 18, ModeCELT, BandwidthNarrowband, 480, false, 0},
		{"celt_nb_20ms", 0x98, 19, ModeCELT, BandwidthNarrowband, 960, false, 0},

		// CELT WB configs 20-23
		{"celt_wb_2.5ms", 0xA0, 20, ModeCELT, BandwidthWideband, 120, false, 0},
		{"celt_wb_5ms", 0xA8, 21, ModeCELT, BandwidthWideband, 240, false, 0},
		{"celt_wb_10ms", 0xB0, 22, ModeCELT, BandwidthWideband, 480, false, 0},
		{"celt_wb_20ms", 0xB8, 23, ModeCELT, BandwidthWideband, 960, false, 0},

		// CELT SWB configs 24-27
		{"celt_swb_2.5ms", 0xC0, 24, ModeCELT, BandwidthSuperwideband, 120, false, 0},
		{"celt_swb_5ms", 0xC8, 25, ModeCELT, BandwidthSuperwideband, 240, false, 0},
		{"celt_swb_10ms", 0xD0, 26, ModeCELT, BandwidthSuperwideband, 480, false, 0},
		{"celt_swb_20ms", 0xD8, 27, ModeCELT, BandwidthSuperwideband, 960, false, 0},

		// CELT FB configs 28-31
		{"celt_fb_2.5ms", 0xE0, 28, ModeCELT, BandwidthFullband, 120, false, 0},
		{"celt_fb_5ms", 0xE8, 29, ModeCELT, BandwidthFullband, 240, false, 0},
		{"celt_fb_10ms", 0xF0, 30, ModeCELT, BandwidthFullband, 480, false, 0},
		{"celt_fb_20ms", 0xF8, 31, ModeCELT, BandwidthFullband, 960, false, 0},

		// Config 31 with all variations
		{"config31_stereo_code0", 0xFC, 31, ModeCELT, BandwidthFullband, 960, true, 0},
		{"config31_mono_code1", 0xF9, 31, ModeCELT, BandwidthFullband, 960, false, 1},
		{"config31_stereo_code2", 0xFE, 31, ModeCELT, BandwidthFullband, 960, true, 2},
		{"config31_stereo_code3", 0xFF, 31, ModeCELT, BandwidthFullband, 960, true, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toc := ParseTOC(tt.toc)

			if toc.Config != tt.config {
				t.Errorf("Config: got %d, want %d", toc.Config, tt.config)
			}
			if toc.Mode != tt.mode {
				t.Errorf("Mode: got %d, want %d", toc.Mode, tt.mode)
			}
			if toc.Bandwidth != tt.bandwidth {
				t.Errorf("Bandwidth: got %d, want %d", toc.Bandwidth, tt.bandwidth)
			}
			if toc.FrameSize != tt.frameSize {
				t.Errorf("FrameSize: got %d, want %d", toc.FrameSize, tt.frameSize)
			}
			if toc.Stereo != tt.stereo {
				t.Errorf("Stereo: got %v, want %v", toc.Stereo, tt.stereo)
			}
			if toc.FrameCode != tt.frameCode {
				t.Errorf("FrameCode: got %d, want %d", toc.FrameCode, tt.frameCode)
			}
		})
	}
}

func TestParsePacketCode0(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		frameSize int
	}{
		{"1_byte_frame", []byte{0x00, 0xAA}, 1},
		{"10_byte_frame", make10BytePacket(), 10},
		{"100_byte_frame", make100BytePacket(), 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != 1 {
				t.Errorf("FrameCount: got %d, want 1", info.FrameCount)
			}
			if len(info.FrameSizes) != 1 {
				t.Errorf("len(FrameSizes): got %d, want 1", len(info.FrameSizes))
			}
			if info.FrameSizes[0] != tt.frameSize {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.frameSize)
			}
		})
	}
}

func make10BytePacket() []byte {
	packet := make([]byte, 11) // TOC + 10 bytes
	packet[0] = 0x00           // Code 0
	return packet
}

func make100BytePacket() []byte {
	packet := make([]byte, 101) // TOC + 100 bytes
	packet[0] = 0x00            // Code 0
	return packet
}

func TestParsePacketCode1(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frameSize  int
		expectErr  bool
		errMessage string
	}{
		{"two_10_byte_frames", makeCode1Packet(20), 10, false, ""},
		{"two_50_byte_frames", makeCode1Packet(100), 50, false, ""},
		{"odd_length_error", []byte{0x01, 0xAA, 0xBB, 0xCC}, 0, true, "invalid packet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != 2 {
				t.Errorf("FrameCount: got %d, want 2", info.FrameCount)
			}
			if len(info.FrameSizes) != 2 {
				t.Errorf("len(FrameSizes): got %d, want 2", len(info.FrameSizes))
			}
			if info.FrameSizes[0] != tt.frameSize {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.frameSize)
			}
			if info.FrameSizes[1] != tt.frameSize {
				t.Errorf("FrameSizes[1]: got %d, want %d", info.FrameSizes[1], tt.frameSize)
			}
		})
	}
}

func makeCode1Packet(frameDataLen int) []byte {
	packet := make([]byte, 1+frameDataLen) // TOC + frame data
	packet[0] = 0x01                       // Code 1
	return packet
}

func TestParsePacketCode2(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frame1Size int
		frame2Size int
	}{
		{
			"small_first_frame",
			// TOC, frame1_len=10, then 10+20 bytes of frame data
			append([]byte{0x02, 10}, make([]byte, 30)...),
			10, 20,
		},
		{
			"frame1_len_251",
			// TOC, frame1_len=251 (single byte), then 251+100 bytes
			append([]byte{0x02, 251}, make([]byte, 351)...),
			251, 100,
		},
		{
			"two_byte_encoding_252",
			// TOC, frame1_len=252 encoded as [252, 0], then frame data
			// length = 4*0 + 252 = 252
			append([]byte{0x02, 252, 0}, make([]byte, 352)...),
			252, 100,
		},
		{
			"two_byte_encoding_256",
			// TOC, frame1_len=256 encoded as [252, 1]
			// length = 4*1 + 252 = 256
			append([]byte{0x02, 252, 1}, make([]byte, 356)...),
			256, 100,
		},
		{
			"two_byte_encoding_large",
			// TOC, frame1_len=1020 encoded as [252, 192]
			// length = 4*192 + 252 = 768 + 252 = 1020
			append([]byte{0x02, 252, 192}, make([]byte, 1120)...),
			1020, 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != 2 {
				t.Errorf("FrameCount: got %d, want 2", info.FrameCount)
			}
			if info.FrameSizes[0] != tt.frame1Size {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.frame1Size)
			}
			if info.FrameSizes[1] != tt.frame2Size {
				t.Errorf("FrameSizes[1]: got %d, want %d", info.FrameSizes[1], tt.frame2Size)
			}
		})
	}
}

func TestParsePacketCode3CBR(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frameCount int
		frameSize  int
		padding    int
	}{
		{
			"cbr_2_frames",
			// TOC=0x03, frameCount=0x02 (CBR, no padding, M=2), frameLen=50
			append([]byte{0x03, 0x02}, make([]byte, 100)...),
			2, 50, 0,
		},
		{
			"cbr_3_frames",
			// TOC=0x03, frameCount=0x03 (CBR, no padding, M=3), frameLen=30
			append([]byte{0x03, 0x03}, make([]byte, 90)...),
			3, 30, 0,
		},
		{
			"cbr_1_frame",
			// TOC=0x03, frameCount=0x01 (CBR, no padding, M=1), no frameLen byte
			append([]byte{0x03, 0x01}, make([]byte, 50)...),
			1, 50, 0,
		},
		{
			"cbr_with_padding",
			// TOC=0x03, frameCount=0x42 (CBR, padding, M=2), padding=10, frameLen=50
			append([]byte{0x03, 0x42, 10}, make([]byte, 110)...),
			2, 50, 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != tt.frameCount {
				t.Errorf("FrameCount: got %d, want %d", info.FrameCount, tt.frameCount)
			}
			if info.Padding != tt.padding {
				t.Errorf("Padding: got %d, want %d", info.Padding, tt.padding)
			}
			for i, size := range info.FrameSizes {
				if size != tt.frameSize {
					t.Errorf("FrameSizes[%d]: got %d, want %d", i, size, tt.frameSize)
				}
			}
		})
	}
}

func TestParsePacketCode3VBR(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frameCount int
		frameSizes []int
		padding    int
	}{
		{
			"vbr_2_frames",
			// TOC=0x03, frameCount=0x82 (VBR, no padding, M=2), frame1Len=30
			// Total data: header(3) + frame1(30) + frame2(remainder)
			append([]byte{0x03, 0x82, 30}, make([]byte, 80)...),
			2, []int{30, 50}, 0,
		},
		{
			"vbr_3_frames",
			// TOC=0x03, frameCount=0x83 (VBR, no padding, M=3), frame1Len=20, frame2Len=30
			append([]byte{0x03, 0x83, 20, 30}, make([]byte, 100)...),
			3, []int{20, 30, 50}, 0,
		},
		{
			"vbr_with_padding",
			// TOC=0x03, frameCount=0xC2 (VBR, padding, M=2), padding=5, frame1Len=30
			append([]byte{0x03, 0xC2, 5, 30}, make([]byte, 85)...),
			2, []int{30, 50}, 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != tt.frameCount {
				t.Errorf("FrameCount: got %d, want %d", info.FrameCount, tt.frameCount)
			}
			if info.Padding != tt.padding {
				t.Errorf("Padding: got %d, want %d", info.Padding, tt.padding)
			}
			if len(info.FrameSizes) != len(tt.frameSizes) {
				t.Fatalf("len(FrameSizes): got %d, want %d", len(info.FrameSizes), len(tt.frameSizes))
			}
			for i, expected := range tt.frameSizes {
				if info.FrameSizes[i] != expected {
					t.Errorf("FrameSizes[%d]: got %d, want %d", i, info.FrameSizes[i], expected)
				}
			}
		})
	}
}

func TestTwoByteFrameLength(t *testing.T) {
	tests := []struct {
		name     string
		encoded  []byte
		expected int
		bytes    int
	}{
		{"length_0", []byte{0}, 0, 1},
		{"length_100", []byte{100}, 100, 1},
		{"length_251", []byte{251}, 251, 1},
		{"length_252", []byte{252, 0}, 252, 2},     // 4*0 + 252
		{"length_255", []byte{255, 0}, 255, 2},     // 4*0 + 255
		{"length_256", []byte{252, 1}, 256, 2},     // 4*1 + 252
		{"length_259", []byte{255, 1}, 259, 2},     // 4*1 + 255
		{"length_1020", []byte{252, 192}, 1020, 2}, // 4*192 + 252
		{"length_1275", []byte{255, 255}, 1275, 2}, // 4*255 + 255 (max two-byte)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test via a code 2 packet
			packetData := append([]byte{0x02}, tt.encoded...)
			packetData = append(packetData, make([]byte, tt.expected+50)...)

			info, err := ParsePacket(packetData)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameSizes[0] != tt.expected {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.expected)
			}
		})
	}
}

func TestParsePacketErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		err  error
	}{
		{"empty_packet", []byte{}, ErrPacketTooShort},
		{"code2_truncated", []byte{0x02}, ErrPacketTooShort},
		{"code3_truncated", []byte{0x03}, ErrPacketTooShort},
		{"code3_m_zero", []byte{0x03, 0x00}, ErrInvalidFrameCount},
		{"code3_m_49", []byte{0x03, 49}, ErrInvalidFrameCount},
		{"code3_m_63", []byte{0x03, 63}, ErrInvalidFrameCount},
		{
			"code2_two_byte_truncated",
			[]byte{0x02, 252}, // Two-byte encoding but missing second byte
			ErrPacketTooShort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePacket(tt.data)
			if err != tt.err {
				t.Errorf("error: got %v, want %v", err, tt.err)
			}
		})
	}
}

func TestParsePacketRejectsLibopusEnvelopeViolations(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "code0_implicit_frame_too_large",
			data: append([]byte{0x00}, make([]byte, maxOpusFrameBytes+1)...),
		},
		{
			name: "code1_implicit_frame_too_large",
			data: append([]byte{0x01}, make([]byte, (maxOpusFrameBytes+1)*2)...),
		},
		{
			name: "code2_implicit_last_frame_too_large",
			data: append([]byte{0x02, 1, 0xAA}, make([]byte, maxOpusFrameBytes+1)...),
		},
		{
			name: "code3_duration_over_120ms",
			data: []byte{GenerateTOC(31, false, 3), 0x07},
		},
		{
			name: "code3_cbr_implicit_frame_too_large",
			data: append([]byte{GenerateTOC(16, false, 3), 0x02}, make([]byte, (maxOpusFrameBytes+1)*2)...),
		},
		{
			name: "code3_vbr_implicit_last_frame_too_large",
			data: append([]byte{GenerateTOC(16, false, 3), 0x82, 1, 0xAA}, make([]byte, maxOpusFrameBytes+1)...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParsePacket(tt.data); err != ErrInvalidPacket {
				t.Fatalf("ParsePacket error=%v want %v", err, ErrInvalidPacket)
			}
		})
	}
}

func TestParsePacketCode3MaxFrames(t *testing.T) {
	// Test M=48 (maximum allowed)
	// 48 2.5 ms frames is exactly the libopus 120 ms packet-duration limit.
	// TOC=CELT NB 2.5 ms code 3, frameCount=0xB0 (VBR, no padding, M=48=0x30)
	// Need 47 frame lengths + last frame is remainder
	header := []byte{GenerateTOC(16, false, 3), 0xB0} // VBR, M=48
	frameLens := make([]byte, 47)
	for i := range frameLens {
		frameLens[i] = 10 // Each frame is 10 bytes
	}
	frameData := make([]byte, 48*10) // 48 frames of 10 bytes each
	packet := append(header, frameLens...)
	packet = append(packet, frameData...)

	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.FrameCount != 48 {
		t.Errorf("FrameCount: got %d, want 48", info.FrameCount)
	}
}

func TestParsePacketCode3ContinuationPadding(t *testing.T) {
	// Test continuation bytes in padding
	// Padding = 254 + 254 + 10 = 518 (each 255 adds 254)
	header := []byte{0x03, 0x42} // CBR, padding, M=2
	padding := []byte{255, 255, 10}
	frameData := make([]byte, 100+518) // 2*50 frames + 518 padding

	packet := append(header, padding...)
	packet = append(packet, frameData...)

	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Padding != 518 {
		t.Errorf("Padding: got %d, want 518", info.Padding)
	}
	if info.FrameCount != 2 {
		t.Errorf("FrameCount: got %d, want 2", info.FrameCount)
	}
}

func TestGenerateTOC(t *testing.T) {
	tests := []struct {
		name      string
		config    uint8
		stereo    bool
		frameCode uint8
		expected  byte
	}{
		// Basic cases
		{"config0_mono_code0", 0, false, 0, 0x00},
		{"config0_stereo_code0", 0, true, 0, 0x04},
		{"config0_mono_code1", 0, false, 1, 0x01},
		{"config0_mono_code2", 0, false, 2, 0x02},
		{"config0_mono_code3", 0, false, 3, 0x03},
		{"config0_stereo_code3", 0, true, 3, 0x07},

		// Hybrid configs (12-15)
		{"hybrid_swb_10ms", 12, false, 0, 0x60},
		{"hybrid_swb_20ms", 13, false, 0, 0x68},
		{"hybrid_fb_10ms", 14, false, 0, 0x70},
		{"hybrid_fb_20ms", 15, false, 0, 0x78},

		// CELT FB config 31
		{"celt_fb_20ms", 31, false, 0, 0xF8},
		{"celt_fb_20ms_stereo", 31, true, 0, 0xFC},
		{"celt_fb_20ms_code3", 31, true, 3, 0xFF},

		// Verify masking works for out-of-range values
		{"config_masked", 0x3F, false, 0, 0xF8},    // 0x3F & 0x1F = 0x1F = 31
		{"frameCode_masked", 0, false, 0x0F, 0x03}, // 0x0F & 0x03 = 3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateTOC(tt.config, tt.stereo, tt.frameCode)
			if got != tt.expected {
				t.Errorf("GenerateTOC(%d, %v, %d) = 0x%02X, want 0x%02X",
					tt.config, tt.stereo, tt.frameCode, got, tt.expected)
			}
		})
	}
}

func TestGenerateTOCRoundTrip(t *testing.T) {
	// Test round-trip for all 32 configs with all stereo/frameCode combinations
	for config := range uint8(32) {
		for _, stereo := range []bool{false, true} {
			for frameCode := range uint8(4) {
				toc := GenerateTOC(config, stereo, frameCode)
				parsed := ParseTOC(toc)

				if parsed.Config != config {
					t.Errorf("config=%d stereo=%v code=%d: Config mismatch: got %d",
						config, stereo, frameCode, parsed.Config)
				}
				if parsed.Stereo != stereo {
					t.Errorf("config=%d stereo=%v code=%d: Stereo mismatch: got %v",
						config, stereo, frameCode, parsed.Stereo)
				}
				if parsed.FrameCode != frameCode {
					t.Errorf("config=%d stereo=%v code=%d: FrameCode mismatch: got %d",
						config, stereo, frameCode, parsed.FrameCode)
				}
			}
		}
	}
}

func TestConfigFromParams(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		bandwidth Bandwidth
		frameSize int
		expected  int
	}{
		// SILK NB
		{"silk_nb_10ms", ModeSILK, BandwidthNarrowband, 480, 0},
		{"silk_nb_20ms", ModeSILK, BandwidthNarrowband, 960, 1},
		{"silk_nb_40ms", ModeSILK, BandwidthNarrowband, 1920, 2},
		{"silk_nb_60ms", ModeSILK, BandwidthNarrowband, 2880, 3},

		// SILK MB
		{"silk_mb_10ms", ModeSILK, BandwidthMediumband, 480, 4},
		{"silk_mb_20ms", ModeSILK, BandwidthMediumband, 960, 5},

		// SILK WB
		{"silk_wb_10ms", ModeSILK, BandwidthWideband, 480, 8},
		{"silk_wb_20ms", ModeSILK, BandwidthWideband, 960, 9},

		// Hybrid SWB (configs 12-13)
		{"hybrid_swb_10ms", ModeHybrid, BandwidthSuperwideband, 480, 12},
		{"hybrid_swb_20ms", ModeHybrid, BandwidthSuperwideband, 960, 13},

		// Hybrid FB (configs 14-15)
		{"hybrid_fb_10ms", ModeHybrid, BandwidthFullband, 480, 14},
		{"hybrid_fb_20ms", ModeHybrid, BandwidthFullband, 960, 15},

		// CELT NB
		{"celt_nb_2.5ms", ModeCELT, BandwidthNarrowband, 120, 16},
		{"celt_nb_5ms", ModeCELT, BandwidthNarrowband, 240, 17},
		{"celt_nb_10ms", ModeCELT, BandwidthNarrowband, 480, 18},
		{"celt_nb_20ms", ModeCELT, BandwidthNarrowband, 960, 19},

		// CELT FB
		{"celt_fb_2.5ms", ModeCELT, BandwidthFullband, 120, 28},
		{"celt_fb_5ms", ModeCELT, BandwidthFullband, 240, 29},
		{"celt_fb_10ms", ModeCELT, BandwidthFullband, 480, 30},
		{"celt_fb_20ms", ModeCELT, BandwidthFullband, 960, 31},

		// Invalid combinations
		{"invalid_hybrid_wb", ModeHybrid, BandwidthWideband, 960, -1},
		{"invalid_silk_fb", ModeSILK, BandwidthFullband, 960, -1},
		{"invalid_celt_40ms", ModeCELT, BandwidthFullband, 1920, -1},
		{"invalid_framesize", ModeSILK, BandwidthNarrowband, 100, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfigFromParams(tt.mode, tt.bandwidth, tt.frameSize)
			if got != tt.expected {
				t.Errorf("ConfigFromParams(%v, %v, %d) = %d, want %d",
					tt.mode, tt.bandwidth, tt.frameSize, got, tt.expected)
			}
		})
	}
}

func TestValidConfig(t *testing.T) {
	// Valid configs 0-31
	for i := range uint8(32) {
		if !ValidConfig(i) {
			t.Errorf("ValidConfig(%d) = false, want true", i)
		}
	}

	// Invalid configs 32+
	for i := uint8(32); i < 255; i++ {
		if ValidConfig(i) {
			t.Errorf("ValidConfig(%d) = true, want false", i)
		}
	}
}

type libopusPacketParseResult struct {
	bandwidth       int32 // OPUS_BANDWIDTH_* (1101-1105) or error code
	nbChannels      int32 // 1 or 2
	nbFrames        int32 // >=1 or OPUS_BAD_ARG(-1) / OPUS_INVALID_PACKET(-4)
	samplesPerFrame int32 // at 48000
	nbSamples       int32 // at 48000 or error
	parseRet        int32 // frame count or error from opus_packet_parse
	parseTOC        int32 // TOC byte (or -1 on parse error)
	payloadOffset   int32 // (or -1 on parse error)
	nFrameSizes     int32 // == parseRet when parseRet > 0
	frameSizes      []int16
}

// libopusPacketParseCase is one test vector.
type libopusPacketParseCase struct {
	name   string
	packet []byte
}

var tocEdgeParityHelper libopustest.HelperCache

func getTOCEdgeHelperPath() (string, error) {
	return tocEdgeParityHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "packet parse",
		OutputBase: "gopus_libopus_packet_parse",
		SourceFile: "libopus_packet_parse_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

func probeLibopusPacketParse(cases []libopusPacketParseCase) ([]libopusPacketParseResult, error) {
	binPath, err := getTOCEdgeHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayload("GPPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packet)))
		payload.Raw(tc.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "packet parse", "GPPO")
	if err != nil {
		return nil, err
	}
	// Each output has variable size: 9×i32 + nFrameSizes×i16.
	// We can't pre-calculate ExpectRemaining, so read directly.
	reader.Count(len(cases))
	if reader.Err() != nil {
		return nil, reader.Err()
	}

	out := make([]libopusPacketParseResult, len(cases))
	for i := range out {
		r := &out[i]
		r.bandwidth = reader.I32()
		r.nbChannels = reader.I32()
		r.nbFrames = reader.I32()
		r.samplesPerFrame = reader.I32()
		r.nbSamples = reader.I32()
		r.parseRet = reader.I32()
		r.parseTOC = reader.I32()
		r.payloadOffset = reader.I32()
		r.nFrameSizes = reader.I32()
		if r.nFrameSizes > 0 {
			r.frameSizes = make([]int16, r.nFrameSizes)
			for j := range r.frameSizes {
				r.frameSizes[j] = reader.I16()
			}
		}
		if reader.Err() != nil {
			return nil, reader.Err()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// libopusBandwidthCode maps gopus Bandwidth (0-4) to the OPUS_BANDWIDTH_*
// integer values returned by opus_packet_get_bandwidth().
// src/opus_decoder.c opus_packet_get_bandwidth()
func libopusBandwidthCode(bw Bandwidth) int32 {
	// OPUS_BANDWIDTH_NARROWBAND=1101 … OPUS_BANDWIDTH_FULLBAND=1105
	return int32(1101) + int32(bw)
}

// TestTOCEdgeAllBytesMatchLibopus verifies that ParseTOC fields for all 256
// TOC bytes match what libopus derives via opus_packet_get_bandwidth() and
// opus_packet_get_nb_channels().  A minimal one-byte packet is used so the
// oracle can be called (zero-length is OPUS_INVALID_PACKET).
func TestTOCEdgeAllBytesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := make([]libopusPacketParseCase, 256)
	for b := range 256 {
		// TOC-only packet: len=1, no payload bytes.
		// opus_packet_get_bandwidth / _nb_channels only need data[0].
		// opus_packet_get_nb_frames with len=1 returns 1 for code 0/1/2
		// but OPUS_INVALID_PACKET for code 3.
		cases[b] = libopusPacketParseCase{
			name:   fmt.Sprintf("toc_0x%02x", b),
			packet: []byte{byte(b)},
		}
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for b := range 256 {
		t.Run(cases[b].name, func(t *testing.T) {
			toc := ParseTOC(byte(b))
			w := want[b]

			// Bandwidth: libopus returns OPUS_BANDWIDTH_NARROWBAND(1101) .. FULLBAND(1105).
			wantBW := w.bandwidth
			gotBW := libopusBandwidthCode(toc.Bandwidth)
			if gotBW != wantBW {
				t.Errorf("bandwidth: got %d want %d (toc=0x%02x)", gotBW, wantBW, b)
			}

			// Channels: libopus (data[0]&0x4)?2:1
			wantCh := w.nbChannels
			gotCh := int32(1)
			if toc.Stereo {
				gotCh = 2
			}
			if gotCh != wantCh {
				t.Errorf("channels: got %d want %d (toc=0x%02x)", gotCh, wantCh, b)
			}
		})
	}
}

// TestTOCEdgeNbFramesMatchLibopus checks packetFrameCountLibopus vs
// opus_packet_get_nb_frames for every TOC byte with one-byte packets.
func TestTOCEdgeNbFramesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := make([]libopusPacketParseCase, 256)
	for b := range 256 {
		cases[b] = libopusPacketParseCase{
			name:   fmt.Sprintf("toc_0x%02x", b),
			packet: []byte{byte(b)},
		}
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for b := range 256 {
		t.Run(cases[b].name, func(t *testing.T) {
			w := want[b]
			got, gotErr := packetFrameCountLibopus(cases[b].packet)

			if w.nbFrames < 0 {
				// libopus returned error; gopus must also return an error
				if gotErr == nil {
					t.Errorf("packetFrameCountLibopus: got %d, nil err; want error (libopus=%d)", got, w.nbFrames)
				}
			} else {
				if gotErr != nil {
					t.Errorf("packetFrameCountLibopus: got err=%v; want %d", gotErr, w.nbFrames)
				} else if int32(got) != w.nbFrames {
					t.Errorf("packetFrameCountLibopus: got %d want %d", got, w.nbFrames)
				}
			}
		})
	}
}

// TestTOCEdgeSamplesPerFrameMatchLibopus verifies packetSamplesPerFrameAtRate
// vs opus_packet_get_samples_per_frame for every TOC byte at 48kHz.
func TestTOCEdgeSamplesPerFrameMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := make([]libopusPacketParseCase, 256)
	for b := range 256 {
		cases[b] = libopusPacketParseCase{
			name:   fmt.Sprintf("toc_0x%02x", b),
			packet: []byte{byte(b)},
		}
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for b := range 256 {
		t.Run(cases[b].name, func(t *testing.T) {
			w := want[b]
			got, gotErr := packetSamplesPerFrameAtRate(cases[b].packet, 48000)
			if gotErr != nil {
				t.Fatalf("packetSamplesPerFrameAtRate: unexpected error %v", gotErr)
			}
			if int32(got) != w.samplesPerFrame {
				t.Errorf("samplesPerFrame: got %d want %d", got, w.samplesPerFrame)
			}
		})
	}
}

// TestTOCEdgeParsePacketCode0AllConfigsMatchLibopus verifies ParsePacket for
// code-0 packets across all 32 configs × stereo.
func TestTOCEdgeParsePacketCode0AllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode0Cases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode1AllConfigsMatchLibopus verifies ParsePacket for
// code-1 packets across all 32 configs × stereo.
func TestTOCEdgeParsePacketCode1AllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode1Cases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode2AllConfigsMatchLibopus verifies ParsePacket for
// code-2 packets across all 32 configs × stereo.
func TestTOCEdgeParsePacketCode2AllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode2Cases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode3CBRAllMMatchLibopus verifies ParsePacket for
// code-3 CBR across all valid M values (1..48) for every config × stereo.
func TestTOCEdgeParsePacketCode3CBRAllMMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode3CBRAllMCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode3VBRAllMMatchLibopus verifies ParsePacket for
// code-3 VBR across all valid M values (1..48).
func TestTOCEdgeParsePacketCode3VBRAllMMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode3VBRAllMCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketPaddingVariantsMatchLibopus exercises all padding-length
// boundary variants: 0, 1, 127, 254, and the 255-chained case (≥256 pad bytes).
func TestTOCEdgeParsePacketPaddingVariantsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgePaddingCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketBoundaryRejectMatchLibopus verifies that gopus and
// libopus agree on rejection for every structurally invalid boundary packet:
// truncated code-2, code-3 with M=0/49+, over-duration, CBR non-divisible.
func TestTOCEdgeParsePacketBoundaryRejectMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeBoundaryCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParseAcceptRejectParity(t, cases, want)
}

// TestTOCEdgeNbSamplesAllConfigsMatchLibopus checks packetSamplesAtRate for a
// valid multi-frame packet across all 32 configs at 48kHz.
func TestTOCEdgeNbSamplesAllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeNbSamplesCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	for i, tc := range cases {
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			got, gotErr := packetSamplesAtRate(tc.packet, 48000)
			if w.nbSamples < 0 {
				if gotErr == nil {
					t.Errorf("packetSamplesAtRate: got %d nil err; want error (libopus=%d)", got, w.nbSamples)
				}
			} else {
				if gotErr != nil {
					t.Errorf("packetSamplesAtRate: err=%v want %d", gotErr, w.nbSamples)
				} else if int32(got) != w.nbSamples {
					t.Errorf("packetSamplesAtRate: got %d want %d", got, w.nbSamples)
				}
			}
		})
	}
}

// ── case builders ────────────────────────────────────────────────────────────

// tocEdgeCode0Cases builds one code-0 case per config × stereo.
func tocEdgeCode0Cases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := range uint8(32) {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 0)
			// 50 bytes of payload — fits in maxOpusFrameBytes (1275)
			pkt := make([]byte, 51)
			pkt[0] = toc
			for j := 1; j < len(pkt); j++ {
				pkt[j] = byte(j & 0xFF)
			}
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases, libopusPacketParseCase{
				name:   fmt.Sprintf("code0_cfg%d_%s", config, s),
				packet: pkt,
			})
		}
	}
	return cases
}

// tocEdgeCode1Cases builds code-1 cases (two equal frames) for all configs × stereo.
func tocEdgeCode1Cases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := range uint8(32) {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 1)
			// 100 bytes payload = two 50-byte frames
			pkt := make([]byte, 101)
			pkt[0] = toc
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases, libopusPacketParseCase{
				name:   fmt.Sprintf("code1_cfg%d_%s", config, s),
				packet: pkt,
			})
		}
	}
	return cases
}

// tocEdgeCode2Cases builds code-2 cases for all configs × stereo with a
// one-byte frame-length header (frame0=30, frame1=remainder).
func tocEdgeCode2Cases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := range uint8(32) {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 2)
			// Header: TOC, length=30, then 30+50=80 bytes payload
			pkt := make([]byte, 82) // 1 toc + 1 len + 30 + 50
			pkt[0] = toc
			pkt[1] = 30
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases, libopusPacketParseCase{
				name:   fmt.Sprintf("code2_cfg%d_%s", config, s),
				packet: pkt,
			})
		}
	}
	return cases
}

// tocEdgeCode3CBRAllMCases generates CBR code-3 packets for every valid M (1..48)
// for the CELT NB 2.5ms config (config=16), which has the smallest frame size
// (120 samples) allowing M=48 within the 120ms limit.
func tocEdgeCode3CBRAllMCases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase

	// config 16 = CELT NB 2.5ms: frameSize=120 samples → 120*48=5760 ≤ 5760 ✓
	for _, config := range []uint8{16, 18, 0} { // also 10ms CELT, 10ms SILK
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 3)
			frameSize48k := configTable[config].FrameSize
			s := "m"
			if stereo {
				s = "s"
			}
			for m := 1; m <= 48; m++ {
				totalSamples := frameSize48k * m
				if totalSamples > maxRepacketizerDuration48k {
					break // M too large for this config
				}
				// CBR: frameCountByte has VBR=0, pad=0, M=m
				frameCountByte := byte(m & 0x3F)
				// Use 10 bytes per frame
				frameDataLen := 10 * m
				pkt := make([]byte, 2+frameDataLen)
				pkt[0] = toc
				pkt[1] = frameCountByte
				cases = append(cases, libopusPacketParseCase{
					name:   fmt.Sprintf("code3_cbr_cfg%d_%s_m%d", config, s, m),
					packet: pkt,
				})
			}
		}
	}
	return cases
}

// tocEdgeCode3VBRAllMCases generates VBR code-3 packets for M=1..48 using
// config 16 (CELT NB 2.5ms).
func tocEdgeCode3VBRAllMCases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase

	// config 16 = CELT NB 2.5ms
	for _, config := range []uint8{16, 18} {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 3)
			frameSize48k := configTable[config].FrameSize
			s := "m"
			if stereo {
				s = "s"
			}
			for m := 1; m <= 48; m++ {
				totalSamples := frameSize48k * m
				if totalSamples > maxRepacketizerDuration48k {
					break
				}
				// VBR: frameCountByte has VBR=1, pad=0, M=m
				frameCountByte := byte(0x80 | (m & 0x3F))
				// Each of the first m-1 frames has explicit size=10 (single byte < 252)
				// last frame is implicit (10 bytes too)
				header := make([]byte, 2+(m-1))
				header[0] = toc
				header[1] = frameCountByte
				for i := 0; i < m-1; i++ {
					header[2+i] = 10
				}
				framePayload := make([]byte, 10*m)
				pkt := append(header, framePayload...)
				cases = append(cases, libopusPacketParseCase{
					name:   fmt.Sprintf("code3_vbr_cfg%d_%s_m%d", config, s, m),
					packet: pkt,
				})
			}
		}
	}
	return cases
}

// tocEdgePaddingCases generates code-3 CBR packets with various padding lengths.
func tocEdgePaddingCases() []libopusPacketParseCase {
	// config 16 = CELT NB 2.5ms, mono, code 3
	toc := GenerateTOC(16, false, 3)

	makePaddedCBR := func(name string, m int, padBytes []byte, frameDataLen int) libopusPacketParseCase {
		// frameCountByte: VBR=0, pad=1, M=m
		frameCountByte := byte(0x40 | (m & 0x3F))
		pkt := []byte{toc, frameCountByte}
		pkt = append(pkt, padBytes...)
		pkt = append(pkt, make([]byte, frameDataLen)...)
		return libopusPacketParseCase{name: name, packet: pkt}
	}

	cases := []libopusPacketParseCase{
		// pad=0: single terminator byte 0x00
		makePaddedCBR("pad0_m2", 2, []byte{0x00}, 20),
		// pad=1: single byte 0x01
		makePaddedCBR("pad1_m2", 2, []byte{0x01}, 21),
		// pad=127
		makePaddedCBR("pad127_m2", 2, []byte{0x7F}, 127+20),
		// pad=254: single byte 0xFE
		makePaddedCBR("pad254_m2", 2, []byte{0xFE}, 254+20),
		// pad=255-chained: 0xFF (adds 254) + 0x01 (adds 1) = 255 total
		makePaddedCBR("pad255_chain_m2", 2, []byte{0xFF, 0x01}, 255+20),
		// pad=508: 0xFF (254) + 0xFF (254) + 0x00 = 508 total
		makePaddedCBR("pad508_chain_m2", 2, []byte{0xFF, 0xFF, 0x00}, 508+20),
		// pad=509: 0xFF (254) + 0xFF (254) + 0x01 = 509 total
		makePaddedCBR("pad509_chain_m2", 2, []byte{0xFF, 0xFF, 0x01}, 509+20),
	}

	// VBR padded
	makeVBRPadded := func(name string, m int, padBytes []byte) libopusPacketParseCase {
		// VBR=1, pad=1, M=m
		frameCountByte := byte(0xC0 | (m & 0x3F))
		header := []byte{toc, frameCountByte}
		header = append(header, padBytes...)
		// m-1 explicit frame sizes (each = 10), then 10*m payload
		for i := 0; i < m-1; i++ {
			header = append(header, 10)
		}
		pkt := append(header, make([]byte, 10*m)...)
		return libopusPacketParseCase{name: name, packet: pkt}
	}
	padLen := 5
	padBytes := []byte{byte(padLen)}
	frameCount := 3
	// Ensure M frames fit within 120ms
	_ = padLen
	cases = append(cases,
		makeVBRPadded("vbr_pad5_m3", frameCount, padBytes),
		makeVBRPadded("vbr_pad0_m1", 1, []byte{0x00}),
		makeVBRPadded("vbr_pad254_m2", 2, []byte{0xFE}),
	)

	return cases
}

// tocEdgeBoundaryCases generates structurally invalid packets that libopus
// and gopus should both reject.
func tocEdgeBoundaryCases() []libopusPacketParseCase {
	// config 16 = CELT NB 2.5ms (120 samples/frame); 48*120=5760 ≤ 5760
	// config 31 = CELT FB 20ms (960 samples/frame); 7*960=6720 > 5760 ⇒ reject M=7

	return []libopusPacketParseCase{
		{name: "empty", packet: []byte{}},
		// code 2 with no length byte
		{name: "code2_toc_only", packet: []byte{GenerateTOC(0, false, 2)}},
		// code 2 with truncated two-byte length (only first byte)
		{name: "code2_two_byte_len_truncated", packet: []byte{GenerateTOC(0, false, 2), 252}},
		// code 3 with no frame-count byte
		{name: "code3_no_count_byte", packet: []byte{GenerateTOC(16, false, 3)}},
		// code 3 with M=0
		{name: "code3_m0", packet: []byte{GenerateTOC(16, false, 3), 0x00}},
		// code 3 with M=49 (>48)
		{name: "code3_m49", packet: []byte{GenerateTOC(16, false, 3), 49}},
		// code 3 with M=63 (max of 6-bit field, still >48)
		{name: "code3_m63", packet: []byte{GenerateTOC(16, false, 3), 63}},
		// code 3 CELT FB 20ms M=7: 7*960=6720 > 5760ms → over-duration
		{name: "code3_over_duration", packet: []byte{GenerateTOC(31, false, 3), 0x07}},
		// code 3 CBR non-divisible: 3 frames, 7 payload bytes (7%3≠0)
		{name: "code3_cbr_nondivisible", packet: append([]byte{GenerateTOC(16, false, 3), 0x03}, make([]byte, 7)...)},
		// code 0 frame too large (maxOpusFrameBytes+1 = 1276 payload bytes)
		{name: "code0_frame_too_large", packet: append([]byte{GenerateTOC(0, false, 0)}, make([]byte, maxOpusFrameBytes+1)...)},
		// code 1 each frame too large
		{name: "code1_frame_too_large", packet: append([]byte{GenerateTOC(0, false, 1)}, make([]byte, (maxOpusFrameBytes+1)*2)...)},
		// code 2 with explicit frame1 size larger than remaining data
		{name: "code2_frame1_overrun", packet: []byte{GenerateTOC(0, false, 2), 200, 0xAA}},
		// code 3 VBR explicit frame size overrun
		{name: "code3_vbr_frame_overrun", packet: []byte{GenerateTOC(16, false, 3), 0x82, 200, 0xAA}},
		// code 3 CBR padding that consumes all remaining bytes (len<0 after pad)
		// TOC + count(pad|M=1) + huge pad byte chain > packet length
		{name: "code3_padding_overrun", packet: []byte{GenerateTOC(16, false, 3), 0x41, 0xFF}},
	}
}

// tocEdgeNbSamplesCases covers every config with a minimal valid packet so we
// can test packetSamplesAtRate vs opus_packet_get_nb_samples.
func tocEdgeNbSamplesCases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := range uint8(32) {
		toc := GenerateTOC(config, false, 0)
		pkt := make([]byte, 51)
		pkt[0] = toc
		cases = append(cases, libopusPacketParseCase{
			name:   fmt.Sprintf("nbsamples_cfg%d", config),
			packet: pkt,
		})
	}
	// Code-3 M=2 CBR for each CELT config to hit the multi-frame path
	for _, config := range []uint8{16, 17, 18, 19, 28, 29, 30, 31} {
		toc := GenerateTOC(config, false, 3)
		frameSize := 10
		pkt := append([]byte{toc, 0x02}, make([]byte, frameSize*2)...)
		cases = append(cases, libopusPacketParseCase{
			name:   fmt.Sprintf("nbsamples_code3_cfg%d_m2", config),
			packet: pkt,
		})
	}
	// Over-duration: must be rejected
	// config 31 (960 samples), M=7: 6720 > 5760
	cases = append(cases, libopusPacketParseCase{
		name:   "nbsamples_overduration",
		packet: []byte{GenerateTOC(31, false, 3), 0x07},
	})
	return cases
}

// ── assertion helpers ─────────────────────────────────────────────────────────

// assertParsePacketParity asserts that ParsePacket produces identical results
// to opus_packet_parse for every case in cases.
func assertParsePacketParity(t *testing.T, cases []libopusPacketParseCase, want []libopusPacketParseResult) {
	t.Helper()
	for i, tc := range cases {
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			info, gotErr := ParsePacket(tc.packet)

			if w.parseRet < 0 {
				// libopus rejected the packet; gopus must also reject
				if gotErr == nil {
					t.Fatalf("ParsePacket: got nil error (frameCount=%d); libopus returned error %d",
						info.FrameCount, w.parseRet)
				}
				return
			}

			// libopus accepted
			if gotErr != nil {
				t.Fatalf("ParsePacket: got error %v; libopus accepted (count=%d)", gotErr, w.parseRet)
			}

			// Frame count
			if int32(info.FrameCount) != w.parseRet {
				t.Errorf("FrameCount: got %d want %d", info.FrameCount, w.parseRet)
			}

			// TOC byte
			if len(tc.packet) > 0 {
				wantTOCByte := byte(w.parseTOC)
				// libopus out_toc preserves the full TOC byte:
				// toc = *data++; ... if (out_toc) *out_toc = toc;
				gotTOCByte := tc.packet[0]
				if gotTOCByte != wantTOCByte {
					t.Errorf("TOC byte: got 0x%02x want 0x%02x", gotTOCByte, wantTOCByte)
				}
			}

			// Frame sizes
			if int32(len(info.FrameSizes)) != w.nFrameSizes {
				t.Errorf("len(FrameSizes): got %d want %d", len(info.FrameSizes), w.nFrameSizes)
				return
			}
			for j, sz := range info.FrameSizes {
				wantSz := int(w.frameSizes[j])
				if sz != wantSz {
					t.Errorf("FrameSizes[%d]: got %d want %d", j, sz, wantSz)
				}
			}
		})
	}
}

// assertParseAcceptRejectParity only checks accept/reject parity (for boundary
// reject cases we do not assert parsed field values).
func assertParseAcceptRejectParity(t *testing.T, cases []libopusPacketParseCase, want []libopusPacketParseResult) {
	t.Helper()
	for i, tc := range cases {
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			_, gotErr := ParsePacket(tc.packet)

			libopusAccepted := w.parseRet > 0
			gopusAccepted := gotErr == nil

			if gopusAccepted != libopusAccepted {
				if libopusAccepted {
					t.Errorf("ParsePacket: gopus rejected but libopus accepted (ret=%d): %v",
						w.parseRet, gotErr)
				} else {
					t.Errorf("ParsePacket: gopus accepted but libopus rejected (ret=%d)",
						w.parseRet)
				}
			}
		})
	}
}
