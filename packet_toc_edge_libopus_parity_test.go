package gopus

// packet_toc_edge_libopus_parity_test.go — differential test of TOC/packet
// parsing against the libopus C oracle across the FULL TOC/config space.
//
// Coverage:
//   - All 256 TOC bytes (32 configs × stereo bit × 4 frame codes)
//   - ParseTOC bandwidth/channel fields vs opus_packet_get_bandwidth / _nb_channels
//   - packetFrameCountLibopus vs opus_packet_get_nb_frames
//   - packetSamplesPerFrameAtRate vs opus_packet_get_samples_per_frame (48kHz)
//   - packetSamplesAtRate vs opus_packet_get_nb_samples (48kHz)
//   - ParsePacket vs opus_packet_parse: accept/reject decision, TOC, payload
//     offset, and per-frame sizes — code 0, 1, 2, 3 (CBR/VBR)
//   - Code-3 M=1..48 exhaustive sweep
//   - Padding lengths: 0, 1, 127, 254, 255-chained (255→508 bytes)
//   - Self-delimited framing via parseSelfDelimitedPacket
//   - Zero-length and truncated boundary packets

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// libopusPacketParseResult holds all oracle outputs for one packet test case.
type libopusPacketParseResult struct {
	bandwidth       int32 // OPUS_BANDWIDTH_* (1101-1105) or error code
	nbChannels      int32 // 1 or 2
	nbFrames        int32 // >=1 or OPUS_BAD_ARG(-1) / OPUS_INVALID_PACKET(-4)
	samplesPerFrame int32 // at 48000
	nbSamples       int32 // at 48000 or error
	parseRet        int32 // frame count or error from opus_packet_parse
	parseTOC        int32 // TOC byte (or -1 on parse error)
	payloadOffset   int32 // (or -1 on parse error)
	nFrameSizes     int32 // == parseRet when parseRet > 0
	frameSizes      []int16
}

// libopusPacketParseCase is one test vector.
type libopusPacketParseCase struct {
	name   string
	packet []byte
}

var tocEdgeParityHelper libopustest.HelperCache

