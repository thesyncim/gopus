package gopus

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
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

func TestPacketPadUnpadCallerOwnedCapacity(t *testing.T) {
	packet := []byte{0x48, 0x11, 0x22, 0x33}
	storage := bytes.Repeat([]byte{0xa5}, 32)
	copy(storage, packet)
	input := storage[:len(packet)]
	if err := PacketPad(input, len(packet), 24); err != nil {
		t.Fatalf("PacketPad with spare capacity: %v", err)
	}
	if len(input) != len(packet) {
		t.Fatalf("PacketPad changed the caller slice length to %d, want %d", len(input), len(packet))
	}
	if info, err := ParsePacket(storage[:24]); err != nil || info.TotalSize != 24 {
		t.Fatalf("ParsePacket(padded)=(%+v,%v), want a valid 24-byte packet", info, err)
	}
	if !bytes.Equal(storage[24:], bytes.Repeat([]byte{0xa5}, len(storage)-24)) {
		t.Fatalf("PacketPad wrote past newLen: tail=%x", storage[24:])
	}

	n, err := PacketUnpad(storage[:24], 24)
	if err != nil {
		t.Fatalf("PacketUnpad with spare capacity: %v", err)
	}
	if got := storage[:n]; !bytes.Equal(got, packet) {
		t.Fatalf("PacketUnpad=%x, want %x", got, packet)
	}
}

func TestPacketUnpadCanonicalizesAndDropsOpaquePadding(t *testing.T) {
	t.Run("noncanonical framing", func(t *testing.T) {
		packet := code3VBRPacket(18, false, []int{1, 1}, 7)
		want := append([]byte{GenerateTOC(18, false, 1)}, packet[len(packet)-2:]...)
		n, err := PacketUnpad(packet, len(packet))
		if err != nil {
			t.Fatalf("PacketUnpad: %v", err)
		}
		if got := packet[:n]; !bytes.Equal(got, want) {
			t.Fatalf("PacketUnpad=%x, want canonical code-1 packet %x", got, want)
		}
	})

	t.Run("malformed opaque extension", func(t *testing.T) {
		packet := mustDecodeHex(t, "4b4102112233ffff")
		want := []byte{0x48, 0x11, 0x22, 0x33}
		n, err := PacketUnpad(packet, len(packet))
		if err != nil {
			t.Fatalf("PacketUnpad: %v", err)
		}
		if got := packet[:n]; !bytes.Equal(got, want) {
			t.Fatalf("PacketUnpad=%x, want %x", got, want)
		}
	})

	t.Run("truncated padding length", func(t *testing.T) {
		packet := []byte{0x4b, 0x41, 0xff}
		before := append([]byte(nil), packet...)
		if _, err := PacketUnpad(packet, len(packet)); err != ErrPacketTooShort {
			t.Fatalf("PacketUnpad error=%v, want %v", err, ErrPacketTooShort)
		}
		if !bytes.Equal(packet, before) {
			t.Fatalf("PacketUnpad changed malformed input: got %x want %x", packet, before)
		}
	})
}

func TestPacketPadUnpad48FrameVBR(t *testing.T) {
	packet := code3VBRPacket(16, false, packet48VBRFrameSizes(), 0x20)
	newLen := len(packet) + 43
	storage := make([]byte, len(packet), newLen)
	copy(storage, packet)
	if err := PacketPad(storage, len(packet), newLen); err != nil {
		t.Fatalf("PacketPad(48-frame VBR): %v", err)
	}
	n, err := PacketUnpad(storage[:newLen], newLen)
	if err != nil {
		t.Fatalf("PacketUnpad(48-frame VBR): %v", err)
	}
	if got := storage[:n]; !bytes.Equal(got, packet) {
		t.Fatalf("pad/unpad changed 48-frame VBR packet: got %x want %x", got, packet)
	}
}

func TestPacketPadExtensionFallbackWithShortSliceAndSpareCapacity(t *testing.T) {
	packet := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantPadded := mustDecodeHex(t, "4b410a112233010101010baa50deadbe")
	storage := make([]byte, len(packet), len(wantPadded))
	copy(storage, packet)
	if err := PacketPad(storage[:len(packet)], len(packet), len(wantPadded)); err != nil {
		t.Fatalf("PacketPad(extension packet): %v", err)
	}
	if got := storage[:len(wantPadded)]; !bytes.Equal(got, wantPadded) {
		t.Fatalf("PacketPad(extension packet)=%x, want %x", got, wantPadded)
	}
	n, err := PacketUnpad(storage[:len(wantPadded)], len(wantPadded))
	if err != nil {
		t.Fatalf("PacketUnpad(extension packet): %v", err)
	}
	wantUnpadded := []byte{0x48, 0x11, 0x22, 0x33}
	if got := storage[:n]; !bytes.Equal(got, wantUnpadded) {
		t.Fatalf("PacketUnpad(extension packet)=%x, want %x", got, wantUnpadded)
	}
}

func TestPacketPadLargeExtensionPaddingRoundTrip(t *testing.T) {
	packet := packetWithLargeLongExtension(4096)
	newLen := len(packet) + 43
	buf := make([]byte, len(packet), newLen)
	copy(buf, packet)
	if err := PacketPad(buf, len(packet), newLen); err != nil {
		t.Fatalf("PacketPad(large extension): %v", err)
	}
	n, err := PacketUnpad(buf[:newLen], newLen)
	if err != nil {
		t.Fatalf("PacketUnpad(large extension): %v", err)
	}
	want := []byte{GenerateTOC(18, false, 0), 0x11, 0x22, 0x33}
	if got := buf[:n]; !bytes.Equal(got, want) {
		t.Fatalf("PacketUnpad(large extension)=%x, want %x", got, want)
	}
}

func TestPacketPadUnpadSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "code 0", data: []byte{0x48, 0x11, 0x22, 0x33}},
		{name: "48-frame VBR", data: makePacket48FrameVBR()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newLen := len(tt.data) + 43
			buf := make([]byte, len(tt.data), newLen)
			pad := func() {
				copy(buf, tt.data)
				if err := PacketPad(buf, len(tt.data), newLen); err != nil {
					panic(err)
				}
			}
			pad()
			if got := testing.AllocsPerRun(100, pad); got != 0 {
				t.Errorf("PacketPad allocs/op=%.2f, want 0", got)
			}

			padded := append([]byte(nil), buf[:newLen]...)
			unpadBuf := make([]byte, newLen)
			unpad := func() {
				copy(unpadBuf, padded)
				if _, err := PacketUnpad(unpadBuf, newLen); err != nil {
					panic(err)
				}
			}
			unpad()
			if got := testing.AllocsPerRun(100, unpad); got != 0 {
				t.Errorf("PacketUnpad allocs/op=%.2f, want 0", got)
			}
		})
	}
}

func makePacket48FrameVBR() []byte {
	return code3VBRPacket(16, false, packet48VBRFrameSizes(), 0x20)
}

func BenchmarkPacketPadCallerCapacity(b *testing.B) {
	benchmarkPacketPad(b, []byte{0x48, 0x11, 0x22, 0x33})
}

func BenchmarkPacketPad48FrameVBR(b *testing.B) {
	benchmarkPacketPad(b, makePacket48FrameVBR())
}

func BenchmarkPacketPadWithExtensions(b *testing.B) {
	benchmarkPacketPad(b, []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe})
}

func BenchmarkPacketPadLargeExtensionPadding(b *testing.B) {
	benchmarkPacketPad(b, packetWithLargeLongExtension(4096))
}

