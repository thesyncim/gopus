//go:build gopus_dred || gopus_osce

package multistream

// Multistream per-stream DRED recovery queue libopus oracle parity.
//
// Verifies that each stream in a multistream decoder maintains an independent
// DRED recovery queue that advances in lock-step with the libopus multistream
// decoder's per-stream state, matching the behavior of
// opus_multistream_decode_float (opus_multistream.c, libopus 1.6.1).
//
// Key invariants (from libopus):
//  1. A stream without DRED falls back to PLC; a stream WITH DRED in its
//     payload uses opus_decoder_dred_decode_float for that stream.
//  2. Each stream's PLC state (blend, FEC fill/skip) is completely independent.
//  3. When only stream S has a DRED payload, streams ≠ S experience plain PLC
//     while stream S applies DRED recovery.
//  4. After recovery, the queue of stream S must advance exactly one frame per
//     loss (decodeOffset += frameSize).
//
// Reference: opus_multistream.c opus_multistream_decode_float (libopus 1.6.1),
// which calls opus_decoder_dred_decode_float per stream independently.

import (
	"fmt"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/types"
)

// TestMSPerStreamDREDQueueOnlyTargetStreamAdvances verifies that after a packet
// loss, only the stream carrying the DRED payload advances its recovery cursor
// while the other streams remain at zero (plain PLC, no DRED).
//
// This directly exercises the per-stream queue isolation: two streams in a
// 3-channel (stereo+mono) multistream; only the mono stream (targetStream=1)
// has a DRED payload.  After one packet loss the mono stream's blend/recovery
// state increments but the stereo stream (stream 0) stays clean.
func TestMSPerStreamDREDQueueOnlyTargetStreamAdvances(t *testing.T) {
	const (
		channels     = 3
		targetStream = 1 // mono stream
	)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeCELTMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	setDecoderComplexityForDREDParityTest(t, dec)
	modelBlob := makeLoadableDecoderDREDControlTestBlob(t)
	dec.SetDNNBlob(modelBlob)
	dec.setDREDDecoderBlob(modelBlob)

	frameSize := 960
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode(carrier) error: %v", err)
	}
	if dec.dred == nil || dec.dred.dredCache[targetStream].Empty() {
		t.Fatal("Decode did not cache target-stream DRED payload")
	}
	// All streams must start with blend=0 (no prior loss)
	for s, plc := range dec.dred.dredPLC {
		if b := plc.Blend(); b != 0 {
			t.Fatalf("stream %d blend after good decode=%d want 0", s, b)
		}
	}

	// Apply one packet loss via Decode(nil)
	if _, err := dec.Decode(nil, frameSize); err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}

	// Only the target stream must have DRED recovery active
	for s := range dec.dred.dredPLC {
		blend := dec.dred.dredPLC[s].Blend()
		recovery := dec.dred.dredRecovery[s]
		if s == targetStream {
			if blend != 1 {
				t.Fatalf("target stream %d blend after loss=%d want 1", s, blend)
			}
			if recovery != frameSize {
				t.Fatalf("target stream %d dredRecovery=%d want %d", s, recovery, frameSize)
			}
		} else {
			// Non-target streams: plain PLC — no DRED cache, no recovery cursor
			if !dec.dred.dredCache[s].Empty() {
				t.Fatalf("non-target stream %d unexpectedly has DRED cache after loss: %+v", s, dec.dred.dredCache[s])
			}
			if recovery != 0 {
				t.Fatalf("non-target stream %d dredRecovery=%d want 0", s, recovery)
			}
		}
	}
}

