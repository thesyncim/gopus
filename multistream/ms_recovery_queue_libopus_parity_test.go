// Multistream per-stream recovery-queue libopus oracle parity.
//
// Tests feed a multistream sequence with packet-gap patterns and assert that
// each stream's PLC and FEC recovery tracks the libopus multistream decoder
// per stream.  Reference: opus_multistream_decoder_create /
// opus_multistream_decode_float in opus_multistream.c (libopus 1.6.1).
//
// DRED per-stream recovery is gated under gopus_dred and tested in the
// separate dred_decoder_test.go / dred_recovery_queue_libopus_parity_test.go
// files; this file covers the non-DRED recovery surface (PLC and in-band FEC).

package multistream

import (
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/types"
)

// runMSRecoveryOracle encodes numFrames frames with the given encoder then
// feeds the sequence with losses at the provided loss indices to both gopus and
// the libopus multistream C oracle.  It asserts that the decoded waveforms
// match at the qualityBarWaveformNearExact level.
//
// lossAt is a map[frameIndex]bool; if true the packet sent to the decoder is
// nil (PLC path).  The libopus oracle receives the same sequence (nil is
// encoded as packet_len=0 which triggers PLC there too).
//
// Pass mode=internalenc.ModeAuto to let the encoder choose automatically;
// pass bandwidth=0 to leave bandwidth at the encoder default.
func runMSRecoveryOracle(t *testing.T, label string, channels, sampleRate, bitrate int, mode internalenc.Mode, bandwidth types.Bandwidth, forceMode bool, lossAt map[int]bool) {
	t.Helper()
	libopustest.RequireOracle(t)

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("%s DefaultMapping: %v", label, err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewEncoder: %v", label, err)
	}
	if forceMode {
		enc.SetMode(mode)
		if bandwidth != 0 {
			enc.SetBandwidth(bandwidth)
		}
	}
	enc.SetBitrate(bitrate)

	const numFrames = 12
	frameSize := sampleRate / 50 // 20 ms

	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(channels, frameSize)
		p, encErr := enc.Encode(pcm, frameSize)
		if encErr != nil {
			t.Fatalf("%s frame %d Encode: %v", label, i, encErr)
		}
		if p == nil {
			// DTX silence - substitute comfort noise TOC so oracle has same input
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	// Build oracle sequence: nil becomes zero-length packet_len=0 in oracle wire
	// format (the C oracle interprets packet_len==0 as PLC like libopus does).
	oracleSeq := make([][]byte, numFrames)
	gopusSeq := make([][]byte, numFrames)
	for i, p := range packets {
		if lossAt[i] {
			oracleSeq[i] = nil // oracle interprets nil as packet_len=0 → PLC
			gopusSeq[i] = nil  // gopus Decode(nil, ...) → PLC
		} else {
			oracleSeq[i] = p
			gopusSeq[i] = p
		}
	}

	// libopus oracle decode
	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, oracleSeq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode", err)
	}

	// gopus decode
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewDecoder: %v", label, err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range gopusSeq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("%s frame %d Decode: %v", label, i, decErr)
		}
		if len(frame) != frameSize*channels {
			t.Fatalf("%s frame %d Decode samples=%d want %d", label, i, len(frame)/channels, frameSize)
		}
		got = append(got, frame...)
	}

	if len(got) != len(want) {
		t.Fatalf("%s decoded sample count=%d want %d", label, len(got), len(want))
	}

	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, label)
}

// TestLibopus_MSRecovery_CELTSingleGap validates PLC recovery for a single
// dropped packet in a 3-channel CELT multistream.
// C ref: opus_multistream_decode_float, opus_multistream.c, libopus 1.6.1
func TestLibopus_MSRecovery_CELTSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@5",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{5: true},
	)
}

// TestLibopus_MSRecovery_CELTLeadingGap validates PLC when the very first
// packet is lost (decoder not yet warmed).
func TestLibopus_MSRecovery_CELTLeadingGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@0",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{0: true},
	)
}

// TestLibopus_MSRecovery_CELTConsecutiveGaps validates PLC over two
// consecutive dropped frames.
func TestLibopus_MSRecovery_CELTConsecutiveGaps(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@3,4",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{3: true, 4: true},
	)
}

// TestLibopus_MSRecovery_CELTTwoSeparateGaps validates PLC recovery with two
// independent gaps in a 6-channel (5.1) multistream.
func TestLibopus_MSRecovery_CELTTwoSeparateGaps(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-6ch-gap@2,8",
		6, 48000, 256000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{2: true, 8: true},
	)
}

// TestLibopus_MSRecovery_SILKSingleGap validates PLC recovery for a single
// dropped packet in a 3-channel SILK multistream.
func TestLibopus_MSRecovery_SILKSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "SILK-3ch-gap@5",
		3, 48000, 192000,
		internalenc.ModeSILK, types.BandwidthWideband, true,
		map[int]bool{5: true},
	)
}

