// Package multistream encodes and decodes Opus packets with multiple elementary
// streams, as used for surround sound and ambisonics.
//
// Encoder and Decoder map channels to mono or coupled stereo streams. The
// default constructors use Vorbis channel order for layouts with up to eight
// channels; explicit constructors accept a mapping table. Ambisonics helpers
// provide ACN/SN3D layouts, including projection encoding and decoding.
//
// PCM is interleaved by sample. Constructors copy mapping tables and projection
// matrices. Stateful Encoder and Decoder instances are not safe for concurrent
// use.
package multistream