func benchmarkPacketPad(b *testing.B, packet []byte) {
	newLen := len(packet) + 43
	buf := make([]byte, len(packet), newLen)
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for range b.N {
		copy(buf, packet)
		if err := PacketPad(buf, len(packet), newLen); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacketUnpadCallerCapacity(b *testing.B) {
	benchmarkPacketUnpad(b, []byte{0x48, 0x11, 0x22, 0x33})
}

func BenchmarkPacketUnpad48FrameVBR(b *testing.B) {
	benchmarkPacketUnpad(b, makePacket48FrameVBR())
}

func benchmarkPacketUnpad(b *testing.B, packet []byte) {
	newLen := len(packet) + 43
	padded := make([]byte, newLen)
	copy(padded, packet)
	if err := PacketPad(padded, len(packet), newLen); err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, newLen)
	b.ReportAllocs()
	b.SetBytes(int64(newLen))
	b.ResetTimer()
	for range b.N {
		copy(buf, padded)
		if _, err := PacketUnpad(buf, newLen); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParsePacket48FrameVBR(b *testing.B) {
	packet := makePacket48FrameVBR()
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for range b.N {
		if _, err := ParsePacket(packet); err != nil {
			b.Fatal(err)
		}
	}
}

func fuzzSeed(def int64) int64 {
	if v := os.Getenv("GOPUS_FUZZ_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// fuzzIterations returns the per-batch case count, overridable via
// GOPUS_FUZZ_ITERS for longer soak runs.
func fuzzIterations(def int) int {
	if v := os.Getenv("GOPUS_FUZZ_ITERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// buildPacketFuzzSeedBank returns a bank of real, valid Opus packets spanning
// the three coding modes, mono+stereo, several frame sizes, plus multi-frame
// (code 1/2/3) framings assembled via the repacketizer. These are the bases the
// mutator perturbs and the inputs the repacketizer cat-loop concatenates.
func buildPacketFuzzSeedBank(t *testing.T) [][]byte {
	t.Helper()
	var seeds [][]byte
	add := func(p []byte) {
		if len(p) > 0 {
			seeds = append(seeds, append([]byte(nil), p...))
		}
	}

	for _, ch := range []int{1, 2} {
		for _, fs := range []int{120, 240, 480, 960, 1920} {
			add(encodeAPIRateCELTPacketFrameSize(t, ch, fs))
		}
		for _, fs := range []int{480, 960, 1920, 2880} {
			add(encodeAPIRateSILKPacketFrameSize(t, ch, fs))
		}
		for _, fs := range []int{480, 960} {
			add(encodeAPIRateHybridPacketFrameSize(t, ch, fs))
		}
	}

	// Multi-frame framings (code 1/2/3) via the repacketizer so the mutator and
	// the cat-loop also exercise multi-frame headers and CBR/VBR re-framing.
	base := seeds[0:len(seeds):len(seeds)]
	for _, s := range base {
		for _, n := range []int{2, 3, 6} {
			add(repackNCopies(s, n))
		}
	}

	// A few hand-built framings across codes to guarantee coverage of every
	// framing-code re-emit path even if the encoder never produces them.
	for _, cfg := range []uint8{1, 11, 14, 18, 31} {
		add(code0Packet(cfg, false, 40, 1))
		add(code1Packet(cfg, false, 25, 2))
		add(code2Packet(cfg, false, 17, 33, 3))
		add(code3CBRPacket(cfg, false, 3, 14, 4))
		add(code3VBRPacket(cfg, false, []int{11, 22, 9}, 5))
	}
	return seeds
}

// repackNCopies repacketizes n copies of base into a single packet, or nil if
// repacketization is not possible (e.g. would exceed 120ms).
func repackNCopies(base []byte, n int) []byte {
	rp := NewRepacketizer()
	for range n {
		if err := rp.Cat(base); err != nil {
			return nil
		}
	}
	buf := make([]byte, len(base)*n+64)
	written, err := rp.Out(buf)
	if err != nil || written <= 0 {
		return nil
	}
	return append([]byte(nil), buf[:written]...)
}

// mutateFuzzPacket applies one seeded structured mutation to a copy of src,
// producing a (usually) malformed packet. Returns nil to signal "skip".
func mutateFuzzPacket(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(13) {
	case 0: // truncate to a random shorter length (incl. 0)
		return p[:rng.Intn(len(p)+1)]
	case 1: // single random byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite TOC config (mode/bandwidth/frame-size), keep low 3 bits
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // rewrite TOC code bits (frame packing)
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // flip TOC stereo bit
		p[0] ^= 0x04
		return p
	case 6: // force code 3 with a boundary frame-count byte
		p[0] = (p[0] & 0xFC) | 0x03
		boundary := []byte{0x00, 0x01, 0x02, 0x30, 0x31, 0x3F, 0x80, 0x81, 0xC0, 0xC1, 0xFF}
		fc := boundary[rng.Intn(len(boundary))]
		if len(p) < 2 {
			p = append(p, fc)
		} else {
			p[1] = fc
		}
		return p
	case 7: // corrupt a code-2/3 frame-length / padding byte in the header region
		if len(p) >= 2 {
			n := 1 + rng.Intn(min(4, len(p)-1))
			p[n] = byte(rng.Intn(256))
		}
		return p
	case 8: // append junk bytes (oversize / trailing data)
		extra := make([]byte, 1+rng.Intn(8))
		for i := range extra {
			extra[i] = byte(rng.Intn(256))
		}
		return append(p, extra...)
	case 9: // force code 0 with an oversized payload (> maxOpusFrameBytes)
		// Clearing the low two TOC bits selects frame-count code 0.
		head := p[0] & 0xFC
		over := make([]byte, 1+maxOpusFrameBytes+1+rng.Intn(4))
		over[0] = head
		return over
	case 10: // self-delimited round-trip on the (possibly mutated) base
		if sd, err := makeSelfDelimitedPacket(p); err == nil {
			// mutate one header byte of the self-delimited form too
			if rng.Intn(2) == 0 && len(sd) > 1 {
				sd[rng.Intn(len(sd))] = byte(rng.Intn(256))
			}
			return sd
		}
		return p
	case 11: // tiny packet (1-2 random bytes) — TOC-only / minimal boundary
		out := make([]byte, 1+rng.Intn(2))
		for i := range out {
			out[i] = byte(rng.Intn(256))
		}
		return out
	default: // pass the unmodified valid packet through (control)
		return p
	}
}

// fuzzPacketBank picks a packet for a fuzz case: with probability ~1/2 a
// mutated/malformed packet, otherwise an unmodified valid seed.
func fuzzPacketBank(rng *rand.Rand, seeds [][]byte) []byte {
	src := seeds[rng.Intn(len(seeds))]
	if rng.Intn(2) == 0 {
		if m := mutateFuzzPacket(rng, src); m != nil {
			return m
		}
	}
	return append([]byte(nil), src...)
}

// ── packet-utility differential fuzz (GPPI/GPPO oracle) ───────────────────────

// TestPacketUtilFuzzMatchesLibopus fires randomized single packets at the
// packet-parse oracle and asserts opus_packet_get_* + opus_packet_parse parity.
func TestPacketUtilFuzzMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := buildPacketFuzzSeedBank(t)
	rng := rand.New(rand.NewSource(fuzzSeed(0x1e3779b97f4a7c15)))
	iters := fuzzIterations(4000)

	cases := make([]libopusPacketParseCase, 0, iters)
	for i := range iters {
		pkt := fuzzPacketBank(rng, seeds)
		cases = append(cases, libopusPacketParseCase{
			name:   fmt.Sprintf("util_%04d_len%d", i, len(pkt)),
			packet: pkt,
		})
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for i, tc := range cases {
		w := want[i]
		assertPacketUtilParity(t, tc.name, tc.packet, w)
	}
}

// assertPacketUtilParity checks every opus_packet_* utility plus opus_packet_parse
// for one packet against the oracle outputs, recovering panics as hard failures.
func assertPacketUtilParity(t *testing.T, name string, pkt []byte, w libopusPacketParseResult) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panic on packet %s: %v", name, hex.EncodeToString(pkt), r)
		}
	}()

	// nb_frames parity (opus_packet_get_nb_frames)
	gotFrames, framesErr := packetFrameCountLibopus(pkt)
	if w.nbFrames < 0 {
		if framesErr == nil {
			t.Fatalf("%s: nb_frames gopus=%d (nil err) libopus=%d pkt=%s",
				name, gotFrames, w.nbFrames, hex.EncodeToString(pkt))
		}
	} else {
		if framesErr != nil {
			t.Fatalf("%s: nb_frames gopus err=%v libopus=%d pkt=%s",
				name, framesErr, w.nbFrames, hex.EncodeToString(pkt))
		}
		if int32(gotFrames) != w.nbFrames {
			t.Fatalf("%s: nb_frames gopus=%d libopus=%d pkt=%s",
				name, gotFrames, w.nbFrames, hex.EncodeToString(pkt))
		}
	}

	// samples_per_frame parity at 48kHz (opus_packet_get_samples_per_frame)
	// Only meaningful for len>=1; the oracle reports OPUS_BAD_ARG for empty.
	if len(pkt) >= 1 {
		gotSPF, spfErr := packetSamplesPerFrameAtRate(pkt, 48000)
		if spfErr != nil {
			t.Fatalf("%s: samples_per_frame unexpected err=%v pkt=%s", name, spfErr, hex.EncodeToString(pkt))
		}
		if int32(gotSPF) != w.samplesPerFrame {
			t.Fatalf("%s: samples_per_frame gopus=%d libopus=%d pkt=%s",
				name, gotSPF, w.samplesPerFrame, hex.EncodeToString(pkt))
		}

		// bandwidth / channels parity (TOC-derived, always defined for len>=1)
		toc := ParseTOC(pkt[0])
		gotBW := libopusBandwidthCode(toc.Bandwidth)
		if gotBW != w.bandwidth {
			t.Fatalf("%s: bandwidth gopus=%d libopus=%d toc=0x%02x", name, gotBW, w.bandwidth, pkt[0])
		}
		gotCh := int32(1)
		if toc.Stereo {
			gotCh = 2
		}
		if gotCh != w.nbChannels {
			t.Fatalf("%s: channels gopus=%d libopus=%d toc=0x%02x", name, gotCh, w.nbChannels, pkt[0])
		}
	}

	// nb_samples parity at 48kHz (opus_packet_get_nb_samples / opus_decoder_get_nb_samples)
	gotSamples, samplesErr := packetSamplesAtRate(pkt, 48000)
	if w.nbSamples < 0 {
		if samplesErr == nil {
			t.Fatalf("%s: nb_samples gopus=%d (nil err) libopus=%d pkt=%s",
				name, gotSamples, w.nbSamples, hex.EncodeToString(pkt))
		}
	} else {
		if samplesErr != nil {
			t.Fatalf("%s: nb_samples gopus err=%v libopus=%d pkt=%s",
				name, samplesErr, w.nbSamples, hex.EncodeToString(pkt))
		}
		if int32(gotSamples) != w.nbSamples {
			t.Fatalf("%s: nb_samples gopus=%d libopus=%d pkt=%s",
				name, gotSamples, w.nbSamples, hex.EncodeToString(pkt))
		}
	}

	// opus_packet_parse parity: accept/reject, frame count, TOC byte, frame sizes.
	info, parseErr := ParsePacket(pkt)
	libAccepted := w.parseRet > 0
	gopAccepted := parseErr == nil
	if gopAccepted != libAccepted {
		t.Fatalf("%s: parse accept gopus=%v(%v) libopus=%v(ret=%d) pkt=%s",
			name, gopAccepted, parseErr, libAccepted, w.parseRet, hex.EncodeToString(pkt))
	}
	if !libAccepted {
		return
	}
	if int32(info.FrameCount) != w.parseRet {
		t.Fatalf("%s: parse frame count gopus=%d libopus=%d pkt=%s",
			name, info.FrameCount, w.parseRet, hex.EncodeToString(pkt))
	}
	if int32(pkt[0]) != w.parseTOC {
		t.Fatalf("%s: parse toc gopus=0x%02x libopus=0x%02x pkt=%s",
			name, pkt[0], byte(w.parseTOC), hex.EncodeToString(pkt))
	}
	if int32(len(info.FrameSizes)) != w.nFrameSizes {
		t.Fatalf("%s: parse nFrameSizes gopus=%d libopus=%d pkt=%s",
			name, len(info.FrameSizes), w.nFrameSizes, hex.EncodeToString(pkt))
	}
	for j, sz := range info.FrameSizes {
		if int32(sz) != int32(w.frameSizes[j]) {
			t.Fatalf("%s: parse frameSizes[%d] gopus=%d libopus=%d pkt=%s",
				name, j, sz, w.frameSizes[j], hex.EncodeToString(pkt))
		}
	}
}

// ── repacketizer differential fuzz (GRPI/GRPO oracle) ─────────────────────────

// TestRepacketizerFuzzMatchesLibopus fires randomized cat sequences + out_range
// + pad/unpad at the repacketizer oracle and asserts byte-exact parity.
func TestRepacketizerFuzzMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := buildPacketFuzzSeedBank(t)
	rng := rand.New(rand.NewSource(fuzzSeed(0x5eadbeefcafef00d)))
	iters := fuzzIterations(3000)

	cases := make([]repacketizerOracleCase, 0, iters)
	for i := range iters {
		cases = append(cases, randomRepacketizerCase(rng, seeds, i))
	}

	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}

	for i, tc := range cases {
		w := want[i]
		assertRepacketizerCaseParity(t, tc, w)
	}
}

// randomRepacketizerCase builds one randomized repacketizer fuzz case: 1..6
// input packets (mix of valid + malformed), a random begin/end sub-range, a
// random maxlen (sometimes too small), and a random pad target.
func randomRepacketizerCase(rng *rand.Rand, seeds [][]byte, idx int) repacketizerOracleCase {
	nPkts := 1 + rng.Intn(6)
	packets := make([][]byte, nPkts)
	maxFrame := 0
	for j := range packets {
		// Bias toward valid packets so multi-packet merges actually exercise the
		// happy path; still inject malformed inputs ~1/3 of the time.
		var p []byte
		if rng.Intn(3) == 0 {
			p = mutateFuzzPacket(rng, seeds[rng.Intn(len(seeds))])
		}
		if p == nil {
			p = append([]byte(nil), seeds[rng.Intn(len(seeds))]...)
		}
		packets[j] = p
		if len(p) > maxFrame {
			maxFrame = len(p)
		}
	}

	// begin/end span: usually full range (end=0 → resolved after cat), sometimes
	// a random sub-range that may be out of bounds (testing reject parity).
	begin, end := 0, 0
	if rng.Intn(2) == 0 {
		begin = rng.Intn(8)
		end = begin + rng.Intn(8)
	}

	// maxlen: usually generous, occasionally far too small to force buffer-too-small.
	maxlen := 2048
	switch rng.Intn(4) {
	case 0:
		maxlen = rng.Intn(6) // tiny → likely buffer-too-small
	case 1:
		maxlen = maxFrame + rng.Intn(16)
	}

	// pad target: 0 (skip) half the time, else a length >= packet[0] len.
	padNewLen := 0
	if rng.Intn(2) == 0 && len(packets[0]) > 0 {
		padNewLen = len(packets[0]) + rng.Intn(320)
	}

	return repacketizerOracleCase{
		name:      fmt.Sprintf("rp_%04d_n%d_b%d_e%d_max%d_pad%d", idx, nPkts, begin, end, maxlen, padNewLen),
		packets:   packets,
		begin:     begin,
		end:       end,
		maxlen:    maxlen,
		padNewLen: padNewLen,
	}
}

// assertRepacketizerCaseParity replays one repacketizer fuzz case through gopus
// and asserts full parity (cat accept/reject, nb_frames, out_range bytes, pad/
// unpad bytes) vs the oracle, recovering panics as hard failures.
func assertRepacketizerCaseParity(t *testing.T, tc repacketizerOracleCase, w repacketizerOracleResult) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panic: %v\n%s", tc.name, r, dumpRepacketizerCase(tc))
		}
	}()

	got := runRepacketizerGopus(tc)

	// cat accept/reject parity
	libCatOK := w.catRet == 0
	gopCatOK := got.catRet == 0
	if libCatOK != gopCatOK {
		t.Fatalf("%s: cat parity gopus(ok=%v ret=%d) libopus(ok=%v ret=%d)\n%s",
			tc.name, gopCatOK, got.catRet, libCatOK, w.catRet, dumpRepacketizerCase(tc))
	}
	if !libCatOK {
		return
	}
	if got.nbFrames != w.nbFrames {
		t.Fatalf("%s: nb_frames gopus=%d libopus=%d\n%s",
			tc.name, got.nbFrames, w.nbFrames, dumpRepacketizerCase(tc))
	}

	// out_range accept/reject + byte-exact
	libOutOK := w.outRet > 0
	gopOutOK := got.outRet > 0
	if libOutOK != gopOutOK {
		t.Fatalf("%s: out_range parity gopus=%d libopus=%d\n%s",
			tc.name, got.outRet, w.outRet, dumpRepacketizerCase(tc))
	}
	if libOutOK {
		if got.outRet != w.outRet {
			t.Fatalf("%s: out_range len gopus=%d libopus=%d\n%s",
				tc.name, got.outRet, w.outRet, dumpRepacketizerCase(tc))
		}
		if hex.EncodeToString(got.outBytes) != hex.EncodeToString(w.outBytes) {
			t.Fatalf("%s: out_range bytes\n got=%s\nwant=%s\n%s",
				tc.name, hex.EncodeToString(got.outBytes), hex.EncodeToString(w.outBytes), dumpRepacketizerCase(tc))
		}
	}

	// pad / unpad parity
	if w.padRet == repacketizerSkipped {
		return
	}
	libPadOK := w.padRet == 0
	gopPadOK := got.padRet == 0
	if libPadOK != gopPadOK {
		t.Fatalf("%s: pad parity gopus=%d libopus=%d\n%s",
			tc.name, got.padRet, w.padRet, dumpRepacketizerCase(tc))
	}
	if !libPadOK {
		return
	}
	if hex.EncodeToString(got.padBytes) != hex.EncodeToString(w.padBytes) {
		t.Fatalf("%s: pad bytes\n got=%s\nwant=%s\n%s",
			tc.name, hex.EncodeToString(got.padBytes), hex.EncodeToString(w.padBytes), dumpRepacketizerCase(tc))
	}
	libUnpadOK := w.unpadRet > 0
	gopUnpadOK := got.unpadRet > 0
	if libUnpadOK != gopUnpadOK {
		t.Fatalf("%s: unpad parity gopus=%d libopus=%d\n%s",
			tc.name, got.unpadRet, w.unpadRet, dumpRepacketizerCase(tc))
	}
	if libUnpadOK {
		if got.unpadRet != w.unpadRet {
			t.Fatalf("%s: unpad len gopus=%d libopus=%d\n%s",
				tc.name, got.unpadRet, w.unpadRet, dumpRepacketizerCase(tc))
		}
		if hex.EncodeToString(got.unpadBytes) != hex.EncodeToString(w.unpadBytes) {
			t.Fatalf("%s: unpad bytes\n got=%s\nwant=%s\n%s",
				tc.name, hex.EncodeToString(got.unpadBytes), hex.EncodeToString(w.unpadBytes), dumpRepacketizerCase(tc))
		}
	}
}