// TestMSPerStreamDREDQueueTwoStreamsIndependent verifies that when two
// different streams in a 6-channel (5.1) multistream have independent DRED
// payloads, their queues advance independently after a loss.
//
// We construct a packet where stream 0 (stereo) and stream 2 (mono) both carry
// a DRED payload, while stream 1 (stereo) does not.  After a packet loss:
//   - Streams 0 and 2 must both advance their recovery cursors.
//   - Stream 1 must stay at zero (plain PLC).
//
// Reference: per-stream DRED independence in opus_multistream_decode_float,
// opus_multistream.c (libopus 1.6.1).
func TestMSPerStreamDREDQueueTwoStreamsIndependent(t *testing.T) {
	const channels = 6
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	dec, err := NewDecoder(48000, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	setDecoderComplexityForDREDParityTest(t, dec)
	modelBlob := makeLoadableDecoderDREDControlTestBlob(t)
	dec.SetDNNBlob(modelBlob)
	dec.setDREDDecoderBlob(modelBlob)

	// Build a 6-channel CELT packet with DRED on streams 0 and 2.
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	enc, err := NewEncoder(48000, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	rawPacket, err := enc.Encode(generateMultichannelSine(channels, 960), 960)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	subPackets, err := parseMultistreamPacket(rawPacket, streams)
	if err != nil {
		t.Fatalf("parseMultistreamPacket: %v", err)
	}
	// Inject DRED into streams 0 and 2
	for _, s := range []int{0, 2} {
		subPackets[s] = addDREDExtensionToOpusPacketFrameForTest(t, subPackets[s], 0, body)
	}
	packet := rebuildMultistreamPacketForTest(t, subPackets)

	frameSize := 960
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode(carrier): %v", err)
	}
	if dec.dred == nil {
		t.Fatal("DRED sidecar not allocated after decode with DRED payload")
	}
	for _, s := range []int{0, 2} {
		if dec.dred.dredCache[s].Empty() {
			t.Fatalf("stream %d: expected DRED cache after carrier decode", s)
		}
	}
	if !dec.dred.dredCache[1].Empty() {
		t.Fatalf("stream 1: unexpected DRED cache (stream has no DRED payload)")
	}

	// Apply one loss
	if _, err := dec.Decode(nil, frameSize); err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}

	// Streams 0 and 2 must have DRED recovery; stream 1 must not.
	for s := 0; s < streams; s++ {
		blend := dec.dred.dredPLC[s].Blend()
		recovery := dec.dred.dredRecovery[s]
		switch s {
		case 0, 2:
			if blend != 1 {
				t.Fatalf("stream %d blend after loss=%d want 1", s, blend)
			}
			if recovery != frameSize {
				t.Fatalf("stream %d dredRecovery=%d want %d", s, recovery, frameSize)
			}
		default:
			if recovery != 0 {
				t.Fatalf("stream %d (no DRED) dredRecovery=%d want 0", s, recovery)
			}
		}
	}
}

// TestMSPerStreamDREDQueueCursorAdvancesPerLoss verifies that a stream's DRED
// recovery cursor (dredRecovery) advances by exactly frameSize per additional
// loss, matching the per-step offset arithmetic in libopus
// opus_decoder_dred_decode_float (opus_decoder.c:1593, `dred_offset` increments
// by frame_size per lost-ago step).
func TestMSPerStreamDREDQueueCursorAdvancesPerLoss(t *testing.T) {
	const (
		channels     = 3
		targetStream = 1
	)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeCELTMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	setDecoderComplexityForDREDParityTest(t, dec)
	modelBlob := makeLoadableDecoderDREDControlTestBlob(t)
	dec.SetDNNBlob(modelBlob)
	dec.setDREDDecoderBlob(modelBlob)

	frameSize := 960
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode(carrier): %v", err)
	}

	// Accumulate losses and check that the cursor advances by frameSize each time
	for loss := 1; loss <= 3; loss++ {
		if _, err := dec.Decode(nil, frameSize); err != nil {
			t.Fatalf("Decode(nil, loss %d): %v", loss, err)
		}
		want := loss * frameSize
		got := dec.dred.dredRecovery[targetStream]
		if got != want {
			t.Fatalf("loss %d: target stream dredRecovery=%d want %d", loss, got, want)
		}
		// non-target streams must remain at zero throughout
		for s, r := range dec.dred.dredRecovery {
			if s == targetStream {
				continue
			}
			if r != 0 {
				t.Fatalf("loss %d: non-target stream %d dredRecovery=%d want 0", loss, s, r)
			}
		}
	}

	// After re-decoding a good packet the cursor must reset to zero.
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode(good after losses): %v", err)
	}
	if got := dec.dred.dredRecovery[targetStream]; got != 0 {
		t.Fatalf("target stream dredRecovery after good decode=%d want 0", got)
	}
}

