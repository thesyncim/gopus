package gopus

import (
	"errors"
	"math"
	"testing"
)

func requireFiniteFloat32(t *testing.T, samples []float32) {
	t.Helper()

	for i, sample := range samples {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Fatalf("sample[%d] is not finite: %v", i, sample)
		}
	}
}

func TestAPIAbuseReturnsError(t *testing.T) {
	t.Run("EncoderSmallOutputBuffer", func(t *testing.T) {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}

		pcm := generateSineWaveFloat32(48000, 440, enc.FrameSize(), 1)
		if _, err := enc.Encode(pcm, make([]byte, 0)); !errors.Is(err, ErrBufferTooSmall) {
			t.Fatalf("Encode error=%v want=%v", err, ErrBufferTooSmall)
		}
	})

	t.Run("EncoderInvalidFrameSize", func(t *testing.T) {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}

		pcm := generateSineWaveFloat32(48000, 440, enc.FrameSize()-1, 1)
		if _, err := enc.Encode(pcm, make([]byte, maxPacketBytesPerStream)); !errors.Is(err, ErrInvalidFrameSize) {
			t.Fatalf("Encode error=%v want=%v", err, ErrInvalidFrameSize)
		}
	})

	t.Run("DecoderTruncatedPacket", func(t *testing.T) {
		dec := newMonoTestDecoder(t)
		pcm := make([]float32, 960)
		if _, err := dec.Decode([]byte{0x03}, pcm); !errors.Is(err, ErrPacketTooShort) {
			t.Fatalf("Decode error=%v want=%v", err, ErrPacketTooShort)
		}
	})

	t.Run("RepeatedPLCStaysFinite", func(t *testing.T) {
		dec := newMonoTestDecoder(t)
		pcm := make([]float32, 960)

		n, err := dec.Decode(nil, pcm)
		if err != nil {
			t.Fatalf("initial Decode(nil) error: %v", err)
		}
		if n != 960 {
			t.Fatalf("initial Decode(nil) samples=%d want=960", n)
		}
		requireFiniteFloat32(t, pcm[:n])

		if _, err := dec.Decode([]byte{0x03}, pcm); !errors.Is(err, ErrPacketTooShort) {
			t.Fatalf("Decode(truncated) error=%v want=%v", err, ErrPacketTooShort)
		}

		for i := 0; i < 8; i++ {
			n, err = dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) iteration %d error: %v", i, err)
			}
			if n != 960 {
				t.Fatalf("Decode(nil) iteration %d samples=%d want=960", i, n)
			}
			requireFiniteFloat32(t, pcm[:n])
		}
	})

	t.Run("MultistreamInvalidMappings", func(t *testing.T) {
		if _, err := NewMultistreamEncoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3}, ApplicationAudio); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("NewMultistreamEncoder error=%v want=%v", err, ErrInvalidMapping)
		}
		if _, err := NewMultistreamDecoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3}); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("NewMultistreamDecoder error=%v want=%v", err, ErrInvalidMapping)
		}
	})
}
