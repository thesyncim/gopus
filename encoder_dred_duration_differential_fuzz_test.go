//go:build gopus_dred || gopus_osce

package gopus

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	encpkg "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"io"
	"math"
	"strconv"
	"testing"
)

func TestEncoderDREDDurationDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	// DRED durations in 10 ms units. libopus clamps to [0,104]; the high end
	// exercises the maximum 104-unit redundancy window. 0 means "no DRED" and is
	// excluded — this sweep targets the carried payload across the active range.
	//
	// The full active range includes short CBR durations. If no DRED fits in
	// the 640-frame window, both encoders must agree on that absence.
	durations := []int{8, 16, 32, 48, 64, 80, 96, 104}

	modes := []struct {
		name      string
		mode      encpkg.Mode
		public    Mode
		bandwidth Bandwidth
		channels  int
	}{
		{"silk_wb_mono", encpkg.ModeSILK, ModeSILK, BandwidthWideband, 1},
		{"silk_wb_stereo", encpkg.ModeSILK, ModeSILK, BandwidthWideband, 2},
		{"hybrid_fb_mono", encpkg.ModeHybrid, ModeHybrid, BandwidthFullband, 1},
		{"celt_fb_mono", encpkg.ModeCELT, ModeCELT, BandwidthFullband, 1},
		{"celt_fb_stereo", encpkg.ModeCELT, ModeCELT, BandwidthFullband, 2},
	}

	rcModes := []struct {
		name string
		cbr  bool
	}{
		{"vbr", false},
		{"cbr", true},
	}

	const frameSize = 960 // 20 ms at 48 kHz
	var (
		tested        int
		payloadOK     int
		indexMismatch int
		offsetMismat  int
		payloadFails  int
		absentOK      int
	)

	for _, m := range modes {
		for _, rc := range rcModes {
			for _, dur := range durations {
				name := fmt.Sprintf("%s/%s/dur%d", m.name, rc.name, dur)
				t.Run(name, func(t *testing.T) {
					tested++
					cfg := libopusDREDPacketConfig{
						FrameSize:    frameSize,
						ForceMode:    m.public,
						Bandwidth:    m.bandwidth,
						Channels:     m.channels,
						CBR:          rc.cbr,
						DREDDuration: dur,
					}
					if rc.cbr {
						cfg.Bitrate = 64000
						if m.public == ModeSILK {
							cfg.Bitrate = 32000
						}
					}
					packetInfo, err := emitLibopusDREDPacketWithConfigRecord(cfg, true)
					if err != nil {
						t.Fatalf("DRED duration packet oracle: %v", err)
					}
					bitrate := 0
					if rc.cbr {
						bitrate = cfg.Bitrate
					}
					settings := encoderDREDPacketSettings{
						mode: m.mode, bandwidth: m.bandwidth, frameSize: frameSize,
						channels: m.channels, bitrate: bitrate, cbr: rc.cbr, dredDuration: dur,
					}
					if len(packetInfo.packet) == 0 {
						gotPacket, gotPayload, _, gotFrameIndex := scanDREDEmissionWithSettings(t, settings, false)
						if gotPacket != nil || gotPayload != nil || gotFrameIndex != packetInfo.frameIndex {
							t.Fatalf("DRED absence mismatch: Go packet=%d payload=%d frame=%d C frame=%d",
								len(gotPacket), len(gotPayload), gotFrameIndex, packetInfo.frameIndex)
						}
						absentOK++
						return
					}
					wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
					if err != nil {
						t.Fatalf("findDREDPayload(libopus) error: %v", err)
					}
					if !ok {
						t.Fatalf("libopus %s packet missing DRED payload at duration %d", m.name, dur)
					}

					gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithSettings(t, settings)
					if ParseTOC(gotPacket[0]).Mode != m.public {
						t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, m.public)
					}
					if gotFrameIndex != packetInfo.frameIndex {
						indexMismatch++
						t.Fatalf("DRED emission frame index=%d want %d (dur=%d)", gotFrameIndex, packetInfo.frameIndex, dur)
					}
					if gotOffset != wantOffset {
						offsetMismat++
						t.Fatalf("DRED frameOffset=%d want %d (dur=%d)", gotOffset, wantOffset, dur)
					}
					if !bytes.Equal(gotPayload, wantPayload) {
						payloadFails++
						t.Fatalf("DRED payload mismatch at duration %d\n got=%x\nwant=%x", dur, gotPayload, wantPayload)
					}
					payloadOK++
				})
			}
		}
	}
	t.Logf("DRED duration differential sweep: %d specs; payload-exact=%d absent-exact=%d index-mismatch=%d offset-mismatch=%d payload-fails=%d",
		tested, payloadOK, absentOK, indexMismatch, offsetMismat, payloadFails)
}

