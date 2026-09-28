package multistream

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
)

func TestMultistreamConstructorsValidateSampleRate(t *testing.T) {
	for _, rate := range []int{-48000, 0, 1, 7999, 8000, 12000, 16000, 24000, 44100, 48000, 96000, 192000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			valid := rate == 8000 || rate == 12000 || rate == 16000 || rate == 24000 || rate == 48000 || rate == 96000 && extsupport.QEXT
			dec, decErr := NewDecoder(rate, 1, 1, 0, []byte{0})
			enc, encErr := NewEncoder(rate, 1, 1, 0, []byte{0})
			if valid {
				if decErr != nil || encErr != nil || dec == nil || enc == nil {
					t.Fatalf("valid rate: decoder=%v encoder=%v", decErr, encErr)
				}
			} else if decErr != ErrInvalidSampleRate || encErr != ErrInvalidSampleRate || dec != nil || enc != nil {
				t.Fatalf("invalid rate: decoder=%v encoder=%v", decErr, encErr)
			}
		})
	}
}

func TestProjectionDecoderValidatesChannelsBeforeAllocation(t *testing.T) {
	for _, channels := range []int{-1, 0, 256, int(^uint(0) >> 1)} {
		dec, err := NewProjectionDecoder(48000, channels, 1, 0, nil)
		if dec != nil || err != ErrInvalidChannels {
			t.Fatalf("channels=%d: decoder=%v error=%v", channels, dec, err)
		}
	}
}