// TestLibopus_MSRecovery_SILKConsecutiveGaps validates SILK multi-gap PLC.
func TestLibopus_MSRecovery_SILKConsecutiveGaps(t *testing.T) {
	runMSRecoveryOracle(t, "SILK-3ch-gap@4,5",
		3, 48000, 192000,
		internalenc.ModeSILK, types.BandwidthWideband, true,
		map[int]bool{4: true, 5: true},
	)
}

// TestLibopus_MSRecovery_HybridSingleGap validates PLC recovery for a single
// dropped packet in a 3-channel Hybrid multistream.
func TestLibopus_MSRecovery_HybridSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "Hybrid-3ch-gap@5",
		3, 48000, 192000,
		internalenc.ModeHybrid, types.BandwidthFullband, true,
		map[int]bool{5: true},
	)
}

// TestLibopus_MSRecovery_71SurroundSingleGap validates PLC recovery for a
// single dropped frame across all 5 streams of a 7.1 (8-channel) config.
func TestLibopus_MSRecovery_71SurroundSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-8ch-gap@6",
		8, 48000, 384000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{6: true},
	)
}

// TestLibopus_MSRecovery_71SurroundMultiGap validates multi-gap PLC for 7.1.
func TestLibopus_MSRecovery_71SurroundMultiGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-8ch-gap@3,7,10",
		8, 48000, 384000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{3: true, 7: true, 10: true},
	)
}

// TestLibopus_MSRecovery_GapAtEnd validates PLC when the last packet is lost.
func TestLibopus_MSRecovery_GapAtEnd(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@11",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{11: true},
	)
}

// TestLibopus_MSRecovery_AutoModeSingleGap validates PLC for Auto (mode-selected)
// multistream across a single gap.
func TestLibopus_MSRecovery_AutoModeSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "Auto-6ch-gap@5",
		6, 48000, 256000,
		internalenc.ModeAuto, 0, false,
		map[int]bool{5: true},
	)
}

// ---------------------------------------------------------------------------
// FEC / LBRR recovery parity
// ---------------------------------------------------------------------------

// runMSFECRecoveryOracle validates in-band FEC recovery:  it encodes with FEC
// enabled, drops a packet in the middle of the sequence, and asks the decoder
// to recover the lost frame from the redundancy in the next good packet
// (forward=1 path).  The libopus oracle is asked to do the same: nil packet
// followed by the redundancy carrier, with no explicit FEC flag needed since
// libopus always auto-applies LBRR on PLC.
//
// The test simply validates that gopus and libopus agree on the recovery audio.
func runMSFECRecoveryOracle(t *testing.T, label string, channels, sampleRate, bitrate, lossIdx int) {
	t.Helper()
	libopustest.RequireOracle(t)

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("%s DefaultMapping: %v", label, err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewEncoder: %v", label, err)
	}
	enc.SetBitrate(bitrate)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	const numFrames = 10
	frameSize := sampleRate / 50

	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(channels, frameSize)
		p, encErr := enc.Encode(pcm, frameSize)
		if encErr != nil {
			t.Fatalf("%s frame %d Encode: %v", label, i, encErr)
		}
		if p == nil {
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	// Build sequence with PLC hole at lossIdx — both oracle and gopus use nil.
	seq := make([][]byte, numFrames)
	copy(seq, packets)
	seq[lossIdx] = nil

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference FEC decode", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewDecoder: %v", label, err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("%s frame %d Decode: %v", label, i, decErr)
		}
		got = append(got, frame...)
	}

	if len(got) != len(want) {
		t.Fatalf("%s decoded sample count=%d want %d", label, len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, label)
}

// TestLibopus_MSRecovery_FECSILKGap validates SILK in-band FEC recovery for a
// 3-channel multistream.
// C ref: opus_decode_frame (silk path, FEC decode), opus_decoder.c libopus 1.6.1
func TestLibopus_MSRecovery_FECSILKGap(t *testing.T) {
	runMSFECRecoveryOracle(t, "FEC-SILK-3ch-gap@3", 3, 48000, 192000, 3)
}

// TestLibopus_MSRecovery_FECSILKGap51 validates SILK in-band FEC for 5.1.
func TestLibopus_MSRecovery_FECSILKGap51(t *testing.T) {
	runMSFECRecoveryOracle(t, "FEC-SILK-6ch-gap@5", 6, 48000, 256000, 5)
}

// ---------------------------------------------------------------------------
// Cross-mode handover parity
// ---------------------------------------------------------------------------