// dumpRepacketizerCase renders a failing case as reproducible hex for triage.
func dumpRepacketizerCase(tc repacketizerOracleCase) string {
	var out strings.Builder
	fmt.Fprintf(&out, "  begin=%d end=%d maxlen=%d padNewLen=%d packets=%d\n",
		tc.begin, tc.end, tc.maxlen, tc.padNewLen, len(tc.packets))
	for i, p := range tc.packets {
		fmt.Fprintf(&out, "  pkt[%d]=%s\n", i, hex.EncodeToString(p))
	}
	return out.String()
}

type repacketizerOracleCase struct {
	name      string
	packets   [][]byte
	begin     int
	end       int // 0 == all frames
	maxlen    int
	padNewLen int // 0 == skip pad/unpad
}

type repacketizerOracleResult struct {
	catRet     int32
	nbFrames   int32
	outRet     int32
	outBytes   []byte
	padRet     int32
	padBytes   []byte
	unpadRet   int32
	unpadBytes []byte
}

const repacketizerSkipped = 0x7fffffff

var repacketizerParityHelper libopustest.HelperCache

func getRepacketizerHelperPath() (string, error) {
	return repacketizerParityHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "repacketizer",
		OutputBase:  "gopus_libopus_repacketizer",
		SourceFile:  "libopus_repacketizer_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"src", "celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusRepacketizer(cases []repacketizerOracleCase) ([]repacketizerOracleResult, error) {
	binPath, err := getRepacketizerHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayload("GRPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packets)))
		for _, p := range tc.packets {
			payload.U32(uint32(len(p)))
			payload.Raw(p)
		}
		payload.U32(uint32(tc.begin))
		payload.U32(uint32(tc.end))
		payload.U32(uint32(tc.maxlen))
		payload.U32(uint32(tc.padNewLen))
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "repacketizer", "GRPO")
	if err != nil {
		return nil, err
	}
	reader.Count(len(cases))
	if reader.Err() != nil {
		return nil, reader.Err()
	}

	out := make([]repacketizerOracleResult, len(cases))
	for i := range out {
		r := &out[i]
		r.catRet = reader.I32()
		r.nbFrames = reader.I32()
		r.outRet = reader.I32()
		outLen := reader.I32()
		if outLen > 0 {
			r.outBytes = append([]byte(nil), reader.Bytes(int(outLen))...)
		}
		r.padRet = reader.I32()
		padLen := reader.I32()
		if padLen > 0 {
			r.padBytes = append([]byte(nil), reader.Bytes(int(padLen))...)
		}
		r.unpadRet = reader.I32()
		unpadLen := reader.I32()
		if unpadLen > 0 {
			r.unpadBytes = append([]byte(nil), reader.Bytes(int(unpadLen))...)
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

// runRepacketizerGopus replays a case through the gopus repacketizer + pad/unpad
// and returns results in the same shape as the oracle output.
func runRepacketizerGopus(tc repacketizerOracleCase) repacketizerOracleResult {
	var res repacketizerOracleResult
	rp := NewRepacketizer()
	catOK := true
	for _, p := range tc.packets {
		if err := rp.Cat(p); err != nil {
			res.catRet = -4 // any negative; oracle uses OPUS_INVALID_PACKET (-4)
			catOK = false
			break
		}
	}
	if !catOK {
		res.outRet = res.catRet
		return res
	}
	res.catRet = 0
	res.nbFrames = int32(rp.NumFrames())

	end := tc.end
	if end == 0 {
		end = rp.NumFrames()
	}
	buf := make([]byte, tc.maxlen)
	n, err := rp.OutRange(tc.begin, end, buf)
	if err != nil {
		res.outRet = -1 // negative sentinel
	} else {
		res.outRet = int32(n)
		res.outBytes = append([]byte(nil), buf[:n]...)
	}

	if tc.padNewLen > 0 && len(tc.packets) > 0 && len(tc.packets[0]) > 0 {
		src := tc.packets[0]
		padBuf := make([]byte, tc.padNewLen)
		copy(padBuf, src)
		if err := PacketPad(padBuf, len(src), tc.padNewLen); err != nil {
			res.padRet = -1
		} else {
			res.padRet = 0
			res.padBytes = append([]byte(nil), padBuf...)
			unpadBuf := make([]byte, tc.padNewLen)
			copy(unpadBuf, padBuf)
			ul, uerr := PacketUnpad(unpadBuf, tc.padNewLen)
			if uerr != nil {
				res.unpadRet = -1
			} else {
				res.unpadRet = int32(ul)
				res.unpadBytes = append([]byte(nil), unpadBuf[:ul]...)
			}
		}
	} else {
		res.padRet = repacketizerSkipped
		res.unpadRet = repacketizerSkipped
	}
	return res
}

func TestRepacketizerByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := repacketizerOracleCases()
	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}

	for i, tc := range cases {
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			got := runRepacketizerGopus(tc)

			// cat accept/reject parity
			libCatOK := w.catRet == 0
			gopCatOK := got.catRet == 0
			if libCatOK != gopCatOK {
				t.Fatalf("cat parity: gopus ok=%v (ret=%d) libopus ok=%v (ret=%d)",
					gopCatOK, got.catRet, libCatOK, w.catRet)
			}
			if !libCatOK {
				return
			}

			if got.nbFrames != w.nbFrames {
				t.Errorf("nb_frames: got %d want %d", got.nbFrames, w.nbFrames)
			}

			// out_range accept/reject parity (positive vs negative)
			libOutOK := w.outRet > 0
			gopOutOK := got.outRet > 0
			if libOutOK != gopOutOK {
				t.Fatalf("out_range parity: gopus ret=%d libopus ret=%d (bufmismatch?)", got.outRet, w.outRet)
			}
			if libOutOK {
				if got.outRet != w.outRet {
					t.Errorf("out_range length: got %d want %d", got.outRet, w.outRet)
				}
				if hex.EncodeToString(got.outBytes) != hex.EncodeToString(w.outBytes) {
					t.Errorf("out_range bytes:\n got=%s\nwant=%s",
						hex.EncodeToString(got.outBytes), hex.EncodeToString(w.outBytes))
				}
			}

			// pad / unpad parity
			if w.padRet != repacketizerSkipped {
				libPadOK := w.padRet == 0
				gopPadOK := got.padRet == 0
				if libPadOK != gopPadOK {
					t.Fatalf("pad parity: gopus ret=%d libopus ret=%d", got.padRet, w.padRet)
				}
				if libPadOK {
					if hex.EncodeToString(got.padBytes) != hex.EncodeToString(w.padBytes) {
						t.Errorf("pad bytes:\n got=%s\nwant=%s",
							hex.EncodeToString(got.padBytes), hex.EncodeToString(w.padBytes))
					}
					libUnpadOK := w.unpadRet > 0
					gopUnpadOK := got.unpadRet > 0
					if libUnpadOK != gopUnpadOK {
						t.Fatalf("unpad parity: gopus ret=%d libopus ret=%d", got.unpadRet, w.unpadRet)
					}
					if libUnpadOK {
						if got.unpadRet != w.unpadRet {
							t.Errorf("unpad length: got %d want %d", got.unpadRet, w.unpadRet)
						}
						if hex.EncodeToString(got.unpadBytes) != hex.EncodeToString(w.unpadBytes) {
							t.Errorf("unpad bytes:\n got=%s\nwant=%s",
								hex.EncodeToString(got.unpadBytes), hex.EncodeToString(w.unpadBytes))
						}
					}
				}
			}
		})
	}
}

func TestPacketUnpadMalformedExtensionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := mustDecodeHex(t, "4b4102112233ffff")
	tc := repacketizerOracleCase{
		name:      "unpad_opaque_malformed_extension",
		packets:   [][]byte{packet},
		begin:     0,
		end:       1,
		maxlen:    64,
		padNewLen: len(packet), // PacketPad is a no-op, so the oracle unpads the input directly.
	}
	want, err := probeLibopusRepacketizer([]repacketizerOracleCase{tc})
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}
	got := runRepacketizerGopus(tc)
	if want[0].unpadRet != int32(len([]byte{0x48, 0x11, 0x22, 0x33})) {
		t.Fatalf("libopus PacketUnpad ret=%d, want 4", want[0].unpadRet)
	}
	if got.unpadRet != want[0].unpadRet || !bytes.Equal(got.unpadBytes, want[0].unpadBytes) {
		t.Fatalf("PacketUnpad=%x (ret=%d), libopus=%x (ret=%d)", got.unpadBytes, got.unpadRet, want[0].unpadBytes, want[0].unpadRet)
	}
}

func TestMalformedPacketExtensionErrorsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	malformed := mustDecodeHex(t, "4b4102112233ffff")
	valid := []byte{0x48, 0x44}
	cases := []repacketizerOracleCase{
		{
			name:      "extension_out_range_adequate_buffer",
			packets:   [][]byte{malformed},
			begin:     0,
			end:       1,
			maxlen:    64,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_short_buffer",
			packets:   [][]byte{malformed},
			begin:     0,
			end:       1,
			maxlen:    1,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_invalid_range",
			packets:   [][]byte{malformed},
			begin:     1,
			end:       1,
			maxlen:    64,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_skips_malformed_frame",
			packets:   [][]byte{malformed, valid},
			begin:     1,
			end:       2,
			maxlen:    64,
			padNewLen: 16,
		},
	}
	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}
	for i, tc := range cases {
		if want[i].catRet != 0 {
			t.Fatalf("libopus %s cat ret=%d, want 0", tc.name, want[i].catRet)
		}
		if want[i].padRet != -3 {
			t.Errorf("libopus %s PacketPad ret=%d, want OPUS_INTERNAL_ERROR (-3)", tc.name, want[i].padRet)
		}
	}
	if want[0].outRet != -3 || want[1].outRet != -3 {
		t.Fatalf("libopus OutRange malformed extension: adequate=%d short=%d, want -3 for both", want[0].outRet, want[1].outRet)
	}
	if want[2].outRet != -1 {
		t.Fatalf("libopus OutRange invalid range ret=%d, want OPUS_BAD_ARG (-1)", want[2].outRet)
	}
	if want[3].outRet != int32(len(valid)) || !bytes.Equal(want[3].outBytes, valid) {
		t.Fatalf("libopus OutRange after malformed frame: ret=%d bytes=%x, want %x", want[3].outRet, want[3].outBytes, valid)
	}

	if _, err := parsePacketExtensionList([]byte{0xff, 0xff}, 1); err != ErrInvalidPacket {
		t.Fatalf("parsePacketExtensionList malformed data err=%v, want ErrInvalidPacket", err)
	}

	rp := NewRepacketizer()
	if err := rp.Cat(malformed); err != nil {
		t.Fatalf("Cat malformed-extension packet: %v", err)
	}
	if got := rp.NumFrames(); got != 1 {
		t.Fatalf("NumFrames after Cat=%d, want 1", got)
	}

	for _, tc := range []struct {
		name      string
		begin     int
		end       int
		outLen    int
		wantError error
	}{
		{name: "adequate output", begin: 0, end: 1, outLen: 64, wantError: ErrInternalError},
		{name: "short output", begin: 0, end: 1, outLen: 1, wantError: ErrInternalError},
		{name: "invalid range", begin: 1, end: 1, outLen: 64, wantError: ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := bytes.Repeat([]byte{0xa5}, tc.outLen)
			before := append([]byte(nil), out...)
			n, err := rp.OutRange(tc.begin, tc.end, out)
			if n != 0 || err != tc.wantError {
				t.Fatalf("OutRange(%d,%d)=(%d,%v), want (0,%v)", tc.begin, tc.end, n, err, tc.wantError)
			}
			if !bytes.Equal(out, before) {
				t.Fatalf("OutRange(%d,%d) changed output on error: got %x want %x", tc.begin, tc.end, out, before)
			}
			if got := rp.NumFrames(); got != 1 {
				t.Fatalf("OutRange(%d,%d) changed NumFrames to %d, want 1", tc.begin, tc.end, got)
			}
		})
	}

	pad := bytes.Repeat([]byte{0xa5}, 16)
	copy(pad, malformed)
	padBefore := append([]byte(nil), pad...)
	if err := PacketPad(pad, len(malformed), len(pad)); err != ErrInternalError {
		t.Fatalf("PacketPad malformed extension err=%v, want ErrInternalError", err)
	}
	if !bytes.Equal(pad, padBefore) {
		t.Fatalf("PacketPad changed output on error: got %x want %x", pad, padBefore)
	}

	if err := rp.Cat(valid); err != nil {
		t.Fatalf("Cat valid packet after extension errors: %v", err)
	}
	out := make([]byte, 64)
	n, err := rp.OutRange(1, 2, out)
	if err != nil {
		t.Fatalf("OutRange valid frame after malformed frame: %v", err)
	}
	if !bytes.Equal(out[:n], want[3].outBytes) {
		t.Fatalf("OutRange valid continuation=%x, want libopus %x", out[:n], want[3].outBytes)
	}
}