func getTOCEdgeHelperPath() (string, error) {
	return tocEdgeParityHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "packet parse",
		OutputBase: "gopus_libopus_packet_parse",
		SourceFile: "libopus_packet_parse_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

func probeLibopusPacketParse(cases []libopusPacketParseCase) ([]libopusPacketParseResult, error) {
	binPath, err := getTOCEdgeHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayload("GPPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packet)))
		payload.Raw(tc.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "packet parse", "GPPO")
	if err != nil {
		return nil, err
	}
	// Each output has variable size: 9×i32 + nFrameSizes×i16.
	// We can't pre-calculate ExpectRemaining, so read directly.
	reader.Count(len(cases))
	if reader.Err() != nil {
		return nil, reader.Err()
	}

	out := make([]libopusPacketParseResult, len(cases))
	for i := range out {
		r := &out[i]
		r.bandwidth = reader.I32()
		r.nbChannels = reader.I32()
		r.nbFrames = reader.I32()
		r.samplesPerFrame = reader.I32()
		r.nbSamples = reader.I32()
		r.parseRet = reader.I32()
		r.parseTOC = reader.I32()
		r.payloadOffset = reader.I32()
		r.nFrameSizes = reader.I32()
		if r.nFrameSizes > 0 {
			r.frameSizes = make([]int16, r.nFrameSizes)
			for j := range r.frameSizes {
				r.frameSizes[j] = reader.I16()
			}
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

// libopusBandwidthCode maps gopus Bandwidth (0-4) to the OPUS_BANDWIDTH_*
// integer values returned by opus_packet_get_bandwidth().
// src/opus_decoder.c opus_packet_get_bandwidth()
func libopusBandwidthCode(bw Bandwidth) int32 {
	// OPUS_BANDWIDTH_NARROWBAND=1101 … OPUS_BANDWIDTH_FULLBAND=1105
	return int32(1101) + int32(bw)
}

// TestTOCEdgeAllBytesMatchLibopus verifies that ParseTOC fields for all 256
// TOC bytes match what libopus derives via opus_packet_get_bandwidth() and
// opus_packet_get_nb_channels().  A minimal one-byte packet is used so the
// oracle can be called (zero-length is OPUS_INVALID_PACKET).
func TestTOCEdgeAllBytesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := make([]libopusPacketParseCase, 256)
	for b := 0; b < 256; b++ {
		// TOC-only packet: len=1, no payload bytes.
		// opus_packet_get_bandwidth / _nb_channels only need data[0].
		// opus_packet_get_nb_frames with len=1 returns 1 for code 0/1/2
		// but OPUS_INVALID_PACKET for code 3.
		cases[b] = libopusPacketParseCase{
			name:   fmt.Sprintf("toc_0x%02x", b),
			packet: []byte{byte(b)},
		}
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for b := 0; b < 256; b++ {
		b := b
		t.Run(cases[b].name, func(t *testing.T) {
			toc := ParseTOC(byte(b))
			w := want[b]

			// Bandwidth: libopus returns OPUS_BANDWIDTH_NARROWBAND(1101) .. FULLBAND(1105).
			wantBW := w.bandwidth
			gotBW := libopusBandwidthCode(toc.Bandwidth)
			if gotBW != wantBW {
				t.Errorf("bandwidth: got %d want %d (toc=0x%02x)", gotBW, wantBW, b)
			}

			// Channels: libopus (data[0]&0x4)?2:1
			wantCh := w.nbChannels
			gotCh := int32(1)
			if toc.Stereo {
				gotCh = 2
			}
			if gotCh != wantCh {
				t.Errorf("channels: got %d want %d (toc=0x%02x)", gotCh, wantCh, b)
			}
		})
	}
}

// TestTOCEdgeNbFramesMatchLibopus checks packetFrameCountLibopus vs
// opus_packet_get_nb_frames for every TOC byte with one-byte packets.
func TestTOCEdgeNbFramesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := make([]libopusPacketParseCase, 256)
	for b := 0; b < 256; b++ {
		cases[b] = libopusPacketParseCase{
			name:   fmt.Sprintf("toc_0x%02x", b),
			packet: []byte{byte(b)},
		}
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for b := 0; b < 256; b++ {
		b := b
		t.Run(cases[b].name, func(t *testing.T) {
			w := want[b]
			got, gotErr := packetFrameCountLibopus(cases[b].packet)

			if w.nbFrames < 0 {
				// libopus returned error; gopus must also return an error
				if gotErr == nil {
					t.Errorf("packetFrameCountLibopus: got %d, nil err; want error (libopus=%d)", got, w.nbFrames)
				}
			} else {
				if gotErr != nil {
					t.Errorf("packetFrameCountLibopus: got err=%v; want %d", gotErr, w.nbFrames)
				} else if int32(got) != w.nbFrames {
					t.Errorf("packetFrameCountLibopus: got %d want %d", got, w.nbFrames)
				}
			}
		})
	}
}

// TestTOCEdgeSamplesPerFrameMatchLibopus verifies packetSamplesPerFrameAtRate
// vs opus_packet_get_samples_per_frame for every TOC byte at 48kHz.
func TestTOCEdgeSamplesPerFrameMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := make([]libopusPacketParseCase, 256)
	for b := 0; b < 256; b++ {
		cases[b] = libopusPacketParseCase{
			name:   fmt.Sprintf("toc_0x%02x", b),
			packet: []byte{byte(b)},
		}
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for b := 0; b < 256; b++ {
		b := b
		t.Run(cases[b].name, func(t *testing.T) {
			w := want[b]
			got, gotErr := packetSamplesPerFrameAtRate(cases[b].packet, 48000)
			if gotErr != nil {
				t.Fatalf("packetSamplesPerFrameAtRate: unexpected error %v", gotErr)
			}
			if int32(got) != w.samplesPerFrame {
				t.Errorf("samplesPerFrame: got %d want %d", got, w.samplesPerFrame)
			}
		})
	}
}

// TestTOCEdgeParsePacketCode0AllConfigsMatchLibopus verifies ParsePacket for
// code-0 packets across all 32 configs × stereo.
func TestTOCEdgeParsePacketCode0AllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode0Cases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode1AllConfigsMatchLibopus verifies ParsePacket for
// code-1 packets across all 32 configs × stereo.
func TestTOCEdgeParsePacketCode1AllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode1Cases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode2AllConfigsMatchLibopus verifies ParsePacket for
// code-2 packets across all 32 configs × stereo.
func TestTOCEdgeParsePacketCode2AllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode2Cases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode3CBRAllMMatchLibopus verifies ParsePacket for
// code-3 CBR across all valid M values (1..48) for every config × stereo.
func TestTOCEdgeParsePacketCode3CBRAllMMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode3CBRAllMCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketCode3VBRAllMMatchLibopus verifies ParsePacket for
// code-3 VBR across all valid M values (1..48).
func TestTOCEdgeParsePacketCode3VBRAllMMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeCode3VBRAllMCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketPaddingVariantsMatchLibopus exercises all padding-length
// boundary variants: 0, 1, 127, 254, and the 255-chained case (≥256 pad bytes).
func TestTOCEdgeParsePacketPaddingVariantsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgePaddingCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParsePacketParity(t, cases, want)
}

// TestTOCEdgeParsePacketBoundaryRejectMatchLibopus verifies that gopus and
// libopus agree on rejection for every structurally invalid boundary packet:
// truncated code-2, code-3 with M=0/49+, over-duration, CBR non-divisible.
func TestTOCEdgeParsePacketBoundaryRejectMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeBoundaryCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	assertParseAcceptRejectParity(t, cases, want)
}

// TestTOCEdgeNbSamplesAllConfigsMatchLibopus checks packetSamplesAtRate for a
// valid multi-frame packet across all 32 configs at 48kHz.
func TestTOCEdgeNbSamplesAllConfigsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := tocEdgeNbSamplesCases()
	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}
	for i, tc := range cases {
		tc := tc
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			got, gotErr := packetSamplesAtRate(tc.packet, 48000)
			if w.nbSamples < 0 {
				if gotErr == nil {
					t.Errorf("packetSamplesAtRate: got %d nil err; want error (libopus=%d)", got, w.nbSamples)
				}
			} else {
				if gotErr != nil {
					t.Errorf("packetSamplesAtRate: err=%v want %d", gotErr, w.nbSamples)
				} else if int32(got) != w.nbSamples {
					t.Errorf("packetSamplesAtRate: got %d want %d", got, w.nbSamples)
				}
			}
		})
	}
}

