//go:build gopus_dred || gopus_extra_controls

package multistream

// Per-mapping DRED dormancy tests for gopus multistream decoder.
//
// Reference: src/opus_multistream_decoder.c opus_multistream_decode_native() —
// the per-stream decode loop (s=0..nb_streams-1) at lines 238-295 applies
// opus_decode_native() to each stream unconditionally; DRED payload presence
// per-stream is a gopus sidecar concern that must remain dormant on streams that
// carry no DRED extension, regardless of channel-mapping family or channel count.
//
// Dormancy invariants verified here for ALL mapping families (0, 1, 255, 3):
//   1. Good decode: DRED sidecar stays nil when no model is loaded (default build).
//   2. Good decode with model: only the stream carrying the DRED extension gets its
//      cache populated; all other streams remain at zero (internaldred.Cache{}).
//   3. Loss (PLC) decode after a good DRED-carrying decode: only the target stream
//      gets dredRecovery incremented; all other streams remain at zero.
//
// Channel/mapping coverage extends existing tests (channels 2-6) to:
//   - family 0/1: mono (1ch), 7ch (6.1 surround), 8ch (7.1 surround)
//   - family 3 (projection): FOA 4ch with identity mapping

import (
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

// makeMultistreamPacketWithDREDForMappingTest builds a multistream packet for the
// given explicit mapping (streams, coupledStreams, mapping) where only the stream at
// targetStream carries a DRED extension body.
//
// It encodes each stream independently with a CELT encoder and injects the provided
// DRED body into only the target stream packet.
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
// Reference: multistream/multistream.go decodePLCChunkToFloat32() —
// calls decodeDREDPLCStream(i, frameSize) per-stream; streams without a cached
// DRED payload return (nil, false, nil) so their dredRecovery stays at zero.
// Only the target stream advances its dredRecovery counter.
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
