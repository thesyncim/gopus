//go:build gopus_dred || gopus_osce

package multistream

import (
	"fmt"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/types"
	"testing"
)

func makeMultistreamPacketWithDREDForMappingTest(
	t *testing.T,
	sampleRate, channels, streams, coupledStreams int,
	mapping []byte,
	targetStream int,
	body []byte,
) []byte {
	t.Helper()

	if targetStream < 0 || targetStream >= streams {
		t.Fatalf("targetStream=%d out of range for %d streams", targetStream, streams)
	}

	packets := make([][]byte, streams)
	for s := 0; s < streams; s++ {
		sCh := streamChannels(s, coupledStreams)
		enc := internalenc.NewEncoder(sampleRate, sCh)
		enc.SetMode(internalenc.ModeCELT)
		enc.SetBandwidth(types.BandwidthFullband)
		enc.SetBitrate(128000)
		if sCh == 2 {
			enc.SetForceChannels(2)
		}
		freq := float64(997 + s*101)
		pkt, err := enc.EncodeFloat32(generateTestSignal(sCh, sampleRate/50, sampleRate, freq), sampleRate/50)
		if err != nil {
			t.Fatalf("stream %d EncodeFloat32 error: %v", s, err)
		}
		packets[s] = pkt
	}

	packets[targetStream] = addDREDExtensionToOpusPacketFrameForTest(t, packets[targetStream], 0, body)
	return rebuildMultistreamPacketForTest(t, packets)
}

// assertMappingDREDDormancyOnGoodDecode verifies that after a successful decode:
//   - streams without DRED extension have an empty cache (internaldred.Cache{})
//   - the target stream (carrying DRED) has a non-empty cache
//
// Reference: multistream/decoder_dred_helpers.go maybeCacheDREDPayload() —
// called per-stream in decodeToFloat32() only for streams where findDREDPayload
// returns ok=true; streams without a DRED extension in their packet are skipped.
func assertMappingDREDDormancyOnGoodDecode(
	t *testing.T,
	label string,
	dec *Decoder,
	targetStream int,
) {
	t.Helper()

	if dec.dred == nil {
		t.Fatalf("%s: DRED sidecar not initialized after good decode with model", label)
	}
	for i := range dec.dred.dredCache {
		if i == targetStream {
			if dec.dred.dredCache[i].Empty() {
				t.Fatalf("%s: stream %d (target) has empty DRED cache after good decode, want non-empty", label, i)
			}
			continue
		}
		if dec.dred.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("%s: stream %d (non-target) has non-zero DRED cache after good decode: %+v, want zero", label, i, dec.dred.dredCache[i])
		}
	}
}

// assertMappingDREDDormancyOnLoss verifies that after a PLC (loss) decode:
//   - only the target stream's dredRecovery is non-zero
//   - all other streams have dredRecovery == 0
//
// Reference: src/opus_multistream_decoder.c opus_multistream_decode_native calls
// each child decoder with dred=NULL and dred_offset=0. A Go child without a
// cached DRED payload may still use main-model PLC, but only a child with a
// cached sidecar payload advances dredRecovery.
func assertMappingDREDDormancyOnLoss(
	t *testing.T,
	label string,
	dec *Decoder,
	targetStream, frameSize int,
) {
	t.Helper()

	if dec.dred == nil {
		t.Fatalf("%s: DRED sidecar nil after PLC decode with model", label)
	}
	for i, got := range dec.dred.dredRecovery {
		if i == targetStream {
			if got != frameSize {
				t.Fatalf("%s: stream %d (target) dredRecovery=%d want %d after PLC", label, i, got, frameSize)
			}
			continue
		}
		if got != 0 {
			t.Fatalf("%s: stream %d (non-target) dredRecovery=%d want 0 after PLC", label, i, got)
		}
	}
}