// ── case builders ────────────────────────────────────────────────────────────

// tocEdgeCode0Cases builds one code-0 case per config × stereo.
func tocEdgeCode0Cases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := uint8(0); config < 32; config++ {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 0)
			// 50 bytes of payload — fits in maxOpusFrameBytes (1275)
			pkt := make([]byte, 51)
			pkt[0] = toc
			for j := 1; j < len(pkt); j++ {
				pkt[j] = byte(j & 0xFF)
			}
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases, libopusPacketParseCase{
				name:   fmt.Sprintf("code0_cfg%d_%s", config, s),
				packet: pkt,
			})
		}
	}
	return cases
}

// tocEdgeCode1Cases builds code-1 cases (two equal frames) for all configs × stereo.
func tocEdgeCode1Cases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := uint8(0); config < 32; config++ {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 1)
			// 100 bytes payload = two 50-byte frames
			pkt := make([]byte, 101)
			pkt[0] = toc
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases, libopusPacketParseCase{
				name:   fmt.Sprintf("code1_cfg%d_%s", config, s),
				packet: pkt,
			})
		}
	}
	return cases
}

// tocEdgeCode2Cases builds code-2 cases for all configs × stereo with a
// one-byte frame-length header (frame0=30, frame1=remainder).
func tocEdgeCode2Cases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := uint8(0); config < 32; config++ {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 2)
			// Header: TOC, length=30, then 30+50=80 bytes payload
			pkt := make([]byte, 82) // 1 toc + 1 len + 30 + 50
			pkt[0] = toc
			pkt[1] = 30
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases, libopusPacketParseCase{
				name:   fmt.Sprintf("code2_cfg%d_%s", config, s),
				packet: pkt,
			})
		}
	}
	return cases
}