const (
	dredEncoderSequenceFrameSize = 960
	dredEncoderSequenceFrames    = 220
)

var libopusDREDFullEncoderSequenceHelper libopustest.HelperCache

type libopusDREDFullEncoderFrame struct {
	index      int
	finalRange uint32
	packet     []byte
}

func TestDREDLowDelayFullSequenceEncoderMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm := dredEncoderSequencePCM(t, channels)
			want := runLibopusDREDFullEncoderSequence(t, channels, pcm)
			got, enc := encodeDREDFullSequenceGo(t, channels, pcm)
			if len(got) != len(want) {
				t.Fatalf("frame records=%d want %d", len(got), len(want))
			}

			carriedDRED := 0
			for frame := range want {
				if got[frame].index != frame || want[frame].index != frame {
					t.Fatalf("record %d indices Go=%d C=%d", frame, got[frame].index, want[frame].index)
				}
				if got[frame].finalRange != want[frame].finalRange {
					limit := min(len(got[frame].packet), len(want[frame].packet))
					diff := 0
					for diff < limit && got[frame].packet[diff] == want[frame].packet[diff] {
						diff++
					}
					t.Fatalf("frame %d final range Go=%08x C=%08x; packet lens Go=%d C=%d TOCs Go=%02x C=%02x first diff=%d prefixes Go=%x C=%x",
						frame, got[frame].finalRange, want[frame].finalRange, len(got[frame].packet), len(want[frame].packet),
						got[frame].packet[0], want[frame].packet[0], diff,
						got[frame].packet[:min(len(got[frame].packet), 32)], want[frame].packet[:min(len(want[frame].packet), 32)])
				}
				if !bytes.Equal(got[frame].packet, want[frame].packet) {
					limit := min(len(got[frame].packet), len(want[frame].packet))
					diff := 0
					for diff < limit && got[frame].packet[diff] == want[frame].packet[diff] {
						diff++
					}
					gotDRED, gotOffset, gotOK, gotErr := findDREDPayload(got[frame].packet)
					wantDRED, wantOffset, wantOK, wantErr := findDREDPayload(want[frame].packet)
					t.Fatalf("frame %d packet mismatch at byte %d: Go len=%d C len=%d Go=%x C=%x; DRED Go=(%t,%d,%x,%v) C=(%t,%d,%x,%v)",
						frame, diff, len(got[frame].packet), len(want[frame].packet),
						got[frame].packet[:min(len(got[frame].packet), 32)],
						want[frame].packet[:min(len(want[frame].packet), 32)],
						gotOK, gotOffset, gotDRED, gotErr, wantOK, wantOffset, wantDRED, wantErr)
				}
				_, _, ok, err := findDREDPayload(got[frame].packet)
				if err != nil {
					t.Fatalf("frame %d findDREDPayload: %v", frame, err)
				}
				if ok {
					carriedDRED++
				}
			}
			if carriedDRED == 0 {
				t.Fatal("full low-delay sequence carries no DRED payloads")
			}
			t.Logf("matched %d/%d packets and final ranges; %d carry DRED", len(got), len(want), carriedDRED)
			assertDREDFullSequenceEncodeZeroAlloc(t, enc, channels, pcm)
		})
	}
}