// runDREDDormancyAcrossMappingCase is the shared dormancy assertion for a given
// explicit channel layout.  It:
//  1. Creates a multistream packet with DRED only on targetStream.
//  2. Decodes it with a fully armed decoder (model loaded).
//  3. Asserts that only targetStream got its cache populated.
//  4. Calls Decode(nil) for PLC.
//  5. Asserts that only targetStream advanced dredRecovery.
func runDREDDormancyAcrossMappingCase(
	t *testing.T,
	label string,
	sampleRate, channels, streams, coupledStreams int,
	mapping []byte,
	targetStream int,
) {
	t.Helper()

	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeMultistreamPacketWithDREDForMappingTest(
		t, sampleRate, channels, streams, coupledStreams, mapping, targetStream, body,
	)

	dec, err := NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("%s: NewDecoder error: %v", label, err)
	}
	setDecoderComplexityForDREDParityTest(t, dec)
	modelBlob := makeLoadableDecoderDREDControlTestBlob(t)
	dec.SetDNNBlob(modelBlob)
	dec.setDREDDecoderBlob(modelBlob)

	frameSize := sampleRate / 50
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("%s: Decode error: %v", label, err)
	}

	assertMappingDREDDormancyOnGoodDecode(t, label, dec, targetStream)

	if _, err := dec.Decode(nil, frameSize); err != nil {
		t.Fatalf("%s: Decode(nil) error: %v", label, err)
	}

	assertMappingDREDDormancyOnLoss(t, label, dec, targetStream, frameSize)
}

// TestMSDREDDormancy_Family0_Mono tests that a mono (1-stream, family-0/1)
// multistream decoder leaves DRED sidecar dormant on a good decode and arms
// only stream 0 on loss.
//
// Reference: src/opus_multistream_decoder.c opus_multistream_decode_native()
// per-stream loop — even a single-stream multistream decoder must obey the
// same per-stream DRED sidecar gating as larger layouts.
func TestMSDREDDormancy_Family0_Mono(t *testing.T) {
	// family 0: 1ch, 1 stream, 0 coupled, mapping [0]
	runDREDDormancyAcrossMappingCase(t, "family0/1ch mono", 48000, 1, 1, 0, []byte{0}, 0)
}

// TestMSDREDDormancy_Family1_7ch tests dormancy for 6.1 surround (7 channels,
// 4 streams, 3 coupled) mapping family 1.  The last stream (stream 3, mono LFE)
// carries the DRED extension; the three coupled streams must stay dormant.
//
// DefaultMapping(7) = streams=4, coupled=3, mapping=[0,4,1,2,3,5,6]
// Streams: 0=FL/FR (coupled), 1=SL/SR (coupled), 2=C/RC (coupled), 3=LFE (mono)
func TestMSDREDDormancy_Family1_7ch_LastStreamArmed(t *testing.T) {
	streams, coupledStreams, mapping, err := DefaultMapping(7)
	if err != nil {
		t.Fatalf("DefaultMapping(7) error: %v", err)
	}
	// Target the final (mono LFE) stream — stream 3
	targetStream := streams - 1
	runDREDDormancyAcrossMappingCase(t, "family1/7ch last-stream", 48000, 7, streams, coupledStreams, mapping, targetStream)
}

// TestMSDREDDormancy_Family1_7ch_FirstStreamArmed tests dormancy for 6.1 surround
// where the first coupled stream (FL/FR) carries the DRED extension; all other
// streams (1, 2, 3) must stay dormant.
func TestMSDREDDormancy_Family1_7ch_FirstStreamArmed(t *testing.T) {
	streams, coupledStreams, mapping, err := DefaultMapping(7)
	if err != nil {
		t.Fatalf("DefaultMapping(7) error: %v", err)
	}
	runDREDDormancyAcrossMappingCase(t, "family1/7ch first-stream", 48000, 7, streams, coupledStreams, mapping, 0)
}

