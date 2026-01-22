// errors.go defines public error types for the gopus package.

package gopus

import "errors"

// Public error types for encoding and decoding operations.
var (
	// ErrInvalidSampleRate indicates an unsupported sample rate.
	// Valid sample rates are: 8000, 12000, 16000, 24000, 48000.
	ErrInvalidSampleRate = errors.New("gopus: invalid sample rate (must be 8000, 12000, 16000, 24000, or 48000)")

	// ErrInvalidChannels indicates an unsupported channel count.
	// Valid channel counts are 1 (mono) or 2 (stereo).
	ErrInvalidChannels = errors.New("gopus: invalid channels (must be 1 or 2)")

	// ErrBufferTooSmall indicates the output buffer is too small for the decoded frame.
	// The buffer must be at least frameSize * channels samples.
	ErrBufferTooSmall = errors.New("gopus: output buffer too small")

	// ErrInvalidFrameSize indicates the input frame size doesn't match expected.
	// The PCM input length must be frameSize * channels.
	ErrInvalidFrameSize = errors.New("gopus: invalid frame size")

	// ErrInvalidBitrate indicates the bitrate is out of valid range.
	// Valid bitrates are 6000 to 510000 bits per second.
	ErrInvalidBitrate = errors.New("gopus: invalid bitrate (must be 6000-510000)")

	// ErrInvalidComplexity indicates the complexity is out of valid range.
	// Valid complexity values are 0 to 10.
	ErrInvalidComplexity = errors.New("gopus: invalid complexity (must be 0-10)")
)

// validSampleRate returns true if the sample rate is valid for Opus.
func validSampleRate(rate int) bool {
	switch rate {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	default:
		return false
	}
}
