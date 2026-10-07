package gopus

// packet_repacketizer_libopus_parity_test.go — byte-exact differential test of
// the repacketizer (Cat/Out/OutRange) and opus_packet_pad / opus_packet_unpad
// against the libopus C oracle.
//
// Coverage:
//   - single-packet pass-through for code 0/1/2/3 (CBR and VBR), all framing
//     codes re-emitted byte-for-byte
//   - multi-packet merges A+B, A+B+C producing code 1/2/3 outputs
//   - sub-range extraction via OutRange(begin,end)
//   - CBR vs VBR re-framing decisions (equal vs unequal frame sizes)
//   - opus_packet_pad to a target length and opus_packet_unpad round-trip
//   - buffer-too-small rejection parity
//   - TOC-mismatch and over-duration rejection parity

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type repacketizerOracleCase struct {
	name      string
	packets   [][]byte
	begin     int
	end       int // 0 == all frames
	maxlen    int
	padNewLen int // 0 == skip pad/unpad
}

type repacketizerOracleResult struct {
	catRet     int32
	nbFrames   int32
	outRet     int32
	outBytes   []byte
	padRet     int32
	padBytes   []byte
	unpadRet   int32
	unpadBytes []byte
}

const repacketizerSkipped = 0x7fffffff

var repacketizerParityHelper libopustest.HelperCache

