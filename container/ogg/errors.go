package ogg

import "errors"

// Package-level errors for Ogg parsing and encoding.
var (
	// ErrInvalidPage indicates the page structure is malformed.
	// This includes missing "OggS" magic, invalid version, or truncated data.
	ErrInvalidPage = errors.New("ogg: invalid page structure")

	// ErrInvalidHeader indicates an Opus header (OpusHead or OpusTags) is malformed.
	// This includes wrong magic signature, unsupported version, or truncated data.
	ErrInvalidHeader = errors.New("ogg: invalid Opus header")

	// ErrBadCRC indicates the page CRC checksum does not match the computed value.
	// This typically indicates data corruption.
	ErrBadCRC = errors.New("ogg: CRC mismatch")

	// ErrUnexpectedEOS indicates the stream ended unexpectedly.
	// This occurs when a page is truncated or data ends mid-packet.
	ErrUnexpectedEOS = errors.New("ogg: unexpected end of stream")
)
