// Package util provides common utility functions for the gopus codec.
package util

// Signed is a constraint for signed integer and float types.
type Signed interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~float32 | ~float64
}

// Abs returns the absolute value of x.
func Abs[T Signed](x T) T {
	if x < 0 {
		return -x
	}
	return x
}
