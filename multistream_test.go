package gopus

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

// generateSurroundTestSignal generates a multi-channel test signal with unique frequency per channel.
// This helps verify channel routing by making each channel's content distinguishable.
func generateSurroundTestSignal(sampleRate, frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	// Use different frequencies for each channel
	// Base frequencies spaced to be distinguishable: 220, 330, 440, 550, 660, 770, 880, 990 Hz
	baseFreq := 220.0

	for s := range frameSize {
		for ch := range channels {
			freq := baseFreq + float64(ch)*110
			val := float32(0.3 * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
			pcm[s*channels+ch] = val
		}
	}
	return pcm
}

func generateSurroundTestSignalInt24(sampleRate, frameSize, channels int) []int32 {
	pcm := make([]int32, frameSize*channels)
	baseFreq := 220.0

	for s := range frameSize {
		for ch := range channels {
			freq := baseFreq + float64(ch)*110
			val := int32((1 << 22) * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
			pcm[s*channels+ch] = val
		}
	}
	return pcm
}

// computeEnergyFloat32 computes the RMS energy of a float32 signal.
func computeEnergyFloat32(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// computeChannelEnergy computes the RMS energy for a single channel in interleaved audio.
func computeChannelEnergy(samples []float32, channels, targetChannel int) float64 {
	if len(samples) == 0 || targetChannel >= channels {
		return 0
	}
	var sum float64
	var count int
	for i := targetChannel; i < len(samples); i += channels {
		sum += float64(samples[i]) * float64(samples[i])
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

func TestMultistreamConstructorErrorsReportSupportedRanges(t *testing.T) {
	tests := []struct {
		name        string
		run         func() error
		wantIs      error
		wantContain []string
	}{
		{
			name: "default encoder low channel count",
			run: func() error {
				_, err := NewMultistreamEncoderDefault(48000, 0, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"default multistream encoder supports 1-8 channels", "got 0"},
		},
		{
			name: "default encoder high channel count",
			run: func() error {
				_, err := NewMultistreamEncoderDefault(48000, 9, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"default multistream encoder supports 1-8 channels", "got 9", "NewMultistreamEncoder"},
		},
		{
			name: "default decoder high channel count",
			run: func() error {
				_, err := NewMultistreamDecoderDefault(48000, 9)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"default multistream decoder supports 1-8 channels", "got 9", "NewMultistreamDecoder"},
		},
		{
			name: "explicit encoder high channel count",
			run: func() error {
				_, err := NewMultistreamEncoder(48000, 256, 1, 0, make([]byte, 256), ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"multistream encoder supports 1-255 channels", "got 256"},
		},
		{
			name: "explicit decoder high channel count",
			run: func() error {
				_, err := NewMultistreamDecoder(48000, 256, 1, 0, make([]byte, 256))
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"multistream decoder supports 1-255 channels", "got 256"},
		},
		{
			name: "explicit encoder stream budget",
			run: func() error {
				_, err := NewMultistreamEncoder(48000, 2, 200, 56, []byte{0, 1}, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidStreams,
			wantContain: []string{"streams + coupledStreams <= 255", "200 + 56 = 256"},
		},
		{
			name: "explicit decoder stream budget",
			run: func() error {
				_, err := NewMultistreamDecoder(48000, 2, 200, 56, []byte{0, 1})
				return err
			},
			wantIs:      ErrInvalidStreams,
			wantContain: []string{"streams + coupledStreams <= 255", "200 + 56 = 256"},
		},
		{
			name: "explicit encoder mapping length",
			run: func() error {
				_, err := NewMultistreamEncoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3}, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidMapping,
			wantContain: []string{"expected 6 mapping entries for 6 channels", "got 5"},
		},
		{
			name: "explicit decoder mapping length",
			run: func() error {
				_, err := NewMultistreamDecoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3})
				return err
			},
			wantIs:      ErrInvalidMapping,
			wantContain: []string{"expected 6 mapping entries for 6 channels", "got 5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tc.wantIs)
			}
			for _, needle := range tc.wantContain {
				if !strings.Contains(err.Error(), needle) {
					t.Fatalf("error %q does not contain %q", err.Error(), needle)
				}
			}
		})
	}
}

func TestMultistreamEncodeFloat32DefaultLSBUsesInputPCM(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 6
		frameSize  = 960
	)

	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	ref := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)

	data := make([]byte, maxPacketBytesPerStream*enc.Streams())
	n, err := enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	if n == 0 {
		t.Fatal("Encode() returned an empty packet")
	}

	refPacket, err := ref.enc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatalf("reference multistream Encode() error: %v", err)
	}
	if len(refPacket) == 0 {
		t.Fatal("reference multistream Encode() returned an empty packet")
	}

	got := data[:n]
	if !bytes.Equal(got, refPacket) {
		t.Fatalf("Encode() packet mismatch at default LSB depth: got %d bytes want %d", len(got), len(refPacket))
	}
	if gotRange, wantRange := enc.FinalRange(), ref.FinalRange(); gotRange != wantRange {
		t.Fatalf("FinalRange() = 0x%08x, want 0x%08x", gotRange, wantRange)
	}
}