// ── case builders ────────────────────────────────────────────────────────────

// mkPayload returns n deterministic non-zero bytes.
func mkPayload(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + seed*31 + 1) & 0xFF)
	}
	return b
}

// code0Packet builds a single-frame (code 0) packet of the given config/stereo.
func code0Packet(config uint8, stereo bool, payloadLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 0)}
	return append(p, mkPayload(payloadLen, seed)...)
}

// code1Packet builds two equal frames (code 1).
func code1Packet(config uint8, stereo bool, frameLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 1)}
	p = append(p, mkPayload(frameLen, seed)...)
	p = append(p, mkPayload(frameLen, seed+1)...)
	return p
}

// code2Packet builds two unequal frames (code 2).
func code2Packet(config uint8, stereo bool, len0, len1, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 2)}
	szBuf := make([]byte, 2)
	n := encodeFrameLength(szBuf, len0)
	p = append(p, szBuf[:n]...)
	p = append(p, mkPayload(len0, seed)...)
	p = append(p, mkPayload(len1, seed+1)...)
	return p
}

// code3CBRPacket builds a code-3 CBR packet with m equal frames of frameLen.
func code3CBRPacket(config uint8, stereo bool, m, frameLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 3), byte(m & 0x3F)}
	for i := range m {
		p = append(p, mkPayload(frameLen, seed+i)...)
	}
	return p
}