// TestMSPerStreamDREDQueueMatchesSingleStreamOracle verifies that the audio
// produced by the multistream decoder's per-stream DRED recovery (via
// Decode(nil)) matches the reference single-stream decoder running on the same
// stream's sub-packet.
//
// This mirrors the libopus guarantee that
// opus_multistream_decode_float(NULL,...) per stream produces the same audio as
// opus_decode_float(NULL,...) on that stream's sub-packet in isolation.
//
// Reference: opus_multistream.c opus_multistream_decode_float, libopus 1.6.1.
func TestMSPerStreamDREDQueueMatchesSingleStreamOracle(t *testing.T) {
	for _, channels := range []int{3, 6} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			streams, coupledStreams, mapping, err := DefaultMapping(channels)
			if err != nil {
				t.Fatalf("DefaultMapping: %v", err)
			}
			// target is the first non-coupled (mono) stream
			targetStream := coupledStreams

			body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
			packet := makeCELTMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

			modelBlob := makeLoadableDecoderDREDControlTestBlob(t)

			// Multi-stream decoder
			dec, err := NewDecoder(48000, channels, streams, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			setDecoderComplexityForDREDParityTest(t, dec)
			dec.SetDNNBlob(modelBlob)
			dec.setDREDDecoderBlob(modelBlob)

			// Single-stream oracle for the target stream
			targetChannels := streamChannels(targetStream, coupledStreams)
			targetCoupled := 0
			targetMapping := []byte{0}
			oracle, err := NewDecoder(48000, targetChannels, 1, targetCoupled, targetMapping)
			if err != nil {
				t.Fatalf("oracle NewDecoder: %v", err)
			}
			setDecoderComplexityForDREDParityTest(t, oracle)
			oracle.SetDNNBlob(modelBlob)
			oracle.setDREDDecoderBlob(modelBlob)

			// Extract the sub-packet for the target stream
			subPackets, err := parseMultistreamPacket(packet, streams)
			if err != nil {
				t.Fatalf("parseMultistreamPacket: %v", err)
			}
			targetSubPacket := subPackets[targetStream]

			frameSize := 960

			// Warm both decoders with the good packet
			msSamples, err := dec.Decode(packet, frameSize)
			if err != nil {
				t.Fatalf("Decode(MS carrier): %v", err)
			}
			if len(msSamples) != frameSize*channels {
				t.Fatalf("MS samples=%d want %d", len(msSamples), frameSize*channels)
			}
			oracleSamples, err := oracle.Decode(targetSubPacket, frameSize)
			if err != nil {
				t.Fatalf("Decode(oracle carrier): %v", err)
			}

			// Verify the target stream channels from MS match the oracle (good packet)
			mstarget := extractMappedStreamSamplesForTest(t, msSamples, frameSize, channels, mapping, coupledStreams, targetStream)
			assertFloat32ExactForTest(t, mstarget, oracleSamples, "target carrier vs oracle")

			// Apply one packet loss
			msPlcSamples, err := dec.Decode(nil, frameSize)
			if err != nil {
				t.Fatalf("Decode(nil): %v", err)
			}
			oraclePlcSamples, err := oracle.Decode(nil, frameSize)
			if err != nil {
				t.Fatalf("oracle Decode(nil): %v", err)
			}

			// Target stream audio from MS must match oracle DRED PLC
			msTargetPlc := extractMappedStreamSamplesForTest(t, msPlcSamples, frameSize, channels, mapping, coupledStreams, targetStream)
			assertFloat32ExactForTest(t, msTargetPlc, oraclePlcSamples, "target PLC vs oracle")
		})
	}
}