func getRepacketizerHelperPath() (string, error) {
	return repacketizerParityHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "repacketizer",
		OutputBase:  "gopus_libopus_repacketizer",
		SourceFile:  "libopus_repacketizer_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"src", "celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusRepacketizer(cases []repacketizerOracleCase) ([]repacketizerOracleResult, error) {
	binPath, err := getRepacketizerHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayload("GRPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packets)))
		for _, p := range tc.packets {
			payload.U32(uint32(len(p)))
			payload.Raw(p)
		}
		payload.U32(uint32(tc.begin))
		payload.U32(uint32(tc.end))
		payload.U32(uint32(tc.maxlen))
		payload.U32(uint32(tc.padNewLen))
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "repacketizer", "GRPO")
	if err != nil {
		return nil, err
	}
	reader.Count(len(cases))
	if reader.Err() != nil {
		return nil, reader.Err()
	}

	out := make([]repacketizerOracleResult, len(cases))
	for i := range out {
		r := &out[i]
		r.catRet = reader.I32()
		r.nbFrames = reader.I32()
		r.outRet = reader.I32()
		outLen := reader.I32()
		if outLen > 0 {
			r.outBytes = append([]byte(nil), reader.Bytes(int(outLen))...)
		}
		r.padRet = reader.I32()
		padLen := reader.I32()
		if padLen > 0 {
			r.padBytes = append([]byte(nil), reader.Bytes(int(padLen))...)
		}
		r.unpadRet = reader.I32()
		unpadLen := reader.I32()
		if unpadLen > 0 {
			r.unpadBytes = append([]byte(nil), reader.Bytes(int(unpadLen))...)
		}
		if reader.Err() != nil {
			return nil, reader.Err()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// runRepacketizerGopus replays a case through the gopus repacketizer + pad/unpad
// and returns results in the same shape as the oracle output.
func runRepacketizerGopus(tc repacketizerOracleCase) repacketizerOracleResult {
	var res repacketizerOracleResult
	rp := NewRepacketizer()
	catOK := true
	for _, p := range tc.packets {
		if err := rp.Cat(p); err != nil {
			res.catRet = -4 // any negative; oracle uses OPUS_INVALID_PACKET (-4)
			catOK = false
			break
		}
	}
	if !catOK {
		res.outRet = res.catRet
		return res
	}
	res.catRet = 0
	res.nbFrames = int32(rp.NumFrames())

	end := tc.end
	if end == 0 {
		end = rp.NumFrames()
	}
	buf := make([]byte, tc.maxlen)
	n, err := rp.OutRange(tc.begin, end, buf)
	if err != nil {
		res.outRet = -1 // negative sentinel
	} else {
		res.outRet = int32(n)
		res.outBytes = append([]byte(nil), buf[:n]...)
	}

	if tc.padNewLen > 0 && len(tc.packets) > 0 && len(tc.packets[0]) > 0 {
		src := tc.packets[0]
		padBuf := make([]byte, tc.padNewLen)
		copy(padBuf, src)
		if err := PacketPad(padBuf, len(src), tc.padNewLen); err != nil {
			res.padRet = -1
		} else {
			res.padRet = 0
			res.padBytes = append([]byte(nil), padBuf...)
			unpadBuf := make([]byte, tc.padNewLen)
			copy(unpadBuf, padBuf)
			ul, uerr := PacketUnpad(unpadBuf, tc.padNewLen)
			if uerr != nil {
				res.unpadRet = -1
			} else {
				res.unpadRet = int32(ul)
				res.unpadBytes = append([]byte(nil), unpadBuf[:ul]...)
			}
		}
	} else {
		res.padRet = repacketizerSkipped
		res.unpadRet = repacketizerSkipped
	}
	return res
}

func TestRepacketizerByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := repacketizerOracleCases()
	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}

	for i, tc := range cases {
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			got := runRepacketizerGopus(tc)

			// cat accept/reject parity
			libCatOK := w.catRet == 0
			gopCatOK := got.catRet == 0
			if libCatOK != gopCatOK {
				t.Fatalf("cat parity: gopus ok=%v (ret=%d) libopus ok=%v (ret=%d)",
					gopCatOK, got.catRet, libCatOK, w.catRet)
			}
			if !libCatOK {
				return
			}

			if got.nbFrames != w.nbFrames {
				t.Errorf("nb_frames: got %d want %d", got.nbFrames, w.nbFrames)
			}

			// out_range accept/reject parity (positive vs negative)
			libOutOK := w.outRet > 0
			gopOutOK := got.outRet > 0
			if libOutOK != gopOutOK {
				t.Fatalf("out_range parity: gopus ret=%d libopus ret=%d (bufmismatch?)", got.outRet, w.outRet)
			}
			if libOutOK {
				if got.outRet != w.outRet {
					t.Errorf("out_range length: got %d want %d", got.outRet, w.outRet)
				}
				if hex.EncodeToString(got.outBytes) != hex.EncodeToString(w.outBytes) {
					t.Errorf("out_range bytes:\n got=%s\nwant=%s",
						hex.EncodeToString(got.outBytes), hex.EncodeToString(w.outBytes))
				}
			}

			// pad / unpad parity
			if w.padRet != repacketizerSkipped {
				libPadOK := w.padRet == 0
				gopPadOK := got.padRet == 0
				if libPadOK != gopPadOK {
					t.Fatalf("pad parity: gopus ret=%d libopus ret=%d", got.padRet, w.padRet)
				}
				if libPadOK {
					if hex.EncodeToString(got.padBytes) != hex.EncodeToString(w.padBytes) {
						t.Errorf("pad bytes:\n got=%s\nwant=%s",
							hex.EncodeToString(got.padBytes), hex.EncodeToString(w.padBytes))
					}
					libUnpadOK := w.unpadRet > 0
					gopUnpadOK := got.unpadRet > 0
					if libUnpadOK != gopUnpadOK {
						t.Fatalf("unpad parity: gopus ret=%d libopus ret=%d", got.unpadRet, w.unpadRet)
					}
					if libUnpadOK {
						if got.unpadRet != w.unpadRet {
							t.Errorf("unpad length: got %d want %d", got.unpadRet, w.unpadRet)
						}
						if hex.EncodeToString(got.unpadBytes) != hex.EncodeToString(w.unpadBytes) {
							t.Errorf("unpad bytes:\n got=%s\nwant=%s",
								hex.EncodeToString(got.unpadBytes), hex.EncodeToString(w.unpadBytes))
						}
					}
				}
			}
		})
	}
}

func TestPacketUnpadMalformedExtensionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := mustDecodeHex(t, "4b4102112233ffff")
	tc := repacketizerOracleCase{
		name:      "unpad_opaque_malformed_extension",
		packets:   [][]byte{packet},
		begin:     0,
		end:       1,
		maxlen:    64,
		padNewLen: len(packet), // PacketPad is a no-op, so the oracle unpads the input directly.
	}
	want, err := probeLibopusRepacketizer([]repacketizerOracleCase{tc})
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}
	got := runRepacketizerGopus(tc)
	if want[0].unpadRet != int32(len([]byte{0x48, 0x11, 0x22, 0x33})) {
		t.Fatalf("libopus PacketUnpad ret=%d, want 4", want[0].unpadRet)
	}
	if got.unpadRet != want[0].unpadRet || !bytes.Equal(got.unpadBytes, want[0].unpadBytes) {
		t.Fatalf("PacketUnpad=%x (ret=%d), libopus=%x (ret=%d)", got.unpadBytes, got.unpadRet, want[0].unpadBytes, want[0].unpadRet)
	}
}

func TestMalformedPacketExtensionErrorsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	malformed := mustDecodeHex(t, "4b4102112233ffff")
	valid := []byte{0x48, 0x44}
	cases := []repacketizerOracleCase{
		{
			name:      "extension_out_range_adequate_buffer",
			packets:   [][]byte{malformed},
			begin:     0,
			end:       1,
			maxlen:    64,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_short_buffer",
			packets:   [][]byte{malformed},
			begin:     0,
			end:       1,
			maxlen:    1,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_invalid_range",
			packets:   [][]byte{malformed},
			begin:     1,
			end:       1,
			maxlen:    64,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_skips_malformed_frame",
			packets:   [][]byte{malformed, valid},
			begin:     1,
			end:       2,
			maxlen:    64,
			padNewLen: 16,
		},
	}
	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}
	for i, tc := range cases {
		if want[i].catRet != 0 {
			t.Fatalf("libopus %s cat ret=%d, want 0", tc.name, want[i].catRet)
		}
		if want[i].padRet != -3 {
			t.Errorf("libopus %s PacketPad ret=%d, want OPUS_INTERNAL_ERROR (-3)", tc.name, want[i].padRet)
		}
	}
	if want[0].outRet != -3 || want[1].outRet != -3 {
		t.Fatalf("libopus OutRange malformed extension: adequate=%d short=%d, want -3 for both", want[0].outRet, want[1].outRet)
	}
	if want[2].outRet != -1 {
		t.Fatalf("libopus OutRange invalid range ret=%d, want OPUS_BAD_ARG (-1)", want[2].outRet)
	}
	if want[3].outRet != int32(len(valid)) || !bytes.Equal(want[3].outBytes, valid) {
		t.Fatalf("libopus OutRange after malformed frame: ret=%d bytes=%x, want %x", want[3].outRet, want[3].outBytes, valid)
	}

	if _, err := parsePacketExtensionList([]byte{0xff, 0xff}, 1); err != ErrInvalidPacket {
		t.Fatalf("parsePacketExtensionList malformed data err=%v, want ErrInvalidPacket", err)
	}

	rp := NewRepacketizer()
	if err := rp.Cat(malformed); err != nil {
		t.Fatalf("Cat malformed-extension packet: %v", err)
	}
	if got := rp.NumFrames(); got != 1 {
		t.Fatalf("NumFrames after Cat=%d, want 1", got)
	}

	for _, tc := range []struct {
		name      string
		begin     int
		end       int
		outLen    int
		wantError error
	}{
		{name: "adequate output", begin: 0, end: 1, outLen: 64, wantError: ErrInternalError},
		{name: "short output", begin: 0, end: 1, outLen: 1, wantError: ErrInternalError},
		{name: "invalid range", begin: 1, end: 1, outLen: 64, wantError: ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := bytes.Repeat([]byte{0xa5}, tc.outLen)
			before := append([]byte(nil), out...)
			n, err := rp.OutRange(tc.begin, tc.end, out)
			if n != 0 || err != tc.wantError {
				t.Fatalf("OutRange(%d,%d)=(%d,%v), want (0,%v)", tc.begin, tc.end, n, err, tc.wantError)
			}
			if !bytes.Equal(out, before) {
				t.Fatalf("OutRange(%d,%d) changed output on error: got %x want %x", tc.begin, tc.end, out, before)
			}
			if got := rp.NumFrames(); got != 1 {
				t.Fatalf("OutRange(%d,%d) changed NumFrames to %d, want 1", tc.begin, tc.end, got)
			}
		})
	}

	pad := bytes.Repeat([]byte{0xa5}, 16)
	copy(pad, malformed)
	padBefore := append([]byte(nil), pad...)
	if err := PacketPad(pad, len(malformed), len(pad)); err != ErrInternalError {
		t.Fatalf("PacketPad malformed extension err=%v, want ErrInternalError", err)
	}
	if !bytes.Equal(pad, padBefore) {
		t.Fatalf("PacketPad changed output on error: got %x want %x", pad, padBefore)
	}

	if err := rp.Cat(valid); err != nil {
		t.Fatalf("Cat valid packet after extension errors: %v", err)
	}
	out := make([]byte, 64)
	n, err := rp.OutRange(1, 2, out)
	if err != nil {
		t.Fatalf("OutRange valid frame after malformed frame: %v", err)
	}
	if !bytes.Equal(out[:n], want[3].outBytes) {
		t.Fatalf("OutRange valid continuation=%x, want libopus %x", out[:n], want[3].outBytes)
	}
}