// code3VBRPacket builds a code-3 VBR packet with frame sizes given.
func code3VBRPacket(config uint8, stereo bool, sizes []int, seed int) []byte {
	m := len(sizes)
	p := []byte{GenerateTOC(config, stereo, 3), byte(0x80 | (m & 0x3F))}
	for i := 0; i < m-1; i++ {
		szBuf := make([]byte, 2)
		n := encodeFrameLength(szBuf, sizes[i])
		p = append(p, szBuf[:n]...)
	}
	for i := range m {
		p = append(p, mkPayload(sizes[i], seed+i)...)
	}
	return p
}

func repacketizerOraclecasesAppendSingle(cases []repacketizerOracleCase) []repacketizerOracleCase {
	// Single-packet round-trips across all framing codes and several configs.
	configs := []uint8{0, 1, 8, 11, 12, 14, 16, 18, 20, 28, 30, 31}
	for _, cfg := range configs {
		for _, stereo := range []bool{false, true} {
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases,
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code0_cfg%d_%s", cfg, s),
					packets:   [][]byte{code0Packet(cfg, stereo, 40, 1)},
					maxlen:    512,
					padNewLen: 60,
				},
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code1_cfg%d_%s", cfg, s),
					packets:   [][]byte{code1Packet(cfg, stereo, 20, 2)},
					maxlen:    512,
					padNewLen: 80,
				},
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code2_cfg%d_%s", cfg, s),
					packets:   [][]byte{code2Packet(cfg, stereo, 15, 33, 3)},
					maxlen:    512,
					padNewLen: 100,
				},
			)
		}
	}
	return cases
}

func repacketizerOracleCases() []repacketizerOracleCase {
	var cases []repacketizerOracleCase
	cases = repacketizerOraclecasesAppendSingle(cases)
	largeExtensionPacket := packetWithLargeLongExtension(4096)

	// CELT 10ms config (18) supports up to 12 frames in 120ms (480 samples each).
	// SILK 20ms config (1) supports up to 6 frames. Use small frame sizes.

	// Merge two code-0 packets -> code 1 (equal) or code 2 (unequal).
	cases = append(cases,
		repacketizerOracleCase{
			name:    "merge_two_code0_equal",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 30, 2)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "merge_two_code0_unequal",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 41, 2)},
			maxlen:  512, padNewLen: 0,
		},
		// large frame (>=252) forces 2-byte size encoding in code 2 / VBR
		repacketizerOracleCase{
			name:    "merge_two_code0_largefirst",
			packets: [][]byte{code0Packet(18, false, 300, 1), code0Packet(18, false, 41, 2)},
			maxlen:  1024, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "merge_two_code0_stereo",
			packets: [][]byte{code0Packet(31, true, 50, 1), code0Packet(31, true, 60, 2)},
			maxlen:  512, padNewLen: 0,
		},
	)

	// Merge three code-0 -> code 3.
	cases = append(cases,
		repacketizerOracleCase{
			name: "merge_three_code0_equal",
			packets: [][]byte{
				code0Packet(18, false, 25, 1),
				code0Packet(18, false, 25, 2),
				code0Packet(18, false, 25, 3),
			},
			maxlen: 512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name: "merge_three_code0_vbr",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			maxlen: 512, padNewLen: 0,
		},
	)

	// OutRange sub-range over a 3-frame accumulation.
	cases = append(cases,
		repacketizerOracleCase{
			name: "outrange_1_3_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 1, end: 3, maxlen: 512,
		},
		repacketizerOracleCase{
			name: "outrange_0_1_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 0, end: 1, maxlen: 512,
		},
		repacketizerOracleCase{
			name: "outrange_1_2_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 1, end: 2, maxlen: 512,
		},
	)

	// Feed multi-frame packets directly (code1/2/3) then re-emit.
	cases = append(cases,
		repacketizerOracleCase{
			name:    "in_code1_passthrough",
			packets: [][]byte{code1Packet(18, false, 22, 5)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "in_code3cbr_passthrough",
			packets: [][]byte{code3CBRPacket(18, false, 4, 15, 6)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "in_code3vbr_passthrough",
			packets: [][]byte{code3VBRPacket(18, false, []int{10, 20, 30}, 7)},
			maxlen:  512, padNewLen: 0,
		},
		// merge a code1 + code0 (3 total frames -> code 3)
		repacketizerOracleCase{
			name:    "merge_code1_plus_code0",
			packets: [][]byte{code1Packet(18, false, 22, 5), code0Packet(18, false, 22, 8)},
			maxlen:  512, padNewLen: 0,
		},
	)

	// pad / unpad focused cases on various single packets.
	cases = append(cases,
		repacketizerOracleCase{
			name:      "pad_code0_small",
			packets:   [][]byte{code0Packet(18, false, 10, 1)},
			maxlen:    512,
			padNewLen: 11, // +1 byte -> minimal code 3 padding
		},
		repacketizerOracleCase{
			name:      "pad_code0_large",
			packets:   [][]byte{code0Packet(18, false, 10, 1)},
			maxlen:    512,
			padNewLen: 300, // forces chained 255 padding bytes
		},
		repacketizerOracleCase{
			name:      "pad_code1",
			packets:   [][]byte{code1Packet(18, false, 12, 2)},
			maxlen:    512,
			padNewLen: 200,
		},
		repacketizerOracleCase{
			name:      "pad_code3cbr",
			packets:   [][]byte{code3CBRPacket(18, false, 3, 12, 4)},
			maxlen:    512,
			padNewLen: 150,
		},
		repacketizerOracleCase{
			name:      "pad_noncanonical_two_frame_vbr",
			packets:   [][]byte{code3VBRPacket(18, false, []int{12, 12}, 5)},
			maxlen:    512,
			padNewLen: len(code3VBRPacket(18, false, []int{12, 12}, 5)) + 29,
		},
		repacketizerOracleCase{
			name:      "pad_48_frame_vbr",
			packets:   [][]byte{code3VBRPacket(16, false, packet48VBRFrameSizes(), 6)},
			maxlen:    512,
			padNewLen: len(code3VBRPacket(16, false, packet48VBRFrameSizes(), 6)) + 43,
		},
		repacketizerOracleCase{
			name:      "pad_with_packet_extension",
			packets:   [][]byte{{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}},
			maxlen:    512,
			padNewLen: 16,
		},
		repacketizerOracleCase{
			name:      "pad_with_large_packet_extension",
			packets:   [][]byte{largeExtensionPacket},
			maxlen:    len(largeExtensionPacket) + 128,
			padNewLen: len(largeExtensionPacket) + 43,
		},
	)

	// buffer-too-small: maxlen smaller than required output.
	cases = append(cases,
		repacketizerOracleCase{
			name:    "buftoosmall_merge",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 30, 2)},
			maxlen:  3, // way too small
		},
	)

	// over-duration / TOC-mismatch rejection on cat.
	cases = append(cases,
		repacketizerOracleCase{
			name: "toc_mismatch",
			packets: [][]byte{
				code0Packet(18, false, 10, 1),
				code0Packet(20, false, 10, 2), // different config -> toc top bits differ
			},
			maxlen: 512,
		},
		repacketizerOracleCase{
			name: "over_120ms",
			packets: [][]byte{
				// config 31 = 960 samples/frame; 2 frames = 40ms; cat 4 of them = 80ms ok,
				// but cat enough to exceed 120ms. 6 frames*960 = 5760 (=120ms) ok,
				// the 7th makes it exceed.
				code3CBRPacket(31, false, 6, 8, 1),
				code0Packet(31, false, 8, 2),
			},
			maxlen: 2048,
		},
	)

	return cases
}

