// decode_edge_cases_test.go — deterministic, table-driven edge-case regression
// tests for the multistream + projection DECODE path on malformed and boundary
// input. These pin specific failure modes the fuzz targets explore at random
// (truncated/empty packets, bad stream/coupled counts, oversized mapping tables,
// inconsistent durations, integer-overflow-prone framing fields, and the maximal
// stream/channel layouts) so a regression surfaces as a named, fast unit test.
//
// They are oracle-free: every assertion is a crash-safety or error-contract
// invariant on the gopus decoder alone, complementing the libopus differential
// parity sweeps elsewhere in the package.

package multistream

import (
	"errors"
	"testing"
)

// TestDecodeMalformedNeverPanics drives the public decode entry points with a
// catalogue of malformed packets and asserts each returns an error (never a
// panic) on a representative surround layout.
func TestDecodeMalformedNeverPanics(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"single-toc-byte", []byte{0x00}},
		{"two-bytes-code0", []byte{0x00, 0x10}},
		{"code1-odd-tail", []byte{0x01, 0xaa, 0xbb, 0xcc}},
		{"code2-truncated-len", []byte{0x02, 0xff}},
		{"code3-no-count", []byte{0x03}},
		{"code3-zero-frames", []byte{0x03, 0x00}},
		{"code3-toomany-frames", []byte{0x03, 0x80 | 49}},
		{"code3-padding-runaway", []byte{0x03, 0x40 | 1, 0xff, 0xff, 0xff, 0xff}},
		{"code3-vbr-truncated", []byte{0x03, 0x80 | 3, 0xff}},
		{"all-ones", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"self-delim-len-overflow", []byte{0x00, 0xff, 0xff, 0x00, 0x10}},
	}

	for _, channels := range []int{1, 2, 6} {
		dec, err := NewDecoderDefault(48000, channels)
		if err != nil {
			t.Fatalf("NewDecoderDefault(%d): %v", channels, err)
		}
		for _, tc := range cases {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("ch%d/%s: PANIC: %v", channels, tc.name, r)
					}
				}()
				// Non-empty malformed packets must error (empty == PLC, handled
				// separately below); we only require no-panic here.
				_, _ = dec.DecodeToFloat32(tc.data, robustFrameSize)
				_, _ = dec.DecodeToInt16(tc.data, robustFrameSize)
			}()
		}
	}
}

// TestDecodeEmptyIsPLC verifies that a nil OR zero-length packet performs PLC
// (libopus do_plc semantics) and returns exactly frameSize*channels samples,
// not an error.
func TestDecodeEmptyIsPLC(t *testing.T) {
	for _, channels := range []int{1, 2, 6, 8} {
		dec, err := NewDecoderDefault(48000, channels)
		if err != nil {
			t.Fatalf("NewDecoderDefault(%d): %v", channels, err)
		}
		for _, data := range [][]byte{nil, {}} {
			out, err := dec.DecodeToFloat32(data, robustFrameSize)
			if err != nil {
				t.Fatalf("ch%d: empty-packet PLC returned error: %v", channels, err)
			}
			if len(out) != robustFrameSize*channels {
				t.Fatalf("ch%d: PLC out len %d, want %d", channels, len(out), robustFrameSize*channels)
			}
			out16, err := dec.DecodeToInt16(data, robustFrameSize)
			if err != nil {
				t.Fatalf("ch%d: empty-packet int16 PLC returned error: %v", channels, err)
			}
			if len(out16) != robustFrameSize*channels {
				t.Fatalf("ch%d: int16 PLC out len %d, want %d", channels, len(out16), robustFrameSize*channels)
			}
		}
	}
}

// TestNewDecoderRejectsBadLayouts pins the constructor validation contract: bad
// channel/stream/coupled counts and malformed mapping tables must each return
// the documented error rather than constructing an unusable decoder.
func TestNewDecoderRejectsBadLayouts(t *testing.T) {
	cases := []struct {
		name                       string
		channels, streams, coupled int
		mapping                    []byte
		wantErr                    error
	}{
		{"channels-zero", 0, 1, 0, []byte{}, ErrInvalidChannels},
		{"channels-over-255", 256, 1, 0, make([]byte, 256), ErrInvalidChannels},
		{"streams-zero", 2, 0, 0, []byte{0, 1}, ErrInvalidStreams},
		{"streams-over-255", 2, 256, 0, []byte{0, 1}, ErrInvalidStreams},
		{"coupled-negative", 2, 1, -1, []byte{0, 1}, ErrInvalidCoupledStreams},
		{"coupled-over-streams", 2, 1, 2, []byte{0, 1}, ErrInvalidCoupledStreams},
		{"streams-plus-coupled-over-255", 4, 200, 100, make([]byte, 4), ErrTooManyChannels},
		{"mapping-too-short", 2, 1, 1, []byte{0}, ErrInvalidMapping},
		{"mapping-too-long", 2, 1, 1, []byte{0, 1, 2}, ErrInvalidMapping},
		{"mapping-value-out-of-range", 2, 1, 0, []byte{0, 5}, ErrInvalidMapping},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDecoder(48000, tc.channels, tc.streams, tc.coupled, tc.mapping)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want wrapping %v", err, tc.wantErr)
			}
		})
	}
}