// TestMSPerStreamDREDQueueLibopusOracleRecoveryAudio validates multistream
// per-stream DRED recovery audio against the libopus reference multistream
// decoder oracle via decodeWithLibopusReferencePackets.
//
// Sequence: 4 good frames, 1 lost frame, 4 good frames. One stream has DRED;
// the oracle and gopus are fed the same sequence and their output PCM is
// compared at near-exact quality.
//
// Reference: opus_multistream_decode_float with nil packet (loss),
// opus_multistream.c (libopus 1.6.1).
func TestMSPerStreamDREDQueueLibopusOracleRecoveryAudio(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		channels   = 3
		sampleRate = 48000
		bitrate    = 192000
		numFrames  = 9
		lossFrame  = 4
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
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(bitrate)

	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
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

	// Build sequence: nil at lossFrame
	seq := make([][]byte, numFrames)
	copy(seq, packets)
	seq[lossFrame] = nil

	// libopus oracle
	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode", err)
	}

	// gopus
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
		t.Fatalf("sample count: got=%d want=%d", len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "MS DRED per-stream queue recovery audio")
}

// TestMSPerStreamDREDQueueCacheIsStreamScoped verifies that a DRED payload in
// stream S does not pollute the cache of stream S' ≠ S (zero-scope isolation).
func TestMSPerStreamDREDQueueCacheIsStreamScoped(t *testing.T) {
	const channels = 6
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	dec, err := NewDecoder(48000, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	setDecoderComplexityForDREDParityTest(t, dec)
	modelBlob := makeLoadableDecoderDREDControlTestBlob(t)
	dec.SetDNNBlob(modelBlob)
	dec.setDREDDecoderBlob(modelBlob)

	// Inject DRED only into stream 2
	const targetStream = 2
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	enc, err := NewEncoder(48000, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	rawPacket, err := enc.Encode(generateMultichannelSine(channels, 960), 960)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	subPackets, err := parseMultistreamPacket(rawPacket, streams)
	if err != nil {
		t.Fatalf("parseMultistreamPacket: %v", err)
	}
	subPackets[targetStream] = addDREDExtensionToOpusPacketFrameForTest(t, subPackets[targetStream], 0, body)
	packet := rebuildMultistreamPacketForTest(t, subPackets)

	if _, err := dec.Decode(packet, 960); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.dred == nil {
		t.Fatal("DRED sidecar not allocated")
	}

	for s := 0; s < streams; s++ {
		hasDRED := !dec.dred.dredCache[s].Empty()
		if s == targetStream {
			if !hasDRED {
				t.Fatalf("stream %d: expected DRED cache", s)
			}
			wantLen := len(body)
			if dec.dred.dredCache[s].Len != wantLen {
				t.Fatalf("stream %d: cache.Len=%d want %d", s, dec.dred.dredCache[s].Len, wantLen)
			}
		} else {
			if hasDRED {
				t.Fatalf("stream %d: unexpected DRED cache=%+v (DRED was only in stream %d)", s, dec.dred.dredCache[s], targetStream)
			}
		}
	}

	// After Reset, all streams must have empty caches
	dec.Reset()
	for s := 0; s < streams; s++ {
		if dec.dred != nil && !dec.dred.dredCache[s].Empty() {
			t.Fatalf("stream %d: cache not empty after Reset", s)
		}
		if got := dec.cachedDREDMaxAvailableSamples(s, 960); got != 0 {
			t.Fatalf("stream %d: cachedDREDMaxAvailableSamples after Reset=%d want 0", s, got)
		}
	}
}

// generateMultichannelSine is already defined in ms_recovery_queue_libopus_parity_test.go.
// internaldred is imported for the Cache zero-value check.
var _ internaldred.Cache