// ── case builders ────────────────────────────────────────────────────────────

// mkPayload returns n deterministic non-zero bytes.
func mkPayload(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + seed*31 + 1) & 0xFF)
	}
	return b
}

// code0Packet builds a single-frame (code 0) packet of the given config/stereo.
func code0Packet(config uint8, stereo bool, payloadLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 0)}
	return append(p, mkPayload(payloadLen, seed)...)
}

// code1Packet builds two equal frames (code 1).
func code1Packet(config uint8, stereo bool, frameLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 1)}
	p = append(p, mkPayload(frameLen, seed)...)
	p = append(p, mkPayload(frameLen, seed+1)...)
	return p
}

// code2Packet builds two unequal frames (code 2).
func code2Packet(config uint8, stereo bool, len0, len1, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 2)}
	szBuf := make([]byte, 2)
	n := encodeFrameLength(szBuf, len0)
	p = append(p, szBuf[:n]...)
	p = append(p, mkPayload(len0, seed)...)
	p = append(p, mkPayload(len1, seed+1)...)
	return p
}

// code3CBRPacket builds a code-3 CBR packet with m equal frames of frameLen.
func code3CBRPacket(config uint8, stereo bool, m, frameLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 3), byte(m & 0x3F)}
	for i := range m {
		p = append(p, mkPayload(frameLen, seed+i)...)
	}
	return p
}

// code3VBRPacket builds a code-3 VBR packet with frame sizes given.
func code3VBRPacket(config uint8, stereo bool, sizes []int, seed int) []byte {
	m := len(sizes)
	p := []byte{GenerateTOC(config, stereo, 3), byte(0x80 | (m & 0x3F))}
	for i := 0; i < m-1; i++ {
		szBuf := make([]byte, 2)
		n := encodeFrameLength(szBuf, sizes[i])
		p = append(p, szBuf[:n]...)
	}
	for i := range m {
		p = append(p, mkPayload(sizes[i], seed+i)...)
	}
	return p
}