// TestLibopus_MSRecovery_ModeHandoverGap checks PLC after a mode transition:
// first half SILK, second half CELT, with a gap across the transition boundary.
// This exercises the cross-mode decoder state management per stream.
// C ref: opus_decode_frame mode-switch PLC path, opus_decoder.c libopus 1.6.1
func TestLibopus_MSRecovery_ModeHandoverGap(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 3
		sampleRate = 48000
		bitrate    = 192000
		numFrames  = 12
	)
	frameSize := sampleRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	var packets [numFrames][]byte
	for phase := 0; phase < 2; phase++ {
		enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
		if err != nil {
			t.Fatalf("phase%d NewEncoder: %v", phase, err)
		}
		enc.SetBitrate(bitrate)
		if phase == 0 {
			enc.SetMode(internalenc.ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
		} else {
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
		}
		start, end := phase*(numFrames/2), (phase+1)*(numFrames/2)
		for i := start; i < end; i++ {
			pcm := generateMultichannelSine(channels, frameSize)
			p, encErr := enc.Encode(pcm, frameSize)
			if encErr != nil {
				t.Fatalf("phase%d frame %d Encode: %v", phase, i, encErr)
			}
			if p == nil {
				p = []byte{0xF8, 0xFF, 0xFE}
			}
			packets[i] = p
		}
	}

	// Drop the first frame after the mode switch (frame 6)
	lossAt := map[int]bool{6: true}
	seq := make([][]byte, numFrames)
	copy(seq, packets[:])
	for i := range seq {
		if lossAt[i] {
			seq[i] = nil
		}
	}

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference mode-handover decode", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("frame %d Decode: %v", i, decErr)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "mode-handover-gap")
}

// ---------------------------------------------------------------------------
// Sub-48 kHz PLC parity
// ---------------------------------------------------------------------------

// TestLibopus_MSRecovery_16kSILKGap validates PLC at 16 kHz API rate for a
// 1-channel (mono) multistream — tests the API-rate PLC path for MS decoder.
func TestLibopus_MSRecovery_16kSILKGap(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encRate    = 48000
		apiRate    = 16000
		channels   = 1
		sampleRate = apiRate
		numFrames  = 8
	)
	encFrameSize := encRate / 50
	frameSize := apiRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(32000)

	var packets [numFrames][]byte
	for i := range packets {
		pcm := generateMultichannelSine(channels, encFrameSize)
		p, encErr := enc.Encode(pcm, encFrameSize)
		if encErr != nil {
			t.Fatalf("frame %d Encode: %v", i, encErr)
		}
		if p == nil {
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	seq := make([][]byte, numFrames)
	copy(seq, packets[:])
	seq[4] = nil // single gap

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference 16k SILK PLC", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("frame %d Decode: %v", i, decErr)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "16k-SILK-1ch-gap@4")
}

// ---------------------------------------------------------------------------
// Per-stream isolation: only the gapped stream should produce PLC audio while
// the others decode normally.
// ---------------------------------------------------------------------------

// TestLibopus_MSRecovery_PerStreamIsolation feeds a 6-channel (5.1) multistream
// sequence to gopus and libopus.  In the reference the entire multistream
// packet is nil (simulating loss of the network packet), so ALL streams PLC
// simultaneously — same as libopus opus_multistream_decode_float(NULL, 0, ...).
// This confirms per-stream state isolation is correct (the other streams resume
// normally after recovery).
func TestLibopus_MSRecovery_PerStreamIsolation(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 6
		sampleRate = 48000
		bitrate    = 256000
		numFrames  = 10
	)
	frameSize := sampleRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetBitrate(bitrate)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	packets := make([][]byte, numFrames)
	for i := range packets {
		pcm := generateMultichannelSine(channels, frameSize)
		p, encErr := enc.Encode(pcm, frameSize)
		if encErr != nil {
			t.Fatalf("frame %d Encode: %v", i, encErr)
		}
		if p == nil {
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	// Drop frames 3 and 4 (two consecutive losses, then resume)
	seq := make([][]byte, numFrames)
	copy(seq, packets)
	seq[3] = nil
	seq[4] = nil

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference per-stream isolation", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("frame %d Decode: %v", i, decErr)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}

	// Assert full-sequence quality: both the PLC frames AND the resumed
	// good frames must match libopus.
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "5.1-CELT-per-stream-isolation-gap@3,4")

	// Additionally assert the resumed frames (5..9) agree closely: extract
	// the post-recovery tail and score it independently.
	skipSamples := 5 * frameSize * channels
	if len(got) > skipSamples && len(want) > skipSamples {
		cmpTail := compareWaveformF32(got[skipSamples:], want[skipSamples:])
		qualitycompare.AssertQuality(t, cmpTail, qualityBarWaveformNearExact, "5.1-CELT-per-stream-isolation-resumed-tail")
	}
}
