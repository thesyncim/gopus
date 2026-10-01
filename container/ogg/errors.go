package ogg

import "errors"

// Package-level errors for Ogg parsing and encoding.
var (
	// ErrNilReader indicates a nil io.Reader was supplied to NewReader.
	ErrNilReader = errors.New("ogg: nil reader")

	// ErrNilWriter indicates a nil io.Writer was supplied to NewWriter.
	ErrNilWriter = errors.New("ogg: nil writer")

	// ErrInvalidPage indicates malformed page framing or a page that violates
	// the stream structure required by Reader. This includes missing "OggS"
	// magic, a truncated header/lacing table/payload, an invalid first-page BOS
	// marker, or inconsistent header serial numbers. ParsePage preserves the
	// version byte instead of rejecting a
	// nonzero value; checksum mismatches return ErrBadCRC.
	ErrInvalidPage = errors.New("ogg: invalid page structure")

	// ErrInvalidHeader indicates an Opus header (OpusHead or OpusTags) is malformed.
	// This includes wrong magic signature, unsupported version, or truncated data.
	ErrInvalidHeader = errors.New("ogg: invalid Opus header")

	// ErrBadCRC indicates the page CRC checksum does not match the computed value.
	// This typically indicates data corruption.
	ErrBadCRC = errors.New("ogg: CRC mismatch")

	// ErrUnexpectedEOS indicates that Writer.WritePacket was called after
	// Writer.Close. Reader reports stream exhaustion with io.EOF; malformed or
	// truncated page framing is reported separately.
	ErrUnexpectedEOS = errors.New("ogg: unexpected end of stream")

	// ErrPacketTooLarge indicates that ReadPacketInto consumed a packet whose
	// length exceeds len(dst). The packet is consumed even though the method
	// returns n == 0 and this error.
	ErrPacketTooLarge = errors.New("ogg: packet too large for buffer")

	// ErrNotSeekable indicates the reader does not support seeking.
	ErrNotSeekable = errors.New("ogg: reader is not seekable")
)
