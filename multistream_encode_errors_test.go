package gopus

import (
	"errors"
	"fmt"
	"testing"
)

func TestMultistreamEncodeShortOutputUsesPublicError(t *testing.T) {
	for _, channels := range []int{1, 2, 6} {
		for _, format := range []string{"float32", "int16", "int24"} {
			t.Run(fmt.Sprintf("%s/%dch", format, channels), func(t *testing.T) {
				enc, err := NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				samples := enc.FrameSize() * channels
				pcm32 := make([]float32, samples)
				pcm16 := make([]int16, samples)
				pcm24 := make([]int32, samples)
				encode := func(out []byte) (int, error) {
					switch format {
					case "float32":
						return enc.Encode(pcm32, out)
					case "int16":
						return enc.EncodeInt16(pcm16, out)
					default:
						return enc.EncodeInt24(pcm24, out)
					}
				}
				// opus_multistream_encode_native requires one TOC per stream
				// and a self-delimited length for every stream except the last.
				minimum := enc.Streams()*2 - 1
				for size := 0; size < minimum; size++ {
					n, err := encode(make([]byte, size))
					if n != 0 || !errors.Is(err, ErrBufferTooSmall) {
						t.Fatalf("output size %d: n=%d err=%v; want 0, ErrBufferTooSmall", size, n, err)
					}
				}
			})
		}
	}
}