func repacketizerOraclecasesAppendSingle(cases []repacketizerOracleCase) []repacketizerOracleCase {
	// Single-packet round-trips across all framing codes and several configs.
	configs := []uint8{0, 1, 8, 11, 12, 14, 16, 18, 20, 28, 30, 31}
	for _, cfg := range configs {
		for _, stereo := range []bool{false, true} {
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases,
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code0_cfg%d_%s", cfg, s),
					packets:   [][]byte{code0Packet(cfg, stereo, 40, 1)},
					maxlen:    512,
					padNewLen: 60,
				},
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code1_cfg%d_%s", cfg, s),
					packets:   [][]byte{code1Packet(cfg, stereo, 20, 2)},
					maxlen:    512,
					padNewLen: 80,
				},
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code2_cfg%d_%s", cfg, s),
					packets:   [][]byte{code2Packet(cfg, stereo, 15, 33, 3)},
					maxlen:    512,
					padNewLen: 100,
				},
			)
		}
	}
	return cases
}

func repacketizerOracleCases() []repacketizerOracleCase {
	var cases []repacketizerOracleCase
	cases = repacketizerOraclecasesAppendSingle(cases)
	largeExtensionPacket := packetWithLargeLongExtension(4096)

	// CELT 10ms config (18) supports up to 12 frames in 120ms (480 samples each).
	// SILK 20ms config (1) supports up to 6 frames. Use small frame sizes.

	// Merge two code-0 packets -> code 1 (equal) or code 2 (unequal).
	cases = append(cases,
		repacketizerOracleCase{
			name:    "merge_two_code0_equal",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 30, 2)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "merge_two_code0_unequal",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 41, 2)},
			maxlen:  512, padNewLen: 0,
		},
		// large frame (>=252) forces 2-byte size encoding in code 2 / VBR
		repacketizerOracleCase{
			name:    "merge_two_code0_largefirst",
			packets: [][]byte{code0Packet(18, false, 300, 1), code0Packet(18, false, 41, 2)},
			maxlen:  1024, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "merge_two_code0_stereo",
			packets: [][]byte{code0Packet(31, true, 50, 1), code0Packet(31, true, 60, 2)},
			maxlen:  512, padNewLen: 0,
		},
	)

	// Merge three code-0 -> code 3.
	cases = append(cases,
		repacketizerOracleCase{
			name: "merge_three_code0_equal",
			packets: [][]byte{
				code0Packet(18, false, 25, 1),
				code0Packet(18, false, 25, 2),
				code0Packet(18, false, 25, 3),
			},
			maxlen: 512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name: "merge_three_code0_vbr",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			maxlen: 512, padNewLen: 0,
		},
	)

	// OutRange sub-range over a 3-frame accumulation.
	cases = append(cases,
		repacketizerOracleCase{
			name: "outrange_1_3_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 1, end: 3, maxlen: 512,
		},
		repacketizerOracleCase{
			name: "outrange_0_1_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 0, end: 1, maxlen: 512,
		},
		repacketizerOracleCase{
			name: "outrange_1_2_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 1, end: 2, maxlen: 512,
		},
	)

	// Feed multi-frame packets directly (code1/2/3) then re-emit.
	cases = append(cases,
		repacketizerOracleCase{
			name:    "in_code1_passthrough",
			packets: [][]byte{code1Packet(18, false, 22, 5)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "in_code3cbr_passthrough",
			packets: [][]byte{code3CBRPacket(18, false, 4, 15, 6)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "in_code3vbr_passthrough",
			packets: [][]byte{code3VBRPacket(18, false, []int{10, 20, 30}, 7)},
			maxlen:  512, padNewLen: 0,
		},
		// merge a code1 + code0 (3 total frames -> code 3)
		repacketizerOracleCase{
			name:    "merge_code1_plus_code0",
			packets: [][]byte{code1Packet(18, false, 22, 5), code0Packet(18, false, 22, 8)},
			maxlen:  512, padNewLen: 0,
		},
	)

	// pad / unpad focused cases on various single packets.
	cases = append(cases,
		repacketizerOracleCase{
			name:      "pad_code0_small",
			packets:   [][]byte{code0Packet(18, false, 10, 1)},
			maxlen:    512,
			padNewLen: 11, // +1 byte -> minimal code 3 padding
		},
		repacketizerOracleCase{
			name:      "pad_code0_large",
			packets:   [][]byte{code0Packet(18, false, 10, 1)},
			maxlen:    512,
			padNewLen: 300, // forces chained 255 padding bytes
		},
		repacketizerOracleCase{
			name:      "pad_code1",
			packets:   [][]byte{code1Packet(18, false, 12, 2)},
			maxlen:    512,
			padNewLen: 200,
		},
		repacketizerOracleCase{
			name:      "pad_code3cbr",
			packets:   [][]byte{code3CBRPacket(18, false, 3, 12, 4)},
			maxlen:    512,
			padNewLen: 150,
		},
		repacketizerOracleCase{
			name:      "pad_noncanonical_two_frame_vbr",
			packets:   [][]byte{code3VBRPacket(18, false, []int{12, 12}, 5)},
			maxlen:    512,
			padNewLen: len(code3VBRPacket(18, false, []int{12, 12}, 5)) + 29,
		},
		repacketizerOracleCase{
			name:      "pad_48_frame_vbr",
			packets:   [][]byte{code3VBRPacket(16, false, packet48VBRFrameSizes(), 6)},
			maxlen:    512,
			padNewLen: len(code3VBRPacket(16, false, packet48VBRFrameSizes(), 6)) + 43,
		},
		repacketizerOracleCase{
			name:      "pad_with_packet_extension",
			packets:   [][]byte{{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}},
			maxlen:    512,
			padNewLen: 16,
		},
		repacketizerOracleCase{
			name:      "pad_with_large_packet_extension",
			packets:   [][]byte{largeExtensionPacket},
			maxlen:    len(largeExtensionPacket) + 128,
			padNewLen: len(largeExtensionPacket) + 43,
		},
	)

	// buffer-too-small: maxlen smaller than required output.
	cases = append(cases,
		repacketizerOracleCase{
			name:    "buftoosmall_merge",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 30, 2)},
			maxlen:  3, // way too small
		},
	)

	// over-duration / TOC-mismatch rejection on cat.
	cases = append(cases,
		repacketizerOracleCase{
			name: "toc_mismatch",
			packets: [][]byte{
				code0Packet(18, false, 10, 1),
				code0Packet(20, false, 10, 2), // different config -> toc top bits differ
			},
			maxlen: 512,
		},
		repacketizerOracleCase{
			name: "over_120ms",
			packets: [][]byte{
				// config 31 = 960 samples/frame; 2 frames = 40ms; cat 4 of them = 80ms ok,
				// but cat enough to exceed 120ms. 6 frames*960 = 5760 (=120ms) ok,
				// the 7th makes it exceed.
				code3CBRPacket(31, false, 6, 8, 1),
				code0Packet(31, false, 8, 2),
			},
			maxlen: 2048,
		},
	)

	return cases
}

func packet48VBRFrameSizes() []int {
	sizes := make([]int, maxRepacketizerFrames)
	for i := range sizes {
		sizes[i] = 1
	}
	sizes[len(sizes)-1] = 2
	return sizes
}

func packetWithLargeLongExtension(payloadLen int) []byte {
	extensionLen := payloadLen + 1 // one byte for the long-extension ID
	packet := []byte{GenerateTOC(18, false, 3), 0x41}
	remainingPadding := extensionLen
	for remainingPadding > 254 {
		packet = append(packet, 255)
		remainingPadding -= 254
	}
	packet = append(packet, byte(remainingPadding))
	extension := make([]byte, extensionLen)
	extension[0] = 0x50
	for i := 0; i < payloadLen; i++ {
		extension[i+1] = byte(i*13 + 7)
	}
	packet = append(packet, 0x11, 0x22, 0x33)
	return append(packet, extension...)
}
