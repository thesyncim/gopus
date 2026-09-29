// Package gopus implements Opus audio encoding and decoding in pure Go.
//
// Encoder and Decoder process one stream at a time. Multichannel layouts use
// multistream encoders and decoders. PCM is interleaved for multiple channels;
// float samples use normalized full scale, while integer methods use int16 or
// right-justified 24-bit samples stored in int32 values.
//
// Encode and Decode accept caller-owned buffers. Encoder and Decoder instances
// keep stream state and are not safe for concurrent use; use one instance per
// stream.
//
// Optional codec extensions depend on build tags. SupportsOptionalExtension
// reports which extensions are part of the supported API for the current
// build. Go 1.27 or later is required. The simd experiment enables SIMD kernels
// where implemented, and the nosimd build tag selects scalar kernels.
package gopus
