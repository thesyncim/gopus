package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

func TestDecodeOggTrimsEOSPageToGranule(t *testing.T) {
	const (
		channels     = 2
		frameSamples = 960
		preSkip      = 312
		inputSamples = 48_000
		packetCount  = 51
		finalGranule = preSkip + inputSamples
	)

	packets, decoded := encodePaddedTestPackets(t, channels, frameSamples, packetCount)
	want := decoded[preSkip*channels : finalGranule*channels]

	for _, finalPagePackets := range []int{1, 2} {
		t.Run(map[int]string{1: "single-packet-final-page", 2: "multi-packet-final-page"}[finalPagePackets], func(t *testing.T) {
			stream := makePaddedTestOgg(t, packets, channels, frameSamples, finalPagePackets, finalGranule, 0)

			decoder, err := newOggPCMDecoder(bytes.NewReader(stream))
			if err != nil {
				t.Fatalf("newOggPCMDecoder: %v", err)
			}
			var got []float32
			stats, err := decoder.decode(func(samples []float32) error {
				got = append(got, samples...)
				return nil
			})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if stats.Samples != inputSamples || len(got) != inputSamples*channels {
				t.Fatalf("streamed %d samples/channel (%d interleaved), want %d (%d)", stats.Samples, len(got), inputSamples, inputSamples*channels)
			}
			if stats.Packets != packetCount {
				t.Fatalf("reported %d decoded packets, want %d", stats.Packets, packetCount)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("PCM[%d] = %08x, want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}

			wavPath := filepath.Join(t.TempDir(), "trimmed.wav")
			wavStats, err := decodeOggToWav(bytes.NewReader(stream), wavPath)
			if err != nil {
				t.Fatalf("decodeOggToWav: %v", err)
			}
			wav, err := os.ReadFile(wavPath)
			if err != nil {
				t.Fatalf("read WAV: %v", err)
			}
			wantDataBytes := uint32(inputSamples * channels * 2)
			if wavStats.Samples != inputSamples || len(wav) != 44+int(wantDataBytes) || binary.LittleEndian.Uint32(wav[40:44]) != wantDataBytes {
				t.Fatalf("WAV stats/data = %d samples/%d bytes, header data size %d; want %d/%d", wavStats.Samples, len(wav)-44, binary.LittleEndian.Uint32(wav[40:44]), inputSamples, wantDataBytes)
			}
		})
	}
}

func TestDecodeOggAppliesOpusHeadOutputGain(t *testing.T) {
	const (
		channels     = 2
		frameSamples = 960
		preSkip      = 312
		packetCount  = 51
		finalGranule = 48_312
		outputGainQ8 = 512
	)

	packets, _ := encodePaddedTestPackets(t, channels, frameSamples, packetCount)
	stream := makePaddedTestOgg(t, packets, channels, frameSamples, 1, finalGranule, outputGainQ8)
	decoder, err := newOggPCMDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("newOggPCMDecoder: %v", err)
	}
	if got := decoder.decoder.Gain(); got != outputGainQ8 {
		t.Fatalf("decoder gain = %d, want OpusHead gain %d", got, outputGainQ8)
	}

	reference, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := reference.SetGain(outputGainQ8); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	pcmOut := make([]float32, gopus.DefaultDecoderConfig(sampleRate, channels).MaxPacketSamples*channels)
	var decoded []float32
	for i, packet := range packets {
		n, err := reference.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
		decoded = append(decoded, pcmOut[:n*channels]...)
	}
	want := decoded[preSkip*channels : finalGranule*channels]
	var got []float32
	if _, err := decoder.decode(func(samples []float32) error {
		got = append(got, samples...)
		return nil
	}); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d interleaved samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PCM[%d] = %08x, want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestDecodeOggReturnsIndexedCodecError(t *testing.T) {
	var stream bytes.Buffer
	writer, err := ogg.NewWriter(&stream, sampleRate, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.WritePacket([]byte{0xff}, 960); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Writer.Close: %v", err)
	}

	decoder, err := newOggPCMDecoder(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatalf("newOggPCMDecoder: %v", err)
	}
	_, err = decoder.decode(func([]float32) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "decode packet 0:") {
		t.Fatalf("decode error = %v, want packet-indexed codec error", err)
	}
}

func TestDecodeOggRejectsInvalidEOSGranule(t *testing.T) {
	const (
		channels     = 2
		frameSamples = 960
	)
	packets, _ := encodePaddedTestPackets(t, channels, frameSamples, 2)
	tests := []struct {
		name             string
		finalPagePackets int
		finalGranule     uint64
		wantError        string
	}{
		{
			name:             "first-audio-page-below-preskip",
			finalPagePackets: 2,
			finalGranule:     311,
			wantError:        "below pre-skip",
		},
		{
			name:             "eos-granule-before-previous-page",
			finalPagePackets: 1,
			finalGranule:     959,
			wantError:        "precedes previous audio granule",
		},
		{
			name:             "eos-granule-beyond-decoded-page",
			finalPagePackets: 1,
			finalGranule:     2_000,
			wantError:        "exceeds decoded EOS-page samples",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := makePaddedTestOgg(t, packets, channels, frameSamples, tt.finalPagePackets, tt.finalGranule, 0)
			decoder, err := newOggPCMDecoder(bytes.NewReader(stream))
			if err != nil {
				t.Fatalf("newOggPCMDecoder: %v", err)
			}
			_, err = decoder.decode(func([]float32) error { return nil })
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("decode error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func encodePaddedTestPackets(t *testing.T, channels, frameSamples, packetCount int) ([][]byte, []float32) {
	t.Helper()
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSamples); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}

	pcm := make([]float32, frameSamples*channels)
	packetBuffer := make([]byte, 4000)
	packets := make([][]byte, 0, packetCount)
	for frame := range packetCount {
		for i := 0; i < frameSamples; i++ {
			value := float32(0)
			if frame*frameSamples+i < 48_000 {
				value = float32(0.25 * math.Sin(2*math.Pi*440*float64(frame*frameSamples+i)/sampleRate))
			}
			for channel := 0; channel < channels; channel++ {
				pcm[i*channels+channel] = value
			}
		}
		n, err := enc.Encode(pcm, packetBuffer)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frame, err)
		}
		if n == 0 {
			t.Fatalf("Encode frame %d returned an empty packet", frame)
		}
		packets = append(packets, append([]byte(nil), packetBuffer[:n]...))
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	decoded := make([]float32, 0, packetCount*frameSamples*channels)
	pcmOut := make([]float32, gopus.DefaultDecoderConfig(sampleRate, channels).MaxPacketSamples*channels)
	for i, packet := range packets {
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
		decoded = append(decoded, pcmOut[:n*channels]...)
	}
	return packets, decoded
}

func makePaddedTestOgg(t *testing.T, packets [][]byte, channels, frameSamples, finalPagePackets int, finalGranule uint64, outputGain int16) []byte {
	t.Helper()
	var encoded bytes.Buffer
	config := ogg.WriterConfig{
		SampleRate:    sampleRate,
		Channels:      uint8(channels),
		PreSkip:       ogg.DefaultPreSkip,
		OutputGain:    outputGain,
		MappingFamily: ogg.MappingFamilyRTP,
		StreamCount:   1,
	}
	if channels == 2 {
		config.CoupledCount = 1
	}
	writer, err := ogg.NewWriterWithConfig(&encoded, config)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i, packet := range packets {
		if err := writer.WritePacket(packet, frameSamples); err != nil {
			t.Fatalf("WritePacket %d: %v", i, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Writer.Close: %v", err)
	}

	var pages []*ogg.Page
	for rest := encoded.Bytes(); len(rest) > 0; {
		page, n, err := ogg.ParsePage(rest)
		if err != nil {
			t.Fatalf("ParsePage: %v", err)
		}
		pages = append(pages, page)
		rest = rest[n:]
	}
	if len(pages) != len(packets)+3 {
		t.Fatalf("got %d pages, want %d headers+audio+EOS", len(pages), len(packets)+3)
	}

	firstEOSPage := len(pages) - 1 - finalPagePackets
	if firstEOSPage < 2 {
		t.Fatalf("final page consumes header pages")
	}
	for i := 0; i < finalPagePackets; i++ {
		if len(pages[firstEOSPage+i].Packets()) != 1 {
			t.Fatalf("audio page %d does not contain exactly one packet", firstEOSPage+i)
		}
	}

	var payload []byte
	var segments []byte
	for i := 0; i < finalPagePackets; i++ {
		packet := pages[firstEOSPage+i].Packets()[0]
		payload = append(payload, packet...)
		segments = append(segments, ogg.BuildSegmentTable(len(packet))...)
	}
	finalPage := &ogg.Page{
		HeaderType:   ogg.PageFlagEOS,
		GranulePos:   finalGranule,
		SerialNumber: pages[firstEOSPage].SerialNumber,
		PageSequence: pages[firstEOSPage].PageSequence,
		Segments:     segments,
		Payload:      payload,
	}

	var stream bytes.Buffer
	for _, page := range pages[:firstEOSPage] {
		stream.Write(page.Encode())
	}
	stream.Write(finalPage.Encode())
	return stream.Bytes()
}