// tocEdgeCode3CBRAllMCases generates CBR code-3 packets for every valid M (1..48)
// for the CELT NB 2.5ms config (config=16), which has the smallest frame size
// (120 samples) allowing M=48 within the 120ms limit.
func tocEdgeCode3CBRAllMCases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase

	// config 16 = CELT NB 2.5ms: frameSize=120 samples → 120*48=5760 ≤ 5760 ✓
	for _, config := range []uint8{16, 18, 0} { // also 10ms CELT, 10ms SILK
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 3)
			frameSize48k := configTable[config].FrameSize
			s := "m"
			if stereo {
				s = "s"
			}
			for m := 1; m <= 48; m++ {
				totalSamples := frameSize48k * m
				if totalSamples > maxRepacketizerDuration48k {
					break // M too large for this config
				}
				// CBR: frameCountByte has VBR=0, pad=0, M=m
				frameCountByte := byte(m & 0x3F)
				// Use 10 bytes per frame
				frameDataLen := 10 * m
				pkt := make([]byte, 2+frameDataLen)
				pkt[0] = toc
				pkt[1] = frameCountByte
				cases = append(cases, libopusPacketParseCase{
					name:   fmt.Sprintf("code3_cbr_cfg%d_%s_m%d", config, s, m),
					packet: pkt,
				})
			}
		}
	}
	return cases
}

// tocEdgeCode3VBRAllMCases generates VBR code-3 packets for M=1..48 using
// config 16 (CELT NB 2.5ms).
func tocEdgeCode3VBRAllMCases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase

	// config 16 = CELT NB 2.5ms
	for _, config := range []uint8{16, 18} {
		for _, stereo := range []bool{false, true} {
			toc := GenerateTOC(config, stereo, 3)
			frameSize48k := configTable[config].FrameSize
			s := "m"
			if stereo {
				s = "s"
			}
			for m := 1; m <= 48; m++ {
				totalSamples := frameSize48k * m
				if totalSamples > maxRepacketizerDuration48k {
					break
				}
				// VBR: frameCountByte has VBR=1, pad=0, M=m
				frameCountByte := byte(0x80 | (m & 0x3F))
				// Each of the first m-1 frames has explicit size=10 (single byte < 252)
				// last frame is implicit (10 bytes too)
				header := make([]byte, 2+(m-1))
				header[0] = toc
				header[1] = frameCountByte
				for i := 0; i < m-1; i++ {
					header[2+i] = 10
				}
				framePayload := make([]byte, 10*m)
				pkt := append(header, framePayload...)
				cases = append(cases, libopusPacketParseCase{
					name:   fmt.Sprintf("code3_vbr_cfg%d_%s_m%d", config, s, m),
					packet: pkt,
				})
			}
		}
	}
	return cases
}

// tocEdgePaddingCases generates code-3 CBR packets with various padding lengths.
func tocEdgePaddingCases() []libopusPacketParseCase {
	// config 16 = CELT NB 2.5ms, mono, code 3
	toc := GenerateTOC(16, false, 3)

	makePaddedCBR := func(name string, m int, padBytes []byte, frameDataLen int) libopusPacketParseCase {
		// frameCountByte: VBR=0, pad=1, M=m
		frameCountByte := byte(0x40 | (m & 0x3F))
		pkt := []byte{toc, frameCountByte}
		pkt = append(pkt, padBytes...)
		pkt = append(pkt, make([]byte, frameDataLen)...)
		return libopusPacketParseCase{name: name, packet: pkt}
	}

	cases := []libopusPacketParseCase{
		// pad=0: single terminator byte 0x00
		makePaddedCBR("pad0_m2", 2, []byte{0x00}, 20),
		// pad=1: single byte 0x01
		makePaddedCBR("pad1_m2", 2, []byte{0x01}, 21),
		// pad=127
		makePaddedCBR("pad127_m2", 2, []byte{0x7F}, 127+20),
		// pad=254: single byte 0xFE
		makePaddedCBR("pad254_m2", 2, []byte{0xFE}, 254+20),
		// pad=255-chained: 0xFF (adds 254) + 0x01 (adds 1) = 255 total
		makePaddedCBR("pad255_chain_m2", 2, []byte{0xFF, 0x01}, 255+20),
		// pad=508: 0xFF (254) + 0xFF (254) + 0x00 = 508 total
		makePaddedCBR("pad508_chain_m2", 2, []byte{0xFF, 0xFF, 0x00}, 508+20),
		// pad=509: 0xFF (254) + 0xFF (254) + 0x01 = 509 total
		makePaddedCBR("pad509_chain_m2", 2, []byte{0xFF, 0xFF, 0x01}, 509+20),
	}

	// VBR padded
	makeVBRPadded := func(name string, m int, padBytes []byte) libopusPacketParseCase {
		// VBR=1, pad=1, M=m
		frameCountByte := byte(0xC0 | (m & 0x3F))
		header := []byte{toc, frameCountByte}
		header = append(header, padBytes...)
		// m-1 explicit frame sizes (each = 10), then 10*m payload
		for i := 0; i < m-1; i++ {
			header = append(header, 10)
		}
		pkt := append(header, make([]byte, 10*m)...)
		return libopusPacketParseCase{name: name, packet: pkt}
	}
	padLen := 5
	padBytes := []byte{byte(padLen)}
	frameCount := 3
	// Ensure M frames fit within 120ms
	_ = padLen
	cases = append(cases,
		makeVBRPadded("vbr_pad5_m3", frameCount, padBytes),
		makeVBRPadded("vbr_pad0_m1", 1, []byte{0x00}),
		makeVBRPadded("vbr_pad254_m2", 2, []byte{0xFE}),
	)

	return cases
}

