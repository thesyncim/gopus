package silk

import "errors"

var (
	// ErrInvalidResampleRate indicates an invalid source rate for resampling.
	ErrInvalidResampleRate = errors.New("silk: invalid resample rate")

	// ErrMismatchedLengths indicates mismatched slice lengths in stereo unmixing.
	ErrMismatchedLengths = errors.New("silk: mismatched slice lengths")
)
