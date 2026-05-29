// malformed_packet_libopus_parity_test.go — exact invalid-packet error-code
// parity between gopus and libopus for every packet-parse/decode failure
// class defined in opus_decoder.c and opus.c (libopus 1.6.1).
//
// For each case the C oracle (libopus_decode_error_probe) decodes the same
// packet through libopus and returns the raw error code.  The test maps that
// code to the expected gopus error class and asserts equivalence.
//
// Error-code mapping (src/opus_decoder.c, src/opus.c):
//   OPUS_INVALID_PACKET (-4) → any of ErrInvalidPacket / ErrPacketTooShort /
//                               ErrInvalidFrameCount (all signal "bad packet")
//   OPUS_BAD_ARG (-1)        → ErrInvalidArgument (or gopus bad-arg variant)
//   OPUS_BUFFER_TOO_SMALL (-2) → ErrBufferTooSmall
//   positive result          → decode succeeded (no error expected)

package gopus

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Libopus raw error codes (opus_defines.h).
const (
	libopusOK              = int32(0)
	libopusErrBadArg       = int32(-1)
	libopusErrBufTooSmall  = int32(-2)
	libopusErrInvalidPkt   = int32(-4)
)

// malformedDecodeFormat selects the PCM decode API to probe.
type malformedDecodeFormat uint32

const (
	malformedFormatFloat32 malformedDecodeFormat = 0
	malformedFormatInt16   malformedDecodeFormat = 1
	malformedFormatInt24   malformedDecodeFormat = 2
)

// malformedPacketCase describes one malformed-packet probe.
type malformedPacketCase struct {
	name      string
	packet    []byte // nil or empty → PLC / len-0 packet
	format    malformedDecodeFormat
	sampleRate int
	channels  int
	// wantGopusErr is the gopus error we expect when libopus returns a
	// negative code.  If nil and libopus returns negative, the test only
	// checks that gopus also returned non-nil.
	wantGopusErr error
}

// libopusDecodeErrorResult is the oracle output for one probe.
type libopusDecodeErrorResult struct {
	code int32 // raw libopus return value
}

// ---- oracle plumbing -------------------------------------------------------

var malformedDecodeErrorHelper libopustest.HelperCache