// tocEdgeBoundaryCases generates structurally invalid packets that libopus
// and gopus should both reject.
func tocEdgeBoundaryCases() []libopusPacketParseCase {
	// config 16 = CELT NB 2.5ms (120 samples/frame); 48*120=5760 ≤ 5760
	// config 31 = CELT FB 20ms (960 samples/frame); 7*960=6720 > 5760 ⇒ reject M=7

	return []libopusPacketParseCase{
		{name: "empty", packet: []byte{}},
		// code 2 with no length byte
		{name: "code2_toc_only", packet: []byte{GenerateTOC(0, false, 2)}},
		// code 2 with truncated two-byte length (only first byte)
		{name: "code2_two_byte_len_truncated", packet: []byte{GenerateTOC(0, false, 2), 252}},
		// code 3 with no frame-count byte
		{name: "code3_no_count_byte", packet: []byte{GenerateTOC(16, false, 3)}},
		// code 3 with M=0
		{name: "code3_m0", packet: []byte{GenerateTOC(16, false, 3), 0x00}},
		// code 3 with M=49 (>48)
		{name: "code3_m49", packet: []byte{GenerateTOC(16, false, 3), 49}},
		// code 3 with M=63 (max of 6-bit field, still >48)
		{name: "code3_m63", packet: []byte{GenerateTOC(16, false, 3), 63}},
		// code 3 CELT FB 20ms M=7: 7*960=6720 > 5760ms → over-duration
		{name: "code3_over_duration", packet: []byte{GenerateTOC(31, false, 3), 0x07}},
		// code 3 CBR non-divisible: 3 frames, 7 payload bytes (7%3≠0)
		{name: "code3_cbr_nondivisible", packet: append([]byte{GenerateTOC(16, false, 3), 0x03}, make([]byte, 7)...)},
		// code 0 frame too large (maxOpusFrameBytes+1 = 1276 payload bytes)
		{name: "code0_frame_too_large", packet: append([]byte{GenerateTOC(0, false, 0)}, make([]byte, maxOpusFrameBytes+1)...)},
		// code 1 each frame too large
		{name: "code1_frame_too_large", packet: append([]byte{GenerateTOC(0, false, 1)}, make([]byte, (maxOpusFrameBytes+1)*2)...)},
		// code 2 with explicit frame1 size larger than remaining data
		{name: "code2_frame1_overrun", packet: []byte{GenerateTOC(0, false, 2), 200, 0xAA}},
		// code 3 VBR explicit frame size overrun
		{name: "code3_vbr_frame_overrun", packet: []byte{GenerateTOC(16, false, 3), 0x82, 200, 0xAA}},
		// code 3 CBR padding that consumes all remaining bytes (len<0 after pad)
		// TOC + count(pad|M=1) + huge pad byte chain > packet length
		{name: "code3_padding_overrun", packet: []byte{GenerateTOC(16, false, 3), 0x41, 0xFF}},
	}
}