func dredEncoderSequencePCM(t *testing.T, channels int) []float32 {
	t.Helper()
	pcm := make([]float32, dredEncoderSequenceFrames*dredEncoderSequenceFrameSize*channels)
	mono := make([]float32, dredEncoderSequenceFrameSize)
	for frame := 0; frame < dredEncoderSequenceFrames; frame++ {
		fillDREDQualitySpeechFrame(mono, frame)
		for sample, value := range mono {
			for channel := 0; channel < channels; channel++ {
				pcm[(frame*dredEncoderSequenceFrameSize+sample)*channels+channel] = value
			}
		}
	}
	return pcm
}

func encodeDREDFullSequenceGo(t *testing.T, channels int, pcm []float32) ([]libopusDREDFullEncoderFrame, *Encoder) {
	t.Helper()
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: ApplicationLowDelay,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	controls := []struct {
		name string
		set  func() error
	}{
		{"bitrate", func() error { return enc.SetBitrate(48000) }},
		{"complexity", func() error { return enc.SetComplexity(10) }},
		{"bandwidth", func() error { return enc.SetBandwidth(BandwidthFullband) }},
		{"max bandwidth", func() error { return enc.SetMaxBandwidth(BandwidthFullband) }},
		{"signal", func() error { return enc.SetSignal(SignalVoice) }},
		{"packet loss", func() error { return enc.SetPacketLoss(60) }},
		{"force channels", func() error { return enc.SetForceChannels(-1) }},
		{"LSB depth", func() error { enc.SetLSBDepth(24); return nil }},
		{"VBR", func() error { enc.SetVBR(true); return nil }},
		{"VBR constraint", func() error { enc.SetVBRConstraint(true); return nil }},
		{"DRED duration", func() error { return enc.SetDREDDuration(80) }},
	}
	for _, control := range controls {
		if err := control.set(); err != nil {
			t.Fatalf("set %s: %v", control.name, err)
		}
	}

	packet := make([]byte, maxPacketBytesPerStream)
	frames := make([]libopusDREDFullEncoderFrame, dredEncoderSequenceFrames)
	for frame := range frames {
		start := frame * dredEncoderSequenceFrameSize * channels
		end := start + dredEncoderSequenceFrameSize*channels
		n, err := enc.Encode(pcm[start:end], packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d): %v", frame, err)
		}
		if n <= 0 {
			t.Fatalf("Encode(frame=%d) returned %d bytes", frame, n)
		}
		frames[frame] = libopusDREDFullEncoderFrame{
			index:      frame,
			finalRange: enc.FinalRange(),
			packet:     append([]byte(nil), packet[:n]...),
		}
	}
	return frames, enc
}

func assertDREDFullSequenceEncodeZeroAlloc(t *testing.T, enc *Encoder, channels int, pcm []float32) {
	t.Helper()
	if enc.enc == nil || !enc.enc.DREDModelLoaded() || !enc.enc.DREDReady() {
		t.Fatal("active DRED encoder is not model-loaded and ready")
	}
	packet := make([]byte, maxPacketBytesPerStream)
	encodeSequence := func() {
		for frame := 0; frame < dredEncoderSequenceFrames; frame++ {
			start := frame * dredEncoderSequenceFrameSize * channels
			end := start + dredEncoderSequenceFrameSize*channels
			n, err := enc.Encode(pcm[start:end], packet)
			if err != nil {
				t.Fatalf("active DRED Encode(frame=%d): %v", frame, err)
			}
			if n <= 0 {
				t.Fatalf("active DRED Encode(frame=%d) returned %d bytes", frame, n)
			}
		}
	}
	encodeSequence()
	allocs := testing.AllocsPerRun(3, encodeSequence)
	if allocs != 0 {
		t.Fatalf("active public DRED Encode full sequence allocs/op=%.2f want 0", allocs)
	}
	t.Logf("active public DRED Encode: %d warmed frames/cycle, %.0f allocs/op", dredEncoderSequenceFrames, allocs)
}

