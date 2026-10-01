//go:build gopus_dred || gopus_osce

package gopus

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

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