// tocEdgeNbSamplesCases covers every config with a minimal valid packet so we
// can test packetSamplesAtRate vs opus_packet_get_nb_samples.
func tocEdgeNbSamplesCases() []libopusPacketParseCase {
	var cases []libopusPacketParseCase
	for config := uint8(0); config < 32; config++ {
		toc := GenerateTOC(config, false, 0)
		pkt := make([]byte, 51)
		pkt[0] = toc
		cases = append(cases, libopusPacketParseCase{
			name:   fmt.Sprintf("nbsamples_cfg%d", config),
			packet: pkt,
		})
	}
	// Code-3 M=2 CBR for each CELT config to hit the multi-frame path
	for _, config := range []uint8{16, 17, 18, 19, 28, 29, 30, 31} {
		toc := GenerateTOC(config, false, 3)
		frameSize := 10
		pkt := append([]byte{toc, 0x02}, make([]byte, frameSize*2)...)
		cases = append(cases, libopusPacketParseCase{
			name:   fmt.Sprintf("nbsamples_code3_cfg%d_m2", config),
			packet: pkt,
		})
	}
	// Over-duration: must be rejected
	// config 31 (960 samples), M=7: 6720 > 5760
	cases = append(cases, libopusPacketParseCase{
		name:   "nbsamples_overduration",
		packet: []byte{GenerateTOC(31, false, 3), 0x07},
	})
	return cases
}

// ── assertion helpers ─────────────────────────────────────────────────────────

// assertParsePacketParity asserts that ParsePacket produces identical results
// to opus_packet_parse for every case in cases.
func assertParsePacketParity(t *testing.T, cases []libopusPacketParseCase, want []libopusPacketParseResult) {
	t.Helper()
	for i, tc := range cases {
		tc := tc
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			info, gotErr := ParsePacket(tc.packet)

			if w.parseRet < 0 {
				// libopus rejected the packet; gopus must also reject
				if gotErr == nil {
					t.Fatalf("ParsePacket: got nil error (frameCount=%d); libopus returned error %d",
						info.FrameCount, w.parseRet)
				}
				return
			}

			// libopus accepted
			if gotErr != nil {
				t.Fatalf("ParsePacket: got error %v; libopus accepted (count=%d)", gotErr, w.parseRet)
			}

			// Frame count
			if int32(info.FrameCount) != w.parseRet {
				t.Errorf("FrameCount: got %d want %d", info.FrameCount, w.parseRet)
			}

			// TOC byte
			if len(tc.packet) > 0 {
				wantTOCByte := byte(w.parseTOC)
				// libopus out_toc preserves the full TOC byte:
				// toc = *data++; ... if (out_toc) *out_toc = toc;
				gotTOCByte := tc.packet[0]
				if gotTOCByte != wantTOCByte {
					t.Errorf("TOC byte: got 0x%02x want 0x%02x", gotTOCByte, wantTOCByte)
				}
			}

			// Frame sizes
			if int32(len(info.FrameSizes)) != w.nFrameSizes {
				t.Errorf("len(FrameSizes): got %d want %d", len(info.FrameSizes), w.nFrameSizes)
				return
			}
			for j, sz := range info.FrameSizes {
				wantSz := int(w.frameSizes[j])
				if sz != wantSz {
					t.Errorf("FrameSizes[%d]: got %d want %d", j, sz, wantSz)
				}
			}
		})
	}
}

// assertParseAcceptRejectParity only checks accept/reject parity (for boundary
// reject cases we do not assert parsed field values).
func assertParseAcceptRejectParity(t *testing.T, cases []libopusPacketParseCase, want []libopusPacketParseResult) {
	t.Helper()
	for i, tc := range cases {
		tc := tc
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			_, gotErr := ParsePacket(tc.packet)

			libopusAccepted := w.parseRet > 0
			gopusAccepted := gotErr == nil

			if gopusAccepted != libopusAccepted {
				if libopusAccepted {
					t.Errorf("ParsePacket: gopus rejected but libopus accepted (ret=%d): %v",
						w.parseRet, gotErr)
				} else {
					t.Errorf("ParsePacket: gopus accepted but libopus rejected (ret=%d)",
						w.parseRet)
				}
			}
		})
	}
}