func packet48VBRFrameSizes() []int {
	sizes := make([]int, maxRepacketizerFrames)
	for i := range sizes {
		sizes[i] = 1
	}
	sizes[len(sizes)-1] = 2
	return sizes
}

func packetWithLargeLongExtension(payloadLen int) []byte {
	extensionLen := payloadLen + 1 // one byte for the long-extension ID
	packet := []byte{GenerateTOC(18, false, 3), 0x41}
	remainingPadding := extensionLen
	for remainingPadding > 254 {
		packet = append(packet, 255)
		remainingPadding -= 254
	}
	packet = append(packet, byte(remainingPadding))
	extension := make([]byte, extensionLen)
	extension[0] = 0x50
	for i := 0; i < payloadLen; i++ {
		extension[i+1] = byte(i*13 + 7)
	}
	packet = append(packet, 0x11, 0x22, 0x33)
	return append(packet, extension...)
}

func TestRepacketizerResetClearsOwnedPacketReferences(t *testing.T) {
	rp := NewRepacketizer()
	packetWithPadding := []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}
	if err := rp.Cat(packetWithPadding); err != nil {
		t.Fatalf("Cat(packet with padding): %v", err)
	}
	if rp.NumFrames() != 1 || rp.frames[0].len == 0 {
		t.Fatalf("Cat retained frames=%d, want one owned frame", rp.NumFrames())
	}
	if rp.paddings[0].len == 0 || !rp.hasPad {
		t.Fatalf("Cat retained paddings=%d, want one non-empty owned padding block", rp.paddings[0].len)
	}

	rp.Reset()
	if rp.NumFrames() != 0 {
		t.Fatalf("NumFrames after Reset=%d, want 0", rp.NumFrames())
	}
	if len(rp.frameBuf) != 0 {
		t.Fatalf("Reset retained frameBuf len=%d, want 0", len(rp.frameBuf))
	}
	if len(rp.padBuf) != 0 {
		t.Fatalf("Reset retained padBuf len=%d, want 0", len(rp.padBuf))
	}
	if rp.hasPad {
		t.Fatalf("Reset retained hasPad=true, want false")
	}

	packetAfterReset := []byte{0x48, 0x44, 0x55, 0x66}
	if err := rp.Cat(packetAfterReset); err != nil {
		t.Fatalf("Cat after Reset: %v", err)
	}
	out := make([]byte, len(packetAfterReset))
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("Out after Reset and Cat: %v", err)
	}
	if got := out[:n]; !bytes.Equal(got, packetAfterReset) {
		t.Fatalf("Out after Reset=%x, want %x", got, packetAfterReset)
	}
}

func TestRepacketizerCatSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name       string
		packet     []byte
		wantAllocs float64
	}{
		{name: "empty frame", packet: []byte{0x48}, wantAllocs: 0},
		{name: "owned frame", packet: []byte{0x48, 0x44, 0x55, 0x66}, wantAllocs: 0},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}, wantAllocs: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet)+64)
			run := func() {
				if err := rp.Cat(tt.packet); err != nil {
					panic(err)
				}
				if _, err := rp.Out(out); err != nil {
					panic(err)
				}
				rp.Reset()
			}
			run()
			if got := testing.AllocsPerRun(100, run); got != tt.wantAllocs {
				t.Fatalf("Cat/Out/Reset allocs/op=%.2f, want %.0f", got, tt.wantAllocs)
			}
		})
	}
}

func TestPacketPadSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name       string
		packet     []byte
		newLen     int
		wantAllocs float64
	}{
		{name: "plain", packet: []byte{0x48, 0x11, 0x22, 0x33}, newLen: 16, wantAllocs: 0},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}, newLen: 20, wantAllocs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, tt.newLen)
			run := func() {
				copy(buf, tt.packet)
				if err := PacketPad(buf, len(tt.packet), tt.newLen); err != nil {
					panic(err)
				}
				if _, err := PacketUnpad(buf, tt.newLen); err != nil {
					panic(err)
				}
			}
			run()
			if got := testing.AllocsPerRun(100, run); got != tt.wantAllocs {
				t.Fatalf("PacketPad/PacketUnpad allocs/op=%.2f, want %.0f", got, tt.wantAllocs)
			}
		})
	}
}

func BenchmarkRepacketizerResetCatOut(b *testing.B) {
	for _, tt := range []struct {
		name   string
		packet []byte
	}{
		{name: "code0", packet: []byte{0x48, 0x44, 0x55, 0x66}},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}},
	} {
		b.Run(tt.name, func(b *testing.B) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet)+64)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := rp.Cat(tt.packet); err != nil {
					b.Fatal(err)
				}
				if _, err := rp.Out(out); err != nil {
					b.Fatal(err)
				}
				rp.Reset()
			}
		})
	}
}

type repacketizerLibopusFixture struct {
	PacketA   string `json:"packet_a"`
	PacketB   string `json:"packet_b"`
	PacketC   string `json:"packet_c"`
	ABOut     string `json:"ab_out"`
	ACOut     string `json:"ac_out"`
	ABCOut    string `json:"abc_out"`
	ABC13     string `json:"abc_1_3"`
	PadNewLen int    `json:"pad_new_len"`
	Pad12     string `json:"pad_12"`
	UnpadLen  int    `json:"unpad_len"`
	Unpad     string `json:"unpad"`
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q error: %v", s, err)
	}
	return b
}

func loadRepacketizerFixture(t *testing.T) repacketizerLibopusFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/repacketizer_libopus_fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f repacketizerLibopusFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return f
}

