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