func runLibopusDREDFullEncoderSequence(t *testing.T, channels int, pcm []float32) []libopusDREDFullEncoderFrame {
	t.Helper()
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatalf("resolve selected libopus reference: %v", err)
	}
	binPath, err := cachedLibopusDREDHelperPath(
		&libopusDREDFullEncoderSequenceHelper,
		"libopus_dred_encoder_sequence.c",
		"gopus_libopus_dred_encoder_sequence",
		true,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED full encoder sequence", err)
	}
	input := make([]byte, len(pcm)*4)
	for sample, value := range pcm {
		binary.LittleEndian.PutUint32(input[sample*4:], math.Float32bits(value))
	}
	pcmHash := sha256.Sum256(input)
	t.Logf("selected libopus=%s reference=%s paired neural models come from selected archive; PCM SHA-256=%s; packet capacity=%d",
		variant, libopustest.RefPath(), hex.EncodeToString(pcmHash[:]), maxPacketBytesPerStream)
	output, err := libopustest.RunHelperArgs(
		binPath,
		input,
		strconv.Itoa(channels),
		strconv.Itoa(dredEncoderSequenceFrames),
		strconv.Itoa(maxPacketBytesPerStream),
	)
	if err != nil {
		t.Fatalf("run libopus DRED full encoder sequence: %v", err)
	}
	return parseLibopusDREDFullEncoderSequence(t, output, channels)
}

func parseLibopusDREDFullEncoderSequence(t *testing.T, data []byte, channels int) []libopusDREDFullEncoderFrame {
	t.Helper()
	r := bytes.NewReader(data)
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil || string(header[:]) != "GDES" {
		t.Fatalf("libopus DRED encoder sequence magic=%q err=%v", header, err)
	}
	readU32 := func() uint32 {
		var value uint32
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			t.Fatalf("read libopus sequence header: %v", err)
		}
		return value
	}
	if got := readU32(); got != 1 {
		t.Fatalf("libopus sequence version=%d want 1", got)
	}
	if got := readU32(); got != 48000 {
		t.Fatalf("libopus sample rate=%d want 48000", got)
	}
	if got := readU32(); got != uint32(channels) {
		t.Fatalf("libopus channels=%d want %d", got, channels)
	}
	if got := readU32(); got != dredEncoderSequenceFrameSize {
		t.Fatalf("libopus frame size=%d want %d", got, dredEncoderSequenceFrameSize)
	}
	if got := readU32(); got != dredEncoderSequenceFrames {
		t.Fatalf("libopus frame count=%d want %d", got, dredEncoderSequenceFrames)
	}

	frames := make([]libopusDREDFullEncoderFrame, dredEncoderSequenceFrames)
	for frame := range frames {
		index := int(readU32())
		packetLen := int(readU32())
		finalRange := readU32()
		if packetLen <= 0 || packetLen > maxPacketBytesPerStream {
			t.Fatalf("frame %d libopus packet length=%d", frame, packetLen)
		}
		packet := make([]byte, packetLen)
		if _, err := io.ReadFull(r, packet); err != nil {
			t.Fatalf("read libopus packet frame %d: %v", frame, err)
		}
		frames[frame] = libopusDREDFullEncoderFrame{index: index, finalRange: finalRange, packet: packet}
	}
	if r.Len() != 0 {
		t.Fatalf("libopus sequence has %d trailing output bytes", r.Len())
	}
	return frames
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo CELT packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeCELT, BandwidthFullband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeCELT || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want celt stereo", toc)
	}

	// DRED payload offset and bytes are independent of the CELT primary-frame
	// length, so we still gate on them strictly.
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}

	delta := len(gotPacket) - len(packetInfo.packet)
	if delta != 0 {
		t.Fatalf("CELT FB stereo packet length=%d want %d (delta=%d bytes)",
			len(gotPacket), len(packetInfo.packet), delta)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus hybrid packet missing DRED payload")
	}
	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func assertDREDPacketExtensionFramingMatchesLibopus(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	_, gotFrames, gotPadding, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parse got DRED packet frames: %v", err)
	}
	_, wantFrames, wantPadding, _, err := parsePacketFramesAndPadding(wantPacket)
	if err != nil {
		t.Fatalf("parse libopus DRED packet frames: %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("primary frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	if !bytes.Equal(gotPadding, wantPadding) {
		t.Fatalf("extension padding mismatch\n got=%x\nwant=%x", gotPadding, wantPadding)
	}
}

func assertDREDPacketPrimaryFrameSizesMatchLibopus(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	_, gotFrames, gotPadding, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parse got DRED packet frames: %v", err)
	}
	_, wantFrames, wantPadding, _, err := parsePacketFramesAndPadding(wantPacket)
	if err != nil {
		t.Fatalf("parse libopus DRED packet frames: %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("primary frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if len(gotFrames[i]) != len(wantFrames[i]) {
			t.Fatalf("primary frame %d length=%d want %d", i, len(gotFrames[i]), len(wantFrames[i]))
		}
	}
	if !bytes.Equal(gotPadding, wantPadding) {
		t.Fatalf("extension padding mismatch\n got=%x\nwant=%x", gotPadding, wantPadding)
	}
}

var libopusDREDEncoderModelBlobHelper libopustest.HelperCache

func getLibopusDREDEncoderModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDEncoderModelBlobHelper, "libopus_dred_encoder_model_blob.c", "gopus_libopus_dred_encoder_model_blob", true)
}

func probeLibopusDREDEncoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDEncoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusPitchDNNModelBlob() ([]byte, error) {
	binPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusEncoderNeuralModelBlob() ([]byte, error) {
	pitchBlob, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		return nil, err
	}
	dredBlob, err := probeLibopusDREDEncoderModelBlob()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob, nil
}

func requireLibopusEncoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusEncoderNeuralModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "encoder neural model", err)
	}
	return blob
}

// assertCarriedDREDPayloadParity compares every carried DRED payload byte with
// the instruction-paired libopus DRED reference, then exercises the public
// decoder on that same carrier packet.
func assertCarriedDREDPayloadParity(t *testing.T, gotPacket []byte, gotPayload, wantPayload []byte, gotOffset int) {
	t.Helper()
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertCarriedDREDPayloadStructuralParity(t, gotPacket, gotPayload, wantPayload, gotOffset)
}

// assertCarriedDREDPayloadStructuralParity validates the exact payload's chunk
// structure and decodes it through the standalone and public DRED paths.
func assertCarriedDREDPayloadStructuralParity(t *testing.T, gotPacket []byte, gotPayload, wantPayload []byte, gotOffset int) {
	t.Helper()

	gotParsed, err := internaldred.ParsePayload(gotPayload, gotOffset)
	if err != nil {
		t.Fatalf("gopus DRED payload does not parse: %v\n got=%x", err, gotPayload)
	}
	wantParsed, err := internaldred.ParsePayload(wantPayload, gotOffset)
	if err != nil {
		t.Fatalf("libopus DRED payload does not parse: %v\nwant=%x", err, wantPayload)
	}
	if gotParsed.PayloadLatents != wantParsed.PayloadLatents {
		t.Fatalf("DRED chunk/latent count=%d want %d (got=%x want=%x)",
			gotParsed.PayloadLatents, wantParsed.PayloadLatents, gotPayload, wantPayload)
	}
	if gotParsed.PayloadLatents <= 0 {
		t.Fatalf("DRED payload carries no latents: %x", gotPayload)
	}
	if gotParsed.Header.QMax < gotParsed.Header.Q0 {
		t.Fatalf("DRED header QMax=%d < Q0=%d", gotParsed.Header.QMax, gotParsed.Header.Q0)
	}

	assertCarriedDREDPayloadRoundTrips(t, gotPacket)
}