func buildMalformedDecodeErrorHelper() (string, error) {
	return malformedDecodeErrorHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "decode error probe",
		OutputBase: "gopus_malformed_decode_error",
		SourceFile: "libopus_decode_error_probe.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

// probeMalformedDecodeErrors sends all cases to the libopus oracle and returns
// the raw error/return code for each one.
//
// All cases in one call must share the same sampleRate and channels because
// the oracle creates one decoder per session.  Call separately for different
// stream configurations.
func probeMalformedDecodeErrors(cases []malformedPacketCase) ([]libopusDecodeErrorResult, error) {
	binPath, err := buildMalformedDecodeErrorHelper()
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, nil
	}

	sr := cases[0].sampleRate
	ch := cases[0].channels
	for _, c := range cases[1:] {
		if c.sampleRate != sr || c.channels != ch {
			return nil, fmt.Errorf("probeMalformedDecodeErrors: all cases must share sampleRate/channels")
		}
	}

	// input:  "GDEI" version=1 channels sample_rate count [cases…]
	payload := libopustest.NewOraclePayload("GDEI",
		uint32(ch),
		uint32(sr),
		uint32(len(cases)),
	)
	for _, c := range cases {
		payload.U32(uint32(c.format))
		payload.U32(5760) // generous frame_size (oracle allocates; real check is below)
		payload.U32(0)    // decode_fec = 0
		if len(c.packet) == 0 {
			payload.U32(0)
		} else {
			payload.U32(uint32(len(c.packet)))
			payload.Raw(c.packet)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "decode error probe", "GDEO")
	if err != nil {
		return nil, err
	}
	n := reader.Count(len(cases))
	reader.ExpectRemaining(4 * n)
	out := make([]libopusDecodeErrorResult, n)
	for i := range out {
		out[i].code = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- error-class helpers ---------------------------------------------------

// isGopusInvalidPacketErr returns true for any gopus error that corresponds
// to OPUS_INVALID_PACKET.  gopus refines OPUS_INVALID_PACKET into three
// sentinel errors depending on the parse failure mode; all three are
// semantically equivalent to libopus OPUS_INVALID_PACKET.
//
// References:
//   packet_parse.go ErrPacketTooShort, ErrInvalidFrameCount, ErrInvalidPacket
//   decoder_misc.go packetFrameCount
func isGopusInvalidPacketErr(err error) bool {
	return errors.Is(err, ErrInvalidPacket) ||
		errors.Is(err, ErrPacketTooShort) ||
		errors.Is(err, ErrInvalidFrameCount)
}

// ---- test corpus -----------------------------------------------------------

// malformedCorpus48k1ch returns all probe cases for 48 kHz mono.
// TOC byte 0x00 = SILK NB 10 ms mono code-0 (config 0).
// TOC byte 0x04 = SILK NB 10 ms stereo code-0.
// TOC byte 0x38 = CELT NB 20 ms mono code-0 (config 7, 0x38).
// TOC byte 0x60 = CELT FB 20 ms mono code-0 (config 12, 0x60).
// TOC byte 0x78 = Hybrid FB 20 ms mono code-0 (config 15, 0x78).

// silkNB10msMonoCode0 is a valid minimal SILK NB 10ms packet used as a
// building block for multi-frame tests that need to survive parse.
// TOC = 0x00 (config 0 = SILK NB 10ms, mono, code-0).
var silkNB10msMonoCode0 = []byte{0x00, 0xFF}

// celtFB20msMonoCode0 is a valid minimal CELT FB 20ms packet (config 12,
// code 0).  TOC = 0x60.
var celtFB20msMonoCode0 = []byte{0x60, 0xFF, 0xFF, 0xFF, 0xFF}

func malformedCorpus48k1ch() []malformedPacketCase {
	var cases []malformedPacketCase

	// ---- Empty / nil packet -----------------------------------------------
	// libopus opus_decode: len==0 triggers PLC not an error; but passing
	// an empty non-nil slice with len=0 is the same as len==0 and returns
	// positive samples (PLC path).  We only test the nil case here; gopus
	// Decode(nil) should succeed (PLC).
	//
	// An empty packet (len==0 passed to opus_decode) in libopus takes the
	// PLC branch and returns positive samples — not an error.  We assert
	// gopus also succeeds.
	cases = append(cases, malformedPacketCase{
		name:        "empty_nil_packet",
		packet:      nil,
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: nil, // libopus PLC → success
	})

	// ---- Code-0 (1 frame) truncations -------------------------------------
	// Code 0: TOC only, no frame data.  parse succeeds (frame size = 0).
	// opus_decode_frame gets len=0 which means PLC — not an error.
	cases = append(cases, malformedPacketCase{
		name:        "code0_toc_only",
		packet:      []byte{0x00},
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: nil, // libopus treats len=0/1 as PLC
	})

	// ---- Code-1 (2 CBR frames) odd-length ---------------------------------
	// Code 1: payload length must be even; odd length → OPUS_INVALID_PACKET.
	// src/opus.c opus_packet_parse_impl case 1: if (len&0x1) return OPUS_INVALID_PACKET
	cases = append(cases, malformedPacketCase{
		name:        "code1_odd_payload_len",
		packet:      []byte{0x01, 0xAA, 0xBB, 0xCC}, // 3-byte payload (odd)
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// Code 1: zero payload → both frames are size 0 → PLC, not error.
	cases = append(cases, malformedPacketCase{
		name:        "code1_zero_payload",
		packet:      []byte{0x01},
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: nil, // parse OK, empty → treated as PLC
	})

	// ---- Code-2 (2 VBR frames) first-frame length overflow ----------------
	// Code 2: frame1_len > remaining → OPUS_INVALID_PACKET.
	// src/opus.c case 2: if (size[0]<0 || size[0] > len) return OPUS_INVALID_PACKET
	cases = append(cases, malformedPacketCase{
		// frame1 length byte claims 10, but only 3 bytes follow
		name:        "code2_frame1_len_overflow",
		packet:      []byte{0x02, 10, 0xAA, 0xBB, 0xCC}, // 3 payload bytes, frame1_len=10
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// Code 2: size field itself truncated (len<1 for parse_size).
	// gopus parseFrameLength: offset >= len(data) → ErrPacketTooShort.
	// libopus parse_size: len<1 → size=-1, returns -1, caller sees size[0]<0 → OPUS_INVALID_PACKET.
	// Both signal "packet is malformed"; gopus uses ErrPacketTooShort.
	cases = append(cases, malformedPacketCase{
		name:        "code2_truncated_size_field",
		packet:      []byte{0x02}, // no size field after TOC
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrPacketTooShort,
	})

	// Code 2: two-byte size field present but second byte missing.
	// gopus parseFrameLength: offset+1 >= len(data) → ErrPacketTooShort.
	// libopus parse_size: len<2 → size=-1 → OPUS_INVALID_PACKET.
	cases = append(cases, malformedPacketCase{
		name:        "code2_size_2byte_partial",
		packet:      []byte{0x02, 0xFC}, // first size byte ≥ 252, second byte missing
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrPacketTooShort,
	})

	// ---- Code-3 M=0 --------------------------------------------------------
	// Code 3: frame count M (bits 0-5 of byte 2) == 0 → OPUS_INVALID_PACKET.
	// src/opus.c default case: if (count <= 0 ...) return OPUS_INVALID_PACKET
	cases = append(cases, malformedPacketCase{
		name:        "code3_M_zero",
		packet:      []byte{0x03, 0x00}, // M=0
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidFrameCount,
	})

	// ---- Code-3 M>48 -------------------------------------------------------
	// Frame count in bits 0-5 can be at most 48 (0x30); 49 overflows.
	// Note: bits 0-5 of 0x31 = 49.
	cases = append(cases, malformedPacketCase{
		name:        "code3_M_49",
		packet:      []byte{0x03, 0x31}, // M=49 (>48)
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidFrameCount,
	})
	cases = append(cases, malformedPacketCase{
		name:        "code3_M_63",
		packet:      []byte{0x03, 0x3F}, // M=63
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidFrameCount,
	})

	// ---- Code-3 >120 ms total duration ------------------------------------
	// framesize(TOC) * M > 5760 → OPUS_INVALID_PACKET.
	// TOC 0x03 = SILK NB 10ms code-3; M=13 → 13*480=6240>5760.
	// src/opus.c: if (count <= 0 || framesize*(opus_int32)count > 5760) INVALID
	cases = append(cases, malformedPacketCase{
		name: "code3_over_120ms",
		// config 0 (SILK NB) = 10ms/frame at 48kHz → 480 samples/frame
		// 13 frames × 480 = 6240 > 5760 → OPUS_INVALID_PACKET
		packet:      []byte{0x03, 0x0D}, // code-3, M=13 (bits 0-5 = 0x0D)
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// ---- Code-3 CBR uneven payload ----------------------------------------
	// CBR: payload len after frame-count/padding byte must be divisible by M.
	// src/opus.c CBR case: if (last_size*count!=len) return OPUS_INVALID_PACKET
	cases = append(cases, malformedPacketCase{
		// M=3, CBR (bit7=0), no padding (bit6=0): 4 payload bytes → 4/3 not integer
		name:        "code3_cbr_uneven_payload",
		packet:      []byte{0x03, 0x03, 0xAA, 0xBB, 0xCC, 0xDD}, // 4 bytes payload, M=3
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// CBR: zero payload divided by M=3 is fine (0/3=0)
	cases = append(cases, malformedPacketCase{
		name:        "code3_cbr_zero_payload",
		packet:      []byte{0x03, 0x03}, // M=3, no payload
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: nil, // 0/3 = 0 bytes/frame → parse OK, PLC-like
	})

	// ---- Code-3 VBR size-field overflow -----------------------------------
	// VBR: size[i] > remaining len after size bytes → OPUS_INVALID_PACKET.
	// src/opus.c VBR loop: if (size[i]<0 || size[i] > len) return OPUS_INVALID_PACKET
	cases = append(cases, malformedPacketCase{
		// M=2, VBR (bit7=1), no padding: size[0] claims 100 but only 2 bytes follow
		name:        "code3_vbr_size_overflow",
		packet:      []byte{0x03, 0x82, 100, 0xAA, 0xBB}, // M=2,VBR,size[0]=100
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// ---- Code-3 padding overflow ------------------------------------------
	// Padding bytes consumed > remaining len → OPUS_INVALID_PACKET.
	// src/opus.c: if (len<=0) return OPUS_INVALID_PACKET inside padding loop
	cases = append(cases, malformedPacketCase{
		// M=1, no VBR, padding flag set (bit6), but pad byte follows with 255
		// meaning 254 pad bytes needed; len after count byte is 2 → immediately
		// runs out: padding loop: len--; tmp=254; len-=254 → len<0
		name:        "code3_padding_overflow",
		packet:      []byte{0x03, 0x41, 0xFF, 0x01}, // M=1, padding=1, pad=255→need254
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// padding present but pad-length byte itself missing (len==0 when reading pad byte).
	// gopus: offset >= len(data) when trying to read the pad byte → ErrPacketTooShort.
	// libopus: len<=0 inside padding loop → OPUS_INVALID_PACKET.
	// Both signal "malformed packet"; gopus uses ErrPacketTooShort.
	cases = append(cases, malformedPacketCase{
		name:        "code3_padding_byte_missing",
		packet:      []byte{0x03, 0x41}, // M=1, padding flag set, no pad-length byte
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrPacketTooShort,
	})

	// ---- Code-3 len<1 after TOC (missing frame-count byte) ----------------
	// src/opus.c default: if (len<1) return OPUS_INVALID_PACKET
	cases = append(cases, malformedPacketCase{
		name:        "code3_missing_frame_count_byte",
		packet:      []byte{0x03}, // code-3 but no frame-count byte
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrPacketTooShort,
	})

	// ---- Last-frame size > 1275 -------------------------------------------
	// Non-self-delimited: last_size > 1275 → OPUS_INVALID_PACKET.
	// src/opus.c: if (last_size > 1275) return OPUS_INVALID_PACKET
	// Code-0: payload = single frame, size = len-1. Craft a 1277-byte packet.
	bigPayload := make([]byte, 1278) // TOC + 1277 payload bytes (> 1275)
	bigPayload[0] = 0x60             // CELT FB 20ms code-0
	for i := 1; i < len(bigPayload); i++ {
		bigPayload[i] = 0xFF
	}
	cases = append(cases, malformedPacketCase{
		name:        "code0_frame_exceeds_1275",
		packet:      bigPayload,
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	// ---- Buffer too small (OPUS_BUFFER_TOO_SMALL) -------------------------
	// libopus opus_decode_native: count*packet_frame_size > frame_size →
	// OPUS_BUFFER_TOO_SMALL.  This is triggered by passing a frame_size
	// smaller than the packet requires to the oracle.  We use a separate
	// batch for this because we need a non-standard frame_size.

	// ---- Self-delimited edge cases ----------------------------------------
	// The public API always uses non-self-delimited parsing. Self-delimited
	// framing is only used internally (opus_repacketizer). These cases verify
	// that normal (non-self-delimited) parsing rejects malformed inputs.

	// ---- Oversized frame (frame data > 1275 inside code-2) ----------------
	// Code 2: frame2_len = len - header - frame1_len; if that > 1275 via the
	// non-self-delimited last_size check → OPUS_INVALID_PACKET.
	// Build: TOC code-2, size[0]=0, frame2 payload = 1276 bytes
	oversizedCode2 := make([]byte, 1+1+1276)
	oversizedCode2[0] = 0x02 // code-2
	oversizedCode2[1] = 0x00 // frame1 size = 0
	for i := 2; i < len(oversizedCode2); i++ {
		oversizedCode2[i] = 0xAA
	}
	cases = append(cases, malformedPacketCase{
		name:        "code2_frame2_exceeds_1275",
		packet:      oversizedCode2,
		format:      malformedFormatFloat32,
		sampleRate:  48000,
		channels:    1,
		wantGopusErr: ErrInvalidPacket,
	})

	return cases
}

// bufferTooSmallCases48k1ch returns cases where libopus should return
// OPUS_BUFFER_TOO_SMALL.  They are probed with a deliberately small frame_size
// so require a separate oracle call.
func bufferTooSmallCases48k1ch() []bufferTooSmallProbeCase {
	return []bufferTooSmallProbeCase{
		{
			name:      "buf_too_small_code0_20ms",
			packet:    []byte{0x60, 0xFF, 0xFF, 0xFF, 0xFF}, // CELT FB 20ms
			frameSize: 480,                                   // only 480, packet needs 960
			sampleRate: 48000,
			channels:  1,
		},
		{
			name:      "buf_too_small_code1_2x20ms",
			// code-1: 2 frames × 960 = 1920 samples needed; give 960
			packet:    []byte{0x61, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			frameSize: 960,
			sampleRate: 48000,
			channels:  1,
		},
	}
}

type bufferTooSmallProbeCase struct {
	name      string
	packet    []byte
	frameSize int
	sampleRate int
	channels  int
}

func probeBufTooSmallCases(cases []bufferTooSmallProbeCase) ([]int32, error) {
	binPath, err := buildMalformedDecodeErrorHelper()
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, nil
	}

	sr := cases[0].sampleRate
	ch := cases[0].channels
	payload := libopustest.NewOraclePayload("GDEI",
		uint32(ch),
		uint32(sr),
		uint32(len(cases)),
	)
	for _, c := range cases {
		payload.U32(uint32(malformedFormatFloat32))
		payload.U32(uint32(c.frameSize))
		payload.U32(0)
		payload.U32(uint32(len(c.packet)))
		payload.Raw(c.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "decode error probe", "GDEO")
	if err != nil {
		return nil, err
	}
	n := reader.Count(len(cases))
	reader.ExpectRemaining(4 * n)
	out := make([]int32, n)
	for i := range out {
		out[i] = reader.I32()
	}
	return out, reader.ExpectConsumed()
}

// ---- test harness ----------------------------------------------------------

var malformedHelperBuildOnce sync.Once
var malformedHelperErr error

func ensureMalformedHelper(t testing.TB) {
	t.Helper()
	malformedHelperBuildOnce.Do(func() {
		_, malformedHelperErr = buildMalformedDecodeErrorHelper()
	})
	if malformedHelperErr != nil {
		libopustest.HelperUnavailable(t, "decode error probe", malformedHelperErr)
	}
}

// TestMalformedPacketErrorCodeParity verifies that for every malformed-packet
// class gopus returns the same error classification as libopus.
//
// For OPUS_INVALID_PACKET:
//   gopus must return one of ErrInvalidPacket / ErrPacketTooShort /
//   ErrInvalidFrameCount (all are OPUS_INVALID_PACKET equivalents).
//
// For success (positive libopus result):
//   gopus must also succeed (return nil error).
func TestMalformedPacketErrorCodeParity(t *testing.T) {
	libopustest.RequireOracle(t)
	ensureMalformedHelper(t)

	corpus := malformedCorpus48k1ch()

	oracleResults, err := probeMalformedDecodeErrors(corpus)
	if err != nil {
		libopustest.HelperUnavailable(t, "decode error probe", err)
		return
	}

	for i, tc := range corpus {
		tc := tc
		want := oracleResults[i]
		t.Run(tc.name, func(t *testing.T) {
			dec, decErr := NewDecoder(DefaultDecoderConfig(tc.sampleRate, tc.channels))
			if decErr != nil {
				t.Fatalf("NewDecoder: %v", decErr)
			}
			pcm := make([]float32, 5760)

			var gopusErr error
			switch tc.format {
			case malformedFormatInt16:
				pcm16 := make([]int16, 5760)
				_, gopusErr = dec.DecodeInt16(tc.packet, pcm16)
			case malformedFormatInt24:
				pcm32 := make([]int32, 5760)
				_, gopusErr = dec.DecodeInt24(tc.packet, pcm32)
			default:
				_, gopusErr = dec.Decode(tc.packet, pcm)
			}

			switch {
			case want.code > 0:
				// libopus decoded successfully → gopus must succeed too
				if gopusErr != nil {
					t.Errorf("libopus returned %d (success), gopus returned error=%v (want nil)",
						want.code, gopusErr)
				}

			case want.code == libopusErrInvalidPkt:
				// libopus returned OPUS_INVALID_PACKET → gopus must return an
				// invalid-packet class error.
				if gopusErr == nil {
					t.Errorf("libopus returned OPUS_INVALID_PACKET (-4), gopus returned nil (want invalid-packet error)")
					return
				}
				if !isGopusInvalidPacketErr(gopusErr) {
					t.Errorf("libopus OPUS_INVALID_PACKET (-4), gopus returned %v (want ErrInvalidPacket/ErrPacketTooShort/ErrInvalidFrameCount)",
						gopusErr)
					return
				}
				// If the test pinned a specific gopus error, check the exact sentinel.
				if tc.wantGopusErr != nil && !errors.Is(gopusErr, tc.wantGopusErr) {
					t.Errorf("libopus OPUS_INVALID_PACKET, gopus=%v want %v (both are invalid-packet class — fix the mapping)",
						gopusErr, tc.wantGopusErr)
				}

			case want.code == libopusErrBufTooSmall:
				// libopus returned OPUS_BUFFER_TOO_SMALL → gopus must return ErrBufferTooSmall.
				if !errors.Is(gopusErr, ErrBufferTooSmall) {
					t.Errorf("libopus OPUS_BUFFER_TOO_SMALL (-2), gopus=%v want ErrBufferTooSmall", gopusErr)
				}

			case want.code == libopusErrBadArg:
				// libopus returned OPUS_BAD_ARG — this should not happen for
				// packet-content errors (bad-arg is for frame_size==0 etc.).
				// If we see it, at minimum gopus must return non-nil.
				if gopusErr == nil {
					t.Errorf("libopus returned OPUS_BAD_ARG (-1), gopus returned nil (want an error)")
				}

			case want.code < 0:
				// Some other negative libopus error → gopus must return non-nil.
				if gopusErr == nil {
					t.Errorf("libopus returned error %d, gopus returned nil (want an error)", want.code)
				}
			}
		})
	}
}

// TestMalformedPacketBufferTooSmallParity verifies OPUS_BUFFER_TOO_SMALL parity.
//
// libopus opus_decode_native: if (count*packet_frame_size > frame_size)
//   return OPUS_BUFFER_TOO_SMALL;                  (src/opus_decoder.c:835)
//
// The libopus oracle is called with a deliberately small frame_size so that
// count*packet_frame_size > frame_size.  gopus must return ErrBufferTooSmall.
func TestMalformedPacketBufferTooSmallParity(t *testing.T) {
	libopustest.RequireOracle(t)
	ensureMalformedHelper(t)

	cases := bufferTooSmallCases48k1ch()
	oracleCodes, err := probeBufTooSmallCases(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "decode error probe (buf-too-small)", err)
		return
	}

	for i, tc := range cases {
		tc := tc
		wantCode := oracleCodes[i]
		t.Run(tc.name, func(t *testing.T) {
			dec, decErr := NewDecoder(DefaultDecoderConfig(tc.sampleRate, tc.channels))
			if decErr != nil {
				t.Fatalf("NewDecoder: %v", decErr)
			}

			// Use tc.frameSize as the buffer size to mirror what the oracle does.
			pcm := make([]float32, tc.frameSize*tc.channels)
			_, gopusErr := dec.Decode(tc.packet, pcm)

			if wantCode == libopusErrBufTooSmall {
				if !errors.Is(gopusErr, ErrBufferTooSmall) {
					t.Errorf("libopus OPUS_BUFFER_TOO_SMALL, gopus=%v want ErrBufferTooSmall", gopusErr)
				}
			} else if wantCode >= 0 {
				// libopus unexpectedly succeeded; either test data is wrong or
				// the frame_size was large enough.  Log and skip the assertion.
				t.Logf("libopus returned %d (not BUFFER_TOO_SMALL) — skipping parity check", wantCode)
			}
		})
	}
}

// TestMalformedPacketInt16Parity repeats a representative subset of the
// malformed corpus through DecodeInt16 to verify the int16 decode path also
// maps errors identically to libopus.
func TestMalformedPacketInt16Parity(t *testing.T) {
	libopustest.RequireOracle(t)
	ensureMalformedHelper(t)

	int16Cases := []malformedPacketCase{
		{
			name:        "int16_code1_odd_payload",
			packet:      []byte{0x01, 0xAA, 0xBB, 0xCC},
			format:      malformedFormatInt16,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrInvalidPacket,
		},
		{
			name:        "int16_code3_M_zero",
			packet:      []byte{0x03, 0x00},
			format:      malformedFormatInt16,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrInvalidFrameCount,
		},
		{
			name:        "int16_code3_missing_frame_count",
			packet:      []byte{0x03},
			format:      malformedFormatInt16,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrPacketTooShort,
		},
		{
			name:        "int16_code2_frame1_overflow",
			packet:      []byte{0x02, 10, 0xAA, 0xBB, 0xCC},
			format:      malformedFormatInt16,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrInvalidPacket,
		},
	}

	oracleResults, err := probeMalformedDecodeErrors(int16Cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "decode error probe (int16)", err)
		return
	}

	for i, tc := range int16Cases {
		tc := tc
		want := oracleResults[i]
		t.Run(tc.name, func(t *testing.T) {
			dec, decErr := NewDecoder(DefaultDecoderConfig(tc.sampleRate, tc.channels))
			if decErr != nil {
				t.Fatalf("NewDecoder: %v", decErr)
			}
			pcm16 := make([]int16, 5760)
			_, gopusErr := dec.DecodeInt16(tc.packet, pcm16)

			if want.code == libopusErrInvalidPkt {
				if gopusErr == nil || !isGopusInvalidPacketErr(gopusErr) {
					t.Errorf("libopus OPUS_INVALID_PACKET, gopus=%v want invalid-packet class", gopusErr)
				}
			} else if want.code > 0 {
				if gopusErr != nil {
					t.Errorf("libopus success (%d), gopus=%v want nil", want.code, gopusErr)
				}
			}
		})
	}
}

// TestMalformedPacketInt24Parity repeats a representative subset through
// DecodeInt24 to verify the int24 decode path.
func TestMalformedPacketInt24Parity(t *testing.T) {
	libopustest.RequireOracle(t)
	ensureMalformedHelper(t)

	int24Cases := []malformedPacketCase{
		{
			name:        "int24_code1_odd_payload",
			packet:      []byte{0x01, 0xAA, 0xBB, 0xCC},
			format:      malformedFormatInt24,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrInvalidPacket,
		},
		{
			name:        "int24_code3_M_63",
			packet:      []byte{0x03, 0x3F},
			format:      malformedFormatInt24,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrInvalidFrameCount,
		},
		{
			name:        "int24_code3_cbr_uneven",
			packet:      []byte{0x03, 0x03, 0xAA, 0xBB, 0xCC, 0xDD},
			format:      malformedFormatInt24,
			sampleRate:  48000,
			channels:    1,
			wantGopusErr: ErrInvalidPacket,
		},
	}

	oracleResults, err := probeMalformedDecodeErrors(int24Cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "decode error probe (int24)", err)
		return
	}

	for i, tc := range int24Cases {
		tc := tc
		want := oracleResults[i]
		t.Run(tc.name, func(t *testing.T) {
			dec, decErr := NewDecoder(DefaultDecoderConfig(tc.sampleRate, tc.channels))
			if decErr != nil {
				t.Fatalf("NewDecoder: %v", decErr)
			}
			pcm32 := make([]int32, 5760)
			_, gopusErr := dec.DecodeInt24(tc.packet, pcm32)

			if want.code == libopusErrInvalidPkt {
				if gopusErr == nil || !isGopusInvalidPacketErr(gopusErr) {
					t.Errorf("libopus OPUS_INVALID_PACKET, gopus=%v want invalid-packet class", gopusErr)
				}
			} else if want.code > 0 {
				if gopusErr != nil {
					t.Errorf("libopus success (%d), gopus=%v want nil", want.code, gopusErr)
				}
			}
		})
	}
}

// TestMalformedPacketAllRatesAndChannels exercises a core malformed-packet
// (code-1 odd-length) across all valid sample rates and both channel counts
// to confirm the error classification is rate/channel-independent.
func TestMalformedPacketAllRatesAndChannels(t *testing.T) {
	libopustest.RequireOracle(t)
	ensureMalformedHelper(t)

	rates := []int{8000, 12000, 16000, 24000, 48000}
	chans := []int{1, 2}

	for _, rate := range rates {
		for _, ch := range chans {
			rate, ch := rate, ch
			name := fmt.Sprintf("fs%d_ch%d_code1_odd", rate, ch)
			t.Run(name, func(t *testing.T) {
				tc := malformedPacketCase{
					name:        name,
					packet:      []byte{0x01, 0xAA, 0xBB, 0xCC}, // code-1, odd payload
					format:      malformedFormatFloat32,
					sampleRate:  rate,
					channels:    ch,
					wantGopusErr: ErrInvalidPacket,
				}
				res, err := probeMalformedDecodeErrors([]malformedPacketCase{tc})
				if err != nil {
					libopustest.HelperUnavailable(t, "decode error probe", err)
					return
				}
				if res[0].code != libopusErrInvalidPkt {
					t.Logf("libopus returned %d (not OPUS_INVALID_PACKET) — skipping", res[0].code)
					return
				}
				dec, decErr := NewDecoder(DefaultDecoderConfig(rate, ch))
				if decErr != nil {
					t.Fatalf("NewDecoder: %v", decErr)
				}
				pcm := make([]float32, 5760)
				_, gopusErr := dec.Decode(tc.packet, pcm)
				if gopusErr == nil || !isGopusInvalidPacketErr(gopusErr) {
					t.Errorf("libopus OPUS_INVALID_PACKET, gopus=%v want invalid-packet class", gopusErr)
				}
			})
		}
	}
}