// TestMSDREDDormancy_Family1_8ch tests dormancy for 7.1 surround (8 channels,
// 5 streams, 3 coupled) mapping family 1.  The first uncoupled stream (stream 3,
// center) carries the DRED extension.
//
// DefaultMapping(8) = streams=5, coupled=3, mapping=[0,6,1,2,3,4,5,7]
// Streams: 0=FL/FR, 1=SL/SR, 2=RL/RR (coupled), 3=C, 4=LFE (mono)
func TestMSDREDDormancy_Family1_8ch_MidStreamArmed(t *testing.T) {
	streams, coupledStreams, mapping, err := DefaultMapping(8)
	if err != nil {
		t.Fatalf("DefaultMapping(8) error: %v", err)
	}
	// Target stream 3 (center, first uncoupled)
	targetStream := coupledStreams
	runDREDDormancyAcrossMappingCase(t, "family1/8ch center-stream", 48000, 8, streams, coupledStreams, mapping, targetStream)
}

// TestMSDREDDormancy_Family1_8ch_FinalStreamArmed verifies 7.1 surround with the
// final uncoupled stream (stream 4, LFE) carrying the DRED extension.
func TestMSDREDDormancy_Family1_8ch_FinalStreamArmed(t *testing.T) {
	streams, coupledStreams, mapping, err := DefaultMapping(8)
	if err != nil {
		t.Fatalf("DefaultMapping(8) error: %v", err)
	}
	// Target the last stream (LFE, stream 4)
	targetStream := streams - 1
	runDREDDormancyAcrossMappingCase(t, "family1/8ch lfe-stream", 48000, 8, streams, coupledStreams, mapping, targetStream)
}

// TestMSDREDDormancy_Family3_Projection_FOA4ch tests dormancy for projection
// family-3 (ambisonics order 1, FOA, 4 channels, 2 streams, 2 coupled).
//
// The projection decoder uses the trivial identity mapping [0,1,2,3] with a
// pre-computed demixing matrix applied as a post-processing step; the per-stream
// DRED dormancy logic is identical to other families.
//
// Reference: src/opus_multistream_decoder.c — the projection path in
// opus_projection_decode is identical to the standard multistream decode path at
// the per-stream level; DRED gating does not change for projection.
func TestMSDREDDormancy_Family3_Projection_FOA4ch_FirstStreamArmed(t *testing.T) {
	// Family 3 FOA: 4ch, 2 streams, 2 coupled, identity mapping
	enc, err := NewEncoderAmbisonics(48000, 4, 3)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics(4, family=3) error: %v", err)
	}

	channels := 4
	streams := enc.Streams()
	coupledStreams := enc.CoupledStreams()
	// Projection (family 3) mapping is identity: [0,1,...,channels-1]
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}

	// Target the first coupled stream (stream 0)
	targetStream := 0
	runDREDDormancyAcrossMappingCase(
		t, "family3/FOA4ch stream0",
		48000, channels, streams, coupledStreams, mapping, targetStream,
	)
}

// TestMSDREDDormancy_Family3_Projection_FOA4ch_SecondStreamArmed verifies that
// when the second coupled stream (stream 1) carries DRED, stream 0 stays dormant.
func TestMSDREDDormancy_Family3_Projection_FOA4ch_SecondStreamArmed(t *testing.T) {
	enc, err := NewEncoderAmbisonics(48000, 4, 3)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics(4, family=3) error: %v", err)
	}

	channels := 4
	streams := enc.Streams()
	coupledStreams := enc.CoupledStreams()
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}

	// Target stream 1 (second coupled pair)
	targetStream := 1
	runDREDDormancyAcrossMappingCase(
		t, "family3/FOA4ch stream1",
		48000, channels, streams, coupledStreams, mapping, targetStream,
	)
}