// assertCarriedDREDPayloadRoundTrips drives the standalone DRED decoder + the
// public Decoder.DecodeDRED path over the carrier packet, asserting the emitted
// DRED extension parses, processes, and reconstructs the expected sample count.
func assertCarriedDREDPayloadRoundTrips(t *testing.T, gotPacket []byte) {
	t.Helper()

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}

	toc := ParseTOC(gotPacket[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}
	const decoderSampleRate = 48000

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	standalone := NewDREDDecoder()
	if err := standalone.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("standalone SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	maxDRED, parseRate := libopusDREDRequestForDecoder(libopusDREDPacket{
		sampleRate:     decoderSampleRate,
		maxDREDSamples: decoderSampleRate, // request up to 1 s; the parse clamps to the carried coverage
	}, decoderSampleRate)
	available, _, err := standalone.Parse(dred, gotPacket, maxDRED, parseRate, true)
	if err != nil {
		t.Fatalf("standalone Parse(carried payload) error: %v", err)
	}
	if dred.Empty() || dred.LatentCount() <= 0 {
		t.Fatalf("standalone Parse retained no DRED latents (available=%d)", available)
	}
	if err := standalone.Process(dred, dred); err != nil {
		t.Fatalf("standalone Process error: %v", err)
	}
	if !dred.Processed() {
		t.Fatal("standalone DRED did not reach processed state")
	}
	if available <= 0 {
		t.Fatalf("standalone Parse reported no available DRED samples: %d", available)
	}

	frameSamples := decoderSampleRate / 50 // one 20 ms frame
	pcm := make([]float32, frameSamples*channels)
	n, err := dec.DecodeDRED(dred, frameSamples, pcm, frameSamples)
	if err != nil {
		t.Fatalf("DecodeDRED(carried payload) error: %v", err)
	}
	if n != frameSamples {
		t.Fatalf("DecodeDRED=%d want %d", n, frameSamples)
	}
}

func encoderDREDBitrateForFrameSize(frameSize int) int {
	bitrate := 40000
	if frameSize > 0 && frameSize < 960 {
		bitrate = (40000 * 960) / frameSize
	}
	if bitrate > 320000 {
		bitrate = 320000
	}
	return bitrate
}

func encoderDREDVoicedSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func encoderDREDFrame(frameIdx, frameSize, sampleRate, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := encoderDREDVoicedSample(frameIdx, i, frameSize, sampleRate)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = s
		}
	}
	return pcm
}

func encoderModeToPublic(mode encpkg.Mode) (Mode, error) {
	switch mode {
	case encpkg.ModeSILK:
		return ModeSILK, nil
	case encpkg.ModeHybrid:
		return ModeHybrid, nil
	case encpkg.ModeCELT:
		return ModeCELT, nil
	default:
		return 0, fmt.Errorf("unsupported encoder mode %v", mode)
	}
}

func encodeUntilDREDPacket(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int) {
	packet, payload, offset, _ := encodeUntilDREDPacketWithFrameIndex(t, mode, bandwidth, frameSize, channels)
	return packet, payload, offset
}

func encodeUntilDREDPacketWithFrameIndex(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int, int) {
	return encodeUntilDREDPacketWithSettings(t, encoderDREDPacketSettings{
		mode:      mode,
		bandwidth: bandwidth,
		frameSize: frameSize,
		channels:  channels,
		bitrate:   encoderDREDBitrateForFrameSize(frameSize),
	})
}

type encoderDREDPacketSettings struct {
	mode         encpkg.Mode
	bandwidth    Bandwidth
	frameSize    int
	channels     int
	bitrate      int
	cbr          bool
	dredDuration int // 0 selects the default 80
}