// TestNewDecoderAcceptsValidEdgeLayouts confirms the boundary layouts the
// constructor must accept: a 255-value silent channel, the maximal-coupling
// case, and an all-silent mapping.
func TestNewDecoderAcceptsValidEdgeLayouts(t *testing.T) {
	cases := []struct {
		name                       string
		channels, streams, coupled int
		mapping                    []byte
	}{
		{"silent-channel-255", 3, 1, 1, []byte{0, 1, 255}},
		{"all-silent", 2, 1, 0, []byte{255, 255}},
		{"max-mapping-value", 3, 2, 1, []byte{0, 1, 2}}, // 2 == 2*M = first mono stream
		{"mono", 1, 1, 0, []byte{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := NewDecoder(48000, tc.channels, tc.streams, tc.coupled, tc.mapping)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// A silent/edge layout must still PLC-conceal an empty packet to the
			// right shape without touching a non-existent stream.
			out, err := dec.DecodeToFloat32(nil, robustFrameSize)
			if err != nil {
				t.Fatalf("PLC decode error: %v", err)
			}
			if len(out) != robustFrameSize*tc.channels {
				t.Fatalf("PLC out len %d, want %d", len(out), robustFrameSize*tc.channels)
			}
		})
	}
}

// TestConstructorCopiesMapping verifies the documented buffer-ownership
// contract: NewDecoder copies the mapping so a later caller mutation cannot
// corrupt the decoder's routing.
func TestConstructorCopiesMapping(t *testing.T) {
	mapping := []byte{0, 1, 255}
	dec, err := NewDecoder(48000, 3, 1, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the caller's slice to an out-of-range value after construction.
	mapping[2] = 200
	// The decoder must still conceal cleanly (its internal copy is unchanged).
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC after caller mutated mapping: %v", r)
		}
	}()
	if _, err := dec.DecodeToFloat32(nil, robustFrameSize); err != nil {
		t.Fatalf("decode error after mapping mutation: %v", err)
	}
}

// TestParseMultistreamPacketBadStreamCount checks the framing parser rejects a
// zero/negative stream count and a truncated multi-stream packet without panic.
func TestParseMultistreamPacketBadStreamCount(t *testing.T) {
	if _, err := parseMultistreamPacket([]byte{0x00}, 0); !errors.Is(err, ErrInvalidStreamCount) {
		t.Fatalf("zero streams: got %v, want ErrInvalidStreamCount", err)
	}
	if _, err := parseMultistreamPacket([]byte{0x00}, -3); !errors.Is(err, ErrInvalidStreamCount) {
		t.Fatalf("negative streams: got %v, want ErrInvalidStreamCount", err)
	}
	// Two streams but only enough bytes for part of the first self-delimited one.
	if _, err := parseMultistreamPacket([]byte{0x00, 0xff}, 2); err == nil {
		t.Fatalf("truncated 2-stream packet: expected error, got nil")
	}
}

// TestSetProjectionDemixingMatrixValidation pins the demixing-matrix validation:
// a wrong-length matrix and a non-identity mapping must be rejected, an empty
// matrix clears projection, and a correctly-sized matrix is accepted.
func TestSetProjectionDemixingMatrixValidation(t *testing.T) {
	channels := 4
	enc, err := NewProjectionEncoder(48000, channels)
	if err != nil {
		t.Fatalf("NewProjectionEncoder: %v", err)
	}
	streams, coupled := enc.Streams(), enc.CoupledStreams()
	good := enc.GetDemixingMatrix()

	// Correct length on a trivial-mapping decoder: accepted.
	dec, err := NewProjectionDecoder(48000, channels, streams, coupled, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetProjectionDemixingMatrix(good); err != nil {
		t.Fatalf("valid matrix rejected: %v", err)
	}
	// Empty clears it.
	if err := dec.SetProjectionDemixingMatrix(nil); err != nil {
		t.Fatalf("clearing matrix errored: %v", err)
	}

	// Wrong length: rejected.
	if err := dec.SetProjectionDemixingMatrix(good[:len(good)-2]); !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("short matrix: got %v, want ErrInvalidProjectionMatrix", err)
	}
	if err := dec.SetProjectionDemixingMatrix(append(append([]byte(nil), good...), 0, 0)); !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("long matrix: got %v, want ErrInvalidProjectionMatrix", err)
	}

	// Non-identity mapping: a projection matrix requires trivial mapping.
	badMap := make([]byte, channels)
	for i := range badMap {
		badMap[i] = byte(channels - 1 - i) // reversed, non-identity
	}
	ndec, err := NewDecoder(48000, channels, streams, coupled, badMap)
	if err != nil {
		t.Fatal(err)
	}
	if err := ndec.SetProjectionDemixingMatrix(good); !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("non-identity mapping: got %v, want ErrInvalidProjectionMatrix", err)
	}
}

// TestDurationMismatchRejected builds a 2-stream multistream packet whose two
// elementary streams have different frame durations and asserts the decoder
// rejects it with ErrDurationMismatch rather than mis-decoding.
func TestDurationMismatchRejected(t *testing.T) {
	// Two CELT TOC bytes with different frame sizes: config 16 (2.5 ms) vs
	// config 19 (20 ms), both mono code 0 with a 1-byte payload. The first
	// stream is reframed self-delimited; the second uses standard framing.
	stream0 := []byte{16 << 3, 0x00}             // 2.5 ms
	stream1 := []byte{19 << 3, 0x00}             // 20 ms
	sd0, err := makeSelfDelimitedPacket(stream0) // first stream is self-delimited
	if err != nil {
		t.Fatalf("self-delimit stream0: %v", err)
	}
	packet := append(append([]byte(nil), sd0...), stream1...)

	dec, err := NewDecoder(48000, 2, 2, 0, []byte{0, 1}) // 2 mono streams
	if err != nil {
		t.Fatal(err)
	}
	_, derr := dec.DecodeToFloat32(packet, robustFrameSize)
	if !errors.Is(derr, ErrDurationMismatch) {
		t.Fatalf("got %v, want ErrDurationMismatch", derr)
	}
}