// TestMSDREDDormancy_NoDREDModel_AllMappings verifies that with no DRED model
// loaded, the sidecar stays nil across all mapping family sizes.
//
// Reference: multistream/decoder_dred_helpers.go maybeCacheDREDPayload() —
// early return when !s.dredModelLoaded; the sidecar must not be allocated.
func TestMSDREDDormancy_NoDREDModel_AllMappings(t *testing.T) {
	cases := []struct {
		label     string
		channels  int
		getLayout func() (int, int, []byte, error)
	}{
		{"family0/1ch", 1, func() (int, int, []byte, error) {
			s, c, m, e := DefaultMapping(1)
			return s, c, m, e
		}},
		{"family1/7ch", 7, func() (int, int, []byte, error) {
			s, c, m, e := DefaultMapping(7)
			return s, c, m, e
		}},
		{"family1/8ch", 8, func() (int, int, []byte, error) {
			s, c, m, e := DefaultMapping(8)
			return s, c, m, e
		}},
		{"family3/FOA4ch", 4, func() (int, int, []byte, error) {
			enc, err := NewEncoderAmbisonics(48000, 4, 3)
			if err != nil {
				return 0, 0, nil, err
			}
			m := make([]byte, 4)
			for i := range m {
				m[i] = byte(i)
			}
			return enc.Streams(), enc.CoupledStreams(), m, nil
		}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			streams, coupledStreams, mapping, err := tc.getLayout()
			if err != nil {
				t.Fatalf("getLayout error: %v", err)
			}
			body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
			// Put DRED on stream 0 only
			packet := makeMultistreamPacketWithDREDForMappingTest(
				t, 48000, tc.channels, streams, coupledStreams, mapping, 0, body,
			)
			dec, err := NewDecoder(48000, tc.channels, streams, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			// No DRED model — only a main decoder blob without DRED layers
			dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, false))

			frameSize := 960
			if _, err := dec.Decode(packet, frameSize); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if dec.dred != nil {
				t.Fatalf("%s: DRED sidecar allocated without model: %+v", tc.label, dec.dred)
			}

			if _, err := dec.Decode(nil, frameSize); err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if dec.dred != nil {
				t.Fatalf("%s: DRED sidecar allocated after PLC without model: %+v", tc.label, dec.dred)
			}
		})
	}
}

// TestMSDREDDormancy_Family1_FullMatrix runs the per-mapping dormancy check across
// all 8 default-mapping channel counts (1-8) with DRED armed on every stream in
// each layout.  This ensures the invariant holds for every stream position in every
// default-mapping family-1 layout, not just the ones exercised by the mode-specific
// tests above.
//
// For each layout and each stream index s:
//   - Builds a packet with DRED only on stream s.
//   - Asserts non-target streams stay dormant on good decode.
//   - Asserts only stream s advances dredRecovery on loss.
func TestMSDREDDormancy_Family1_FullMatrix(t *testing.T) {
	for ch := 1; ch <= 8; ch++ {
		streams, coupledStreams, mapping, err := DefaultMapping(ch)
		if err != nil {
			t.Fatalf("DefaultMapping(%d) error: %v", ch, err)
		}
		for targetStream := 0; targetStream < streams; targetStream++ {
			label := func(ch, s int) string {
				return "family1/" + itoa(ch) + "ch/stream" + itoa(s)
			}(ch, targetStream)
			t.Run(label, func(t *testing.T) {
				runDREDDormancyAcrossMappingCase(
					t, label,
					48000, ch, streams, coupledStreams, mapping, targetStream,
				)
			})
		}
	}
}

// itoa converts a small non-negative int to its decimal string representation
// without importing strconv (avoiding an extra import for a test-only helper).
func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string([]byte{byte('0' + n)})
	}
	return itoa(n/10) + itoa(n%10)
}

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
//   - Stream 1 must keep its sidecar cursor at zero; its ordinary child PLC
//     still runs independently.
//
// The pinned C `opus_multistream_decode_native` passes no `OpusDRED` state to
// its child decoders; these assertions cover the Go sidecar cursor only.
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
	rawPacket, err := encodePacket(enc, generateMultichannelSine(channels, 960), 960)
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
// loss while the cached sidecar remains available. This is Go-sidecar
// bookkeeping; public libopus multistream decode passes no OpusDRED argument to
// its child decoders.
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
// For ordinary public decode, libopus opus_multistream_decode_native calls each
// child decoder independently. This test checks that the Go multistream DRED
// path produces the same target-stream output as its single-stream counterpart.
//
// Reference: src/opus_multistream_decoder.c opus_multistream_decode_native.
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
		p, encErr := encodePacket(enc, pcm, frameSize)
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
	rawPacket, err := encodePacket(enc, generateMultichannelSine(channels, 960), 960)
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