func encodeUntilDREDPacketWithSettings(t *testing.T, settings encoderDREDPacketSettings) ([]byte, []byte, int, int) {
	return scanDREDEmissionWithSettings(t, settings, true)
}

// scanDREDEmissionWithSettings checks the same 640-frame window as the C emit
// oracle. requireEmission controls only whether absence is an expected result.
func scanDREDEmissionWithSettings(t *testing.T, settings encoderDREDPacketSettings, requireEmission bool) ([]byte, []byte, int, int) {
	t.Helper()
	if settings.frameSize <= 0 {
		settings.frameSize = 960
	}
	if settings.channels <= 0 {
		settings.channels = 1
	}
	if settings.bitrate <= 0 {
		settings.bitrate = encoderDREDBitrateForFrameSize(settings.frameSize)
	}

	cfg := EncoderConfig{
		SampleRate:  48000,
		Channels:    settings.channels,
		Application: ApplicationAudio,
	}
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(settings.frameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(settings.bandwidth); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(settings.bitrate); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if settings.cbr {
		if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
			t.Fatalf("SetBitrateMode(CBR) error: %v", err)
		}
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dredDuration := settings.dredDuration
	if dredDuration == 0 {
		dredDuration = 80
	}
	if err := enc.SetDREDDuration(dredDuration); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	enc.enc.SetMode(settings.mode)

	wantMode, err := encoderModeToPublic(settings.mode)
	if err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, maxPacketBytesPerStream)
	for frameIdx := 0; frameIdx < libopusDREDPacketMaxFramesToTry; frameIdx++ {
		pcm := encoderDREDFrame(frameIdx, settings.frameSize, cfg.SampleRate, cfg.Channels)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frameIdx, err)
		}
		gotPacket := append([]byte(nil), packet[:n]...)
		toc := ParseTOC(gotPacket[0])
		packetDuration, err := opusPacketDurationSamples(gotPacket)
		if err != nil {
			t.Fatalf("parse packet duration frame=%d: %v", frameIdx, err)
		}
		if toc.Mode != wantMode || toc.Bandwidth != settings.bandwidth || packetDuration != settings.frameSize {
			continue
		}
		payload, frameOffset, ok, err := findDREDPayload(gotPacket)
		if err != nil {
			t.Fatalf("findDREDPayload(frame=%d) error: %v", frameIdx, err)
		}
		if ok {
			return gotPacket, append([]byte(nil), payload...), frameOffset, frameIdx
		}
	}
	if !requireEmission {
		return nil, nil, 0, libopusDREDPacketMaxFramesToTry
	}
	t.Fatalf("no DRED packet emitted for mode=%v bandwidth=%v frameSize=%d", settings.mode, settings.bandwidth, settings.frameSize)
	return nil, nil, 0, 0
}

func TestEncoderDREDEmissionFrameIndexMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      encpkg.Mode
		public    Mode
		bandwidth Bandwidth
		channels  int
	}{
		{name: "silk", mode: encpkg.ModeSILK, public: ModeSILK, bandwidth: BandwidthWideband, channels: 1},
		{name: "hybrid", mode: encpkg.ModeHybrid, public: ModeHybrid, bandwidth: BandwidthFullband, channels: 1},
		{name: "celt", mode: encpkg.ModeCELT, public: ModeCELT, bandwidth: BandwidthFullband, channels: 1},
		{name: "stereo_celt", mode: encpkg.ModeCELT, public: ModeCELT, bandwidth: BandwidthFullband, channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: tc.public,
				Bandwidth: tc.bandwidth,
				Channels:  tc.channels,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "DRED packet", err)
			}
			_, _, _, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, tc.mode, tc.bandwidth, 960, tc.channels)
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
		})
	}
}