func TestRepacketizerParityWithLibopusFixture(t *testing.T) {
	f := loadRepacketizerFixture(t)

	a := mustDecodeHex(t, f.PacketA)
	b := mustDecodeHex(t, f.PacketB)
	c := mustDecodeHex(t, f.PacketC)

	rp := NewRepacketizer()
	out := make([]byte, 256)

	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(b); err != nil {
		t.Fatalf("cat(b): %v", err)
	}
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("out(ab): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABOut; got != want {
		t.Fatalf("out(ab)=%s want=%s", got, want)
	}

	rp.Reset()
	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(c); err != nil {
		t.Fatalf("cat(c): %v", err)
	}
	n, err = rp.Out(out)
	if err != nil {
		t.Fatalf("out(ac): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ACOut; got != want {
		t.Fatalf("out(ac)=%s want=%s", got, want)
	}

	rp.Reset()
	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(b); err != nil {
		t.Fatalf("cat(b): %v", err)
	}
	if err := rp.Cat(c); err != nil {
		t.Fatalf("cat(c): %v", err)
	}
	n, err = rp.Out(out)
	if err != nil {
		t.Fatalf("out(abc): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABCOut; got != want {
		t.Fatalf("out(abc)=%s want=%s", got, want)
	}
	n, err = rp.OutRange(1, 3, out)
	if err != nil {
		t.Fatalf("out_range(1,3): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABC13; got != want {
		t.Fatalf("out_range(1,3)=%s want=%s", got, want)
	}

	buf := make([]byte, f.PadNewLen)
	copy(buf, a)
	if err := PacketPad(buf, len(a), f.PadNewLen); err != nil {
		t.Fatalf("packet_pad: %v", err)
	}
	if got, want := hex.EncodeToString(buf[:f.PadNewLen]), f.Pad12; got != want {
		t.Fatalf("packet_pad=%s want=%s", got, want)
	}
	unpaddedLen, err := PacketUnpad(buf, f.PadNewLen)
	if err != nil {
		t.Fatalf("packet_unpad: %v", err)
	}
	if got, want := unpaddedLen, f.UnpadLen; got != want {
		t.Fatalf("packet_unpad len=%d want=%d", got, want)
	}
	if got, want := hex.EncodeToString(buf[:unpaddedLen]), f.Unpad; got != want {
		t.Fatalf("packet_unpad=%s want=%s", got, want)
	}
}

func TestRepacketizerRejectsTOCMismatch(t *testing.T) {
	rp := NewRepacketizer()
	p1 := []byte{0x48, 0x11, 0x22, 0x33}
	p2 := []byte{0x78, 0x44, 0x55, 0x66}

	if err := rp.Cat(p1); err != nil {
		t.Fatalf("cat(p1): %v", err)
	}
	if err := rp.Cat(p2); err != ErrInvalidPacket {
		t.Fatalf("cat(p2)=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestRepacketizerRejectsDurationOver120ms(t *testing.T) {
	rp := NewRepacketizer()

	packet120ms := append([]byte{GenerateTOC(16, false, 3), 0x30}, make([]byte, 48)...)
	if err := rp.Cat(packet120ms); err != nil {
		t.Fatalf("cat(120ms packet): %v", err)
	}

	oneMore := []byte{GenerateTOC(16, false, 0), 0x7f}
	if err := rp.Cat(oneMore); err != ErrInvalidPacket {
		t.Fatalf("cat(extra frame)=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestRepacketizerRejectsLibopusParserEnvelopeViolations(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
	}{
		{
			name:   "implicit_frame_too_large",
			packet: append([]byte{0x00}, make([]byte, maxOpusFrameBytes+1)...),
		},
		{
			name:   "code3_duration_over_120ms",
			packet: []byte{GenerateTOC(31, false, 3), 0x07},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := NewRepacketizer()
			if err := rp.Cat(tt.packet); err != ErrInvalidPacket {
				t.Fatalf("Cat error=%v want %v", err, ErrInvalidPacket)
			}
		})
	}
}

func TestRepacketizerPreservesPacketExtensions(t *testing.T) {
	packetAExt := mustDecodeHex(t, "4b41061122330baa50deadbe")
	packetB := mustDecodeHex(t, "48445566")
	wantAB := "4b42061122334455660baa50deadbe"

	rp := NewRepacketizer()
	if err := rp.Cat(packetAExt); err != nil {
		t.Fatalf("cat(packetAExt): %v", err)
	}
	clear(packetAExt)
	if err := rp.Cat(packetB); err != nil {
		t.Fatalf("cat(packetB): %v", err)
	}

	out := make([]byte, 64)
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("out(ab with extensions): %v", err)
	}
	if got := hex.EncodeToString(out[:n]); got != wantAB {
		t.Fatalf("out(ab with extensions)=%s want=%s", got, wantAB)
	}
}

func TestRepacketizerExtensionOutRangeShortBuffers(t *testing.T) {
	const packetHex = "4b41061122330baa50deadbe"
	packet := mustDecodeHex(t, packetHex)
	rp := NewRepacketizer()
	if err := rp.Cat(packet); err != nil {
		t.Fatalf("cat(packet with extensions): %v", err)
	}

	// This one-frame code-3 output needs a two-byte header and three frame
	// bytes before it can carry the retained extension padding.
	for size := 0; size < len(packet); size++ {
		out := make([]byte, size)
		n, err := rp.OutRange(0, 1, out)
		if n != 0 || err != ErrBufferTooSmall {
			t.Fatalf("OutRange buffer length %d: n=%d err=%v, want ErrBufferTooSmall", size, n, err)
		}
		if got := rp.NumFrames(); got != 1 {
			t.Fatalf("OutRange buffer length %d changed repacketizer frame count to %d", size, got)
		}
	}

	out := make([]byte, len(packet))
	n, err := rp.OutRange(0, 1, out)
	if err != nil {
		t.Fatalf("OutRange after short buffers: %v", err)
	}
	if n != len(packet) || hex.EncodeToString(out[:n]) != packetHex {
		t.Fatalf("OutRange after short buffers=%s (n=%d), want %s", hex.EncodeToString(out[:n]), n, packetHex)
	}
}

func TestPacketPadPreservesPacketExtensions(t *testing.T) {
	packetAExt := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantPadded := "4b410a112233010101010baa50deadbe"
	wantUnpadded := "48112233"

	buf := make([]byte, 16)
	copy(buf, packetAExt)
	if err := PacketPad(buf, len(packetAExt), len(buf)); err != nil {
		t.Fatalf("PacketPad(packet with extensions): %v", err)
	}
	if got := hex.EncodeToString(buf); got != wantPadded {
		t.Fatalf("PacketPad(packet with extensions)=%s want=%s", got, wantPadded)
	}

	unpaddedLen, err := PacketUnpad(buf, len(buf))
	if err != nil {
		t.Fatalf("PacketUnpad(packet with extensions): %v", err)
	}
	if got, want := unpaddedLen, 4; got != want {
		t.Fatalf("PacketUnpad len=%d want=%d", got, want)
	}
	if got := hex.EncodeToString(buf[:unpaddedLen]); got != wantUnpadded {
		t.Fatalf("PacketUnpad(packet with extensions)=%s want=%s", got, wantUnpadded)
	}
}

func TestSelfDelimitedPacketPreservesPacketExtensions(t *testing.T) {
	packetAExt := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantSelfDelimited := "4b4106031122330baa50deadbe"

	selfDelimited, err := makeSelfDelimitedPacket(packetAExt)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(packet with extensions): %v", err)
	}
	if got := hex.EncodeToString(selfDelimited); got != wantSelfDelimited {
		t.Fatalf("makeSelfDelimitedPacket(packet with extensions)=%s want=%s", got, wantSelfDelimited)
	}

	decoded, consumed, err := decodeSelfDelimitedPacket(selfDelimited)
	if err != nil {
		t.Fatalf("decodeSelfDelimitedPacket(packet with extensions): %v", err)
	}
	if consumed != len(selfDelimited) {
		t.Fatalf("decodeSelfDelimitedPacket consumed=%d want=%d", consumed, len(selfDelimited))
	}
	if got := hex.EncodeToString(decoded); got != hex.EncodeToString(packetAExt) {
		t.Fatalf("decodeSelfDelimitedPacket(packet with extensions)=%s want=%s", got, hex.EncodeToString(packetAExt))
	}
}

func TestDecodeSelfDelimitedPacketPreservesOpaqueMalformedPadding(t *testing.T) {
	packet := mustDecodeHex(t, "4b4102112233ffff")
	selfDelimited := mustDecodeHex(t, "4b410203112233ffff")

	decoded, consumed, err := decodeSelfDelimitedPacket(selfDelimited)
	if err != nil {
		t.Fatalf("decodeSelfDelimitedPacket(malformed padding): %v", err)
	}
	if consumed != len(selfDelimited) {
		t.Fatalf("decodeSelfDelimitedPacket consumed=%d want=%d", consumed, len(selfDelimited))
	}
	if got := hex.EncodeToString(decoded); got != hex.EncodeToString(packet) {
		t.Fatalf("decodeSelfDelimitedPacket(malformed padding)=%s want=%s", got, hex.EncodeToString(packet))
	}
}

func TestPacketPadRejectsLengthBeyondInputOnNoOp(t *testing.T) {
	for _, data := range [][]byte{nil, {0x08}} {
		length := len(data) + 1
		if err := PacketPad(data, length, length); err != ErrInvalidArgument {
			t.Fatalf("PacketPad(len=%d, length=%d, newLen=%d) = %v, want %v", len(data), length, length, err, ErrInvalidArgument)
		}
	}
}