func assertSilkMonoPrimaryFrameByteExact(t *testing.T, frameSize int) {
	t.Helper()
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	gotPacket, _, _ := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 1)
	_, gotFrames, _, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(got): %v", err)
	}
	_, wantFrames, _, _, err := parsePacketFramesAndPadding(packetInfo.packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(want): %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if !bytes.Equal(gotFrames[i], wantFrames[i]) {
			t.Fatalf("frame %d DIVERGES\n got=%x\nwant=%x", i, gotFrames[i], wantFrames[i])
		}
	}
}

func TestEncoderSilkMono20msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 960)
}

func TestEncoderSilkMono40msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 1920)
}

func TestEncoderSilkMono60msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 2880)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msCBR(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Bitrate:   24000,
		CBR:       true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus CBR silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithSettings(t, encoderDREDPacketSettings{
		mode:      encpkg.ModeSILK,
		bandwidth: BandwidthWideband,
		frameSize: 960,
		channels:  1,
		bitrate:   24000,
		cbr:       true,
	})
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if gotFrameIndex != packetInfo.frameIndex {
		t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 2880,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "60 ms DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 60 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 2880, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWidebandLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{3840, 4800, 5760} {
		t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "long SILK DRED packet", err)
			}
			wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
			if err != nil {
				t.Fatalf("findDREDPayload(libopus) error: %v", err)
			}
			if !ok {
				t.Fatal("libopus long SILK packet missing DRED payload")
			}

			gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 1)
			if ParseTOC(gotPacket[0]).Mode != ModeSILK {
				t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
			}
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
			if len(gotPacket) != len(packetInfo.packet) {
				t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
			}
			if gotOffset != wantOffset {
				t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
			}
			if !bytes.Equal(gotPayload, wantPayload) {
				t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
			}
			assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
		})
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereoPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus CELT packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeCELT, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeCELT {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeCELT)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullbandLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		frameSize int
		channels  int
	}{
		{frameSize: 1920, channels: 1},
		{frameSize: 2880, channels: 1},
		{frameSize: 1920, channels: 2},
	} {
		tc := tc
		t.Run(fmt.Sprintf("%d_samples_%dch", tc.frameSize, tc.channels), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
				Channels:  tc.channels,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "CELT DRED packet", err)
			}
			wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
			if err != nil {
				t.Fatalf("findDREDPayload(libopus) error: %v", err)
			}
			if !ok {
				t.Fatal("libopus CELT packet missing DRED payload")
			}

			gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, encpkg.ModeCELT, BandwidthFullband, tc.frameSize, tc.channels)
			toc := ParseTOC(gotPacket[0])
			if toc.Mode != ModeCELT || toc.Stereo != (tc.channels == 2) {
				t.Fatalf("got packet toc=%+v want celt channels=%d", toc, tc.channels)
			}
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
			if gotOffset != wantOffset {
				t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
			}
			assertCarriedDREDPayloadParity(t, gotPacket, gotPayload, wantPayload, gotOffset)
		})
	}
}

func assertSilkStereoPrimaryFrameByteExact(t *testing.T, frameSize int) {
	t.Helper()
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	gotPacket, _, _ := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 2)
	_, gotFrames, _, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(got): %v", err)
	}
	_, wantFrames, _, _, err := parsePacketFramesAndPadding(packetInfo.packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(want): %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if !bytes.Equal(gotFrames[i], wantFrames[i]) {
			t.Fatalf("frame %d DIVERGES\n got=%x\nwant=%x", i, gotFrames[i], wantFrames[i])
		}
	}
}

func TestEncoderSilkStereo20msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkStereoPrimaryFrameByteExact(t, 960)
}

func TestEncoderSilkStereo40msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkStereoPrimaryFrameByteExact(t, 1920)
}

func TestEncoderSilkStereo60msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkStereoPrimaryFrameByteExact(t, 2880)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 40 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 2880,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 60 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 60 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 2880, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}
