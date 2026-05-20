package main

import (
	"io"
	"testing"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

func TestREDPayloadRoundTrip(t *testing.T) {
	history := []redHistoryFrame{
		{timestamp: 2000, payload: []byte{0x11, 0x22}},
		{timestamp: 1040, payload: []byte{0x33, 0x44, 0x55}},
	}
	primary := []byte{0xaa, 0xbb, 0xcc}
	payload, redundantBytes := buildREDPayload(primary, 2960, history, 2, 960)
	if redundantBytes != 5 {
		t.Fatalf("redundantBytes=%d want 5", redundantBytes)
	}
	gotPrimary, blocks, err := parseREDPayload(payload)
	if err != nil {
		t.Fatalf("parseREDPayload error: %v", err)
	}
	if string(gotPrimary) != string(primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks=%d want 2", len(blocks))
	}
	if got := findREDRecoveryBlock(blocks, 1, 960); string(got) != string(history[0].payload) {
		t.Fatalf("lostAgo=1 payload=%x want %x", got, history[0].payload)
	}
	if got := findREDRecoveryBlock(blocks, 2, 960); string(got) != string(history[1].payload) {
		t.Fatalf("lostAgo=2 payload=%x want %x", got, history[1].payload)
	}
}

func TestREDPrimaryOnlyRoundTrip(t *testing.T) {
	primary := []byte{0xf8, 0xff, 0xfe}
	payload, redundantBytes := buildREDPayload(primary, 960, nil, 2, 960)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}
	gotPrimary, blocks, err := parseREDPayload(payload)
	if err != nil {
		t.Fatalf("parseREDPayload error: %v", err)
	}
	if string(gotPrimary) != string(primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks=%d want 0", len(blocks))
	}
}

func TestBuildREDPayloadDisabledLeavesOpusPayloadUnwrapped(t *testing.T) {
	primary := []byte{0x80, 0x01}
	payload, redundantBytes := buildREDPayload(primary, 960, nil, 0, 960)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}
	if string(payload) != string(primary) {
		t.Fatalf("payload=%x want raw Opus %x", payload, primary)
	}
}

func TestBuildREDPayloadInvalidFrameSamplesKeepsREDEnvelope(t *testing.T) {
	primary := []byte{0x80, 0x01}
	payload, redundantBytes := buildREDPayload(primary, 960, nil, 1, 0)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}
	gotPrimary, blocks, err := parseREDPayload(payload)
	if err != nil {
		t.Fatalf("parseREDPayload error: %v", err)
	}
	if string(gotPrimary) != string(primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks=%d want 0", len(blocks))
	}
}

func TestParseREDPayloadRejectsUnexpectedPayloadTypes(t *testing.T) {
	if _, _, err := parseREDPayload([]byte{0x60, 0x01}); err == nil {
		t.Fatal("parseREDPayload accepted unexpected primary payload type")
	}
	if _, _, err := parseREDPayload([]byte{0x80 | 0x60, 0x0f, 0x00, 0x01, redOpusPayloadType, 0xaa, 0xbb}); err == nil {
		t.Fatal("parseREDPayload accepted unexpected redundant payload type")
	}
}

func TestParseREDPayloadRejectsMalformedPayloads(t *testing.T) {
	tooManyHeaders := make([]byte, 0, (maxREDDepth+1)*4+1)
	for i := 0; i < maxREDDepth+1; i++ {
		tooManyHeaders = append(tooManyHeaders, 0x80|redOpusPayloadType, 0x00, 0x04, 0x01)
	}
	tooManyHeaders = append(tooManyHeaders, redOpusPayloadType)

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: nil},
		{name: "truncated_redundant_header", payload: []byte{0x80 | redOpusPayloadType, 0x00}},
		{name: "zero_offset", payload: []byte{0x80 | redOpusPayloadType, 0x00, 0x00, 0x01, redOpusPayloadType, 0xaa, 0xbb}},
		{name: "zero_length", payload: []byte{0x80 | redOpusPayloadType, 0x00, 0x04, 0x00, redOpusPayloadType, 0xaa}},
		{name: "too_many_headers", payload: tooManyHeaders},
		{name: "truncated_redundant_payload", payload: []byte{0x80 | redOpusPayloadType, 0x00, 0x04, 0x02, redOpusPayloadType, 0xaa}},
		{name: "missing_primary", payload: []byte{redOpusPayloadType}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseREDPayload(tc.payload); err == nil {
				t.Fatal("parseREDPayload accepted malformed payload")
			}
		})
	}
}

func TestReceiveLoopMissingPacketsUsePLC(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(10, redOpusPayloadType, []byte{0x01}),
		testPacket(12, redOpusPayloadType, []byte{0x02}),
	)

	want := []decodeKind{decodeNormal, decodeLossPath, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.LossPathFrames != 1 || stats.ConcealedFrames != 1 || stats.PacketsReceived != 2 {
		t.Fatalf("stats loss=%d concealed=%d received=%d, want 1/1/2", stats.LossPathFrames, stats.ConcealedFrames, stats.PacketsReceived)
	}
}

func TestReceiveLoopMultipleMissingPacketsUseMatchingREDBlocks(t *testing.T) {
	var calls []string
	var recovered [][]byte
	e := newReceiveLoopTestEngine(engineConfig{RED: true})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeREDHook = func(payload []byte) bool {
		calls = append(calls, "red")
		recovered = append(recovered, append([]byte(nil), payload...))
		e.bumpDecodeStats(decodeRED)
		return true
	}

	redPayload, _ := buildREDPayload(
		[]byte{0xb3},
		103*frameSamples,
		[]redHistoryFrame{
			{timestamp: 102 * frameSamples, payload: []byte{0xa2}},
			{timestamp: 101 * frameSamples, payload: []byte{0xa1}},
		},
		2,
		frameSamples,
	)
	runReceiveLoopTest(t, e,
		testPacket(100, redOpusPayloadType, []byte{0x01}),
		testPacket(103, redPayloadType, redPayload),
	)

	wantCalls := []string{decodeNormal.String(), "red", "red", decodeNormal.String()}
	if !sameStrings(calls, wantCalls) {
		t.Fatalf("calls=%v want %v", calls, wantCalls)
	}
	if len(recovered) != 2 || string(recovered[0]) != string([]byte{0xa1}) || string(recovered[1]) != string([]byte{0xa2}) {
		t.Fatalf("recovered=%x want [a1 a2]", recovered)
	}
	stats := e.Stats()
	if stats.REDFrames != 2 || stats.REDRecoveryAttempts != 2 || stats.LossPathFrames != 0 || stats.FECRecoveryAttempts != 0 || stats.DREDRecoveryAttempts != 0 {
		t.Fatalf("stats red=%d/%d plc=%d fec=%d dred=%d, want red 2/2 only",
			stats.REDFrames, stats.REDRecoveryAttempts, stats.LossPathFrames, stats.FECRecoveryAttempts, stats.DREDRecoveryAttempts)
	}
}

func TestReceiveLoopMalformedREDConcealsWithoutPoisoningSequenceAccounting(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{RED: true})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(20, redPayloadType, []byte{0x80 | redOpusPayloadType, 0x00, 0x00, 0x01}),
		testPacket(21, redOpusPayloadType, []byte{0x02}),
	)

	stats := e.Stats()
	if stats.DecodeErrors != 1 || stats.REDFallbackFrames != 1 {
		t.Fatalf("red malformed stats decodeErrors=%d redFallback=%d, want 1/1", stats.DecodeErrors, stats.REDFallbackFrames)
	}
	if stats.LossPathFrames != 1 || stats.ConcealedFrames != 1 {
		t.Fatalf("lossPathFrames=%d concealed=%d want 1/1", stats.LossPathFrames, stats.ConcealedFrames)
	}
	want := []decodeKind{decodeLossPath, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
}

func TestReceiveLoopMalformedREDWithGapUsesPLCOnly(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
	e.fecEnabledHook = func(_ []byte) bool {
		t.Fatal("FEC should not inspect malformed RED")
		return false
	}
	e.prepareDREDHook = func(_ []byte, _ int) (int, bool) {
		t.Fatal("DRED should not prepare from malformed RED")
		return 0, false
	}
	e.decodeREDHook = func(_ []byte) bool {
		t.Fatal("RED should not decode malformed RED")
		return false
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(10, redPayloadType, mustREDPayload(t, []byte{0x01}, nil)),
		testPacket(12, redPayloadType, []byte{0x80 | redOpusPayloadType, 0x00, 0x00, 0x01}),
		testPacket(13, redPayloadType, mustREDPayload(t, []byte{0x03}, nil)),
	)

	want := []decodeKind{decodeNormal, decodeLossPath, decodeLossPath, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.DecodeErrors != 1 || stats.REDFallbackFrames != 1 || stats.LossPathFrames != 2 || stats.ConcealedFrames != 2 {
		t.Fatalf("stats decodeErrors=%d redFallback=%d loss=%d concealed=%d, want 1/1/2/2",
			stats.DecodeErrors, stats.REDFallbackFrames, stats.LossPathFrames, stats.ConcealedFrames)
	}
}

func TestReceiveLoopMalformedREDVariantsUsePLC(t *testing.T) {
	tooManyHeaders := make([]byte, 0, (maxREDDepth+1)*4+1)
	for i := 0; i < maxREDDepth+1; i++ {
		tooManyHeaders = append(tooManyHeaders, 0x80|redOpusPayloadType, 0x00, 0x04, 0x01)
	}
	tooManyHeaders = append(tooManyHeaders, redOpusPayloadType)

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "unexpected_primary_pt", payload: []byte{0x60, 0x01}},
		{name: "unexpected_redundant_pt", payload: []byte{0x80 | 0x60, 0x0f, 0x00, 0x01, redOpusPayloadType, 0xaa, 0xbb}},
		{name: "truncated_redundant_payload", payload: []byte{0x80 | redOpusPayloadType, 0x00, 0x04, 0x02, redOpusPayloadType, 0xaa}},
		{name: "too_many_headers", payload: tooManyHeaders},
		{name: "missing_primary", payload: []byte{redOpusPayloadType}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []decodeKind
			e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
			e.fecEnabledHook = func(_ []byte) bool {
				t.Fatal("FEC should not inspect malformed RED")
				return false
			}
			e.prepareDREDHook = func(_ []byte, _ int) (int, bool) {
				t.Fatal("DRED should not prepare from malformed RED")
				return 0, false
			}
			e.decodeREDHook = func(_ []byte) bool {
				t.Fatal("RED should not decode malformed RED")
				return false
			}
			e.decodeHook = func(_ []byte, kind decodeKind) bool {
				calls = append(calls, kind)
				e.bumpDecodeStats(kind)
				return true
			}

			runReceiveLoopTest(t, e,
				testPacket(30, redPayloadType, tc.payload),
				testPacket(31, redPayloadType, mustREDPayload(t, []byte{0x31}, nil)),
			)

			want := []decodeKind{decodeLossPath, decodeNormal}
			if !sameDecodeKinds(calls, want) {
				t.Fatalf("decode calls=%v want %v", calls, want)
			}
			stats := e.Stats()
			if stats.DecodeErrors != 1 || stats.REDFallbackFrames != 1 || stats.LossPathFrames != 1 || stats.ConcealedFrames != 1 || stats.PacketsReceived != 1 {
				t.Fatalf("stats decodeErrors=%d redFallback=%d loss=%d concealed=%d received=%d, want 1/1/1/1/1",
					stats.DecodeErrors, stats.REDFallbackFrames, stats.LossPathFrames, stats.ConcealedFrames, stats.PacketsReceived)
			}
			if stats.REDRecoveryAttempts != 0 || stats.FECRecoveryAttempts != 0 || stats.DREDRecoveryAttempts != 0 {
				t.Fatalf("recovery attempts red=%d fec=%d dred=%d, want 0/0/0",
					stats.REDRecoveryAttempts, stats.FECRecoveryAttempts, stats.DREDRecoveryAttempts)
			}
		})
	}
}

func TestReceiveLoopREDFECDREDPriority(t *testing.T) {
	var calls []string
	e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
	e.fecEnabledHook = func(_ []byte) bool {
		t.Fatal("FEC should not be probed when RED carries the missing packet")
		return false
	}
	e.prepareDREDHook = func(_ []byte, maxDREDSamples int) (int, bool) {
		t.Fatal("DRED should not prepare when RED carries the missing packet")
		return 0, false
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		if kind == decodeFEC {
			t.Fatal("FEC should not run when RED carries the missing packet")
		}
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeREDHook = func(_ []byte) bool {
		calls = append(calls, "red")
		e.mu.Lock()
		e.stats.REDRecoveryAttempts++
		e.stats.REDFrames++
		e.stats.ConcealedFrames++
		e.mu.Unlock()
		return true
	}
	e.decodeDREDHook = func(int) bool {
		t.Fatal("DRED should not run after RED recovers the packet")
		return false
	}

	redPayload, _ := buildREDPayload(
		[]byte{0xb2},
		2*frameSamples,
		[]redHistoryFrame{{timestamp: frameSamples, payload: []byte{0xa1}}},
		1,
		frameSamples,
	)
	runReceiveLoopTest(t, e,
		testPacket(100, redPayloadType, mustREDPayload(t, []byte{0x01}, nil)),
		testPacket(102, redPayloadType, redPayload),
	)

	want := []string{decodeNormal.String(), "red", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.FECRecoveryAttempts != 0 || stats.FECFallbackFrames != 0 || stats.REDRecoveryAttempts != 1 || stats.REDFrames != 1 || stats.DREDRecoveryAttempts != 0 || stats.LossPathFrames != 0 {
		t.Fatalf("stats fec=%d/%d red=%d/%d dred=%d plc=%d, want fec 0/0 red 1/1 dred 0 plc 0",
			stats.FECRecoveryAttempts, stats.FECFallbackFrames, stats.REDRecoveryAttempts, stats.REDFrames, stats.DREDRecoveryAttempts, stats.LossPathFrames)
	}
}

func TestReceiveLoopMixedGapUsesREDThenFECWithoutDRED(t *testing.T) {
	var calls []string
	var redPayloads [][]byte
	var fecPayload []byte
	e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
	e.fecEnabledHook = func(payload []byte) bool {
		if string(payload) != string([]byte{0xb3}) {
			t.Fatalf("FEC probe payload=%x want parsed RED primary b3", payload)
		}
		return true
	}
	e.prepareDREDHook = func(_ []byte, _ int) (int, bool) {
		t.Fatal("DRED should not prepare after RED and FEC recover the gap")
		return 0, false
	}
	e.decodeDREDHook = func(int) bool {
		t.Fatal("DRED should not run after RED and FEC recover the gap")
		return false
	}
	e.decodeHook = func(payload []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		if kind == decodeFEC {
			fecPayload = append([]byte(nil), payload...)
		}
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeREDHook = func(payload []byte) bool {
		calls = append(calls, "red")
		redPayloads = append(redPayloads, append([]byte(nil), payload...))
		e.bumpDecodeStats(decodeRED)
		return true
	}

	redPayload, _ := buildREDPayload(
		[]byte{0xb3},
		103*frameSamples,
		[]redHistoryFrame{{timestamp: 101 * frameSamples, payload: []byte{0xa1}}},
		2,
		frameSamples,
	)
	runReceiveLoopTest(t, e,
		testPacket(100, redPayloadType, mustREDPayload(t, []byte{0x01}, nil)),
		testPacket(103, redPayloadType, redPayload),
	)

	want := []string{decodeNormal.String(), "red", decodeFEC.String(), decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	if len(redPayloads) != 1 || string(redPayloads[0]) != string([]byte{0xa1}) {
		t.Fatalf("RED payloads=%x want [a1]", redPayloads)
	}
	if string(fecPayload) != string([]byte{0xb3}) {
		t.Fatalf("FEC payload=%x want parsed RED primary b3", fecPayload)
	}
	stats := e.Stats()
	if stats.REDFrames != 1 || stats.REDRecoveryAttempts != 1 || stats.FECFrames != 1 || stats.FECRecoveryAttempts != 1 ||
		stats.DREDRecoveryAttempts != 0 || stats.DREDFrames != 0 || stats.LossPathFrames != 0 ||
		stats.REDFallbackFrames != 0 || stats.FECFallbackFrames != 0 || stats.DREDFallbackFrames != 0 {
		t.Fatalf("stats red=%d/%d fec=%d/%d dred=%d/%d plc=%d fallbacks=%d/%d/%d, want red 1/1 fec 1/1 only",
			stats.REDFrames, stats.REDRecoveryAttempts, stats.FECFrames, stats.FECRecoveryAttempts,
			stats.DREDFrames, stats.DREDRecoveryAttempts, stats.LossPathFrames,
			stats.REDFallbackFrames, stats.FECFallbackFrames, stats.DREDFallbackFrames)
	}
}

func TestReceiveLoopMultiGapUsesDREDForEveryCoveredPacket(t *testing.T) {
	var calls []string
	var offsets []int
	prepareCalls := 0
	e := newReceiveLoopTestEngine(engineConfig{DRED: true})
	e.fecEnabledHook = func(_ []byte) bool {
		t.Fatal("FEC should remain disabled")
		return false
	}
	e.prepareDREDHook = func(_ []byte, maxDREDSamples int) (int, bool) {
		prepareCalls++
		if maxDREDSamples != 2*frameSamples {
			t.Fatalf("maxDREDSamples=%d want %d", maxDREDSamples, 2*frameSamples)
		}
		return maxDREDSamples, true
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		if kind == decodeLossPath {
			t.Fatal("PLC should not run when DRED covers the full gap")
		}
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeDREDHook = func(offset int) bool {
		calls = append(calls, "dred")
		offsets = append(offsets, offset)
		e.bumpDecodeStats(decodeDRED)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(100, redOpusPayloadType, []byte{0x01}),
		testPacket(103, redOpusPayloadType, []byte{0x03}),
	)

	want := []string{decodeNormal.String(), "dred", "dred", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	if prepareCalls != 1 {
		t.Fatalf("prepareCalls=%d want 1", prepareCalls)
	}
	if len(offsets) != 2 || offsets[0] != 2*frameSamples || offsets[1] != frameSamples {
		t.Fatalf("dred offsets=%v want [%d %d]", offsets, 2*frameSamples, frameSamples)
	}
	stats := e.Stats()
	if stats.DREDFrames != 2 || stats.DREDRecoveryAttempts != 2 || stats.DREDFallbackFrames != 0 || stats.LossPathFrames != 0 {
		t.Fatalf("stats dred=%d/%d fallback=%d plc=%d, want 2/2/0/0",
			stats.DREDFrames, stats.DREDRecoveryAttempts, stats.DREDFallbackFrames, stats.LossPathFrames)
	}
}

func TestReceiveLoopPartialDREDCoverageFallsBackPerPacket(t *testing.T) {
	var calls []string
	var offsets []int
	prepareCalls := 0
	e := newReceiveLoopTestEngine(engineConfig{DRED: true})
	e.prepareDREDHook = func(_ []byte, maxDREDSamples int) (int, bool) {
		prepareCalls++
		if maxDREDSamples != 2*frameSamples {
			t.Fatalf("maxDREDSamples=%d want %d", maxDREDSamples, 2*frameSamples)
		}
		return frameSamples, true
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeDREDHook = func(offset int) bool {
		calls = append(calls, "dred")
		offsets = append(offsets, offset)
		e.bumpDecodeStats(decodeDRED)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(100, redOpusPayloadType, []byte{0x01}),
		testPacket(103, redOpusPayloadType, []byte{0x03}),
	)

	want := []string{decodeNormal.String(), decodeLossPath.String(), "dred", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	if prepareCalls != 1 {
		t.Fatalf("prepareCalls=%d want 1", prepareCalls)
	}
	if len(offsets) != 1 || offsets[0] != frameSamples {
		t.Fatalf("dred offsets=%v want [%d]", offsets, frameSamples)
	}
	stats := e.Stats()
	if stats.DREDFallbackFrames != 1 || stats.DREDFrames != 1 || stats.DREDRecoveryAttempts != 1 || stats.LossPathFrames != 1 || stats.ConcealedFrames != 2 {
		t.Fatalf("stats dredFallback=%d dred=%d/%d loss=%d concealed=%d, want 1/1/1/1/2",
			stats.DREDFallbackFrames, stats.DREDFrames, stats.DREDRecoveryAttempts, stats.LossPathFrames, stats.ConcealedFrames)
	}
}

func TestReceiveLoopREDDecodeFailureFallsBackThroughFECAndDRED(t *testing.T) {
	var calls []string
	var fecPayload []byte
	var dredPayload []byte
	e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
	e.fecEnabledHook = func(_ []byte) bool { return true }
	e.prepareDREDHook = func(payload []byte, maxDREDSamples int) (int, bool) {
		calls = append(calls, "prepare-dred")
		dredPayload = append([]byte(nil), payload...)
		return maxDREDSamples, true
	}
	e.decodeREDHook = func(_ []byte) bool {
		calls = append(calls, "red")
		e.mu.Lock()
		e.stats.REDRecoveryAttempts++
		e.mu.Unlock()
		return false
	}
	e.decodeHook = func(payload []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		if kind == decodeFEC {
			fecPayload = append([]byte(nil), payload...)
			e.mu.Lock()
			e.stats.FECRecoveryAttempts++
			e.mu.Unlock()
			return false
		}
		if kind == decodeLossPath {
			t.Fatal("PLC should not run after DRED recovers the packet")
		}
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeDREDHook = func(int) bool {
		calls = append(calls, "dred")
		e.bumpDecodeStats(decodeDRED)
		return true
	}

	redPayload, _ := buildREDPayload(
		[]byte{0xb2},
		102*frameSamples,
		[]redHistoryFrame{{timestamp: 101 * frameSamples, payload: []byte{0xa1}}},
		1,
		frameSamples,
	)
	runReceiveLoopTest(t, e,
		testPacket(100, redPayloadType, mustREDPayload(t, []byte{0x01}, nil)),
		testPacket(102, redPayloadType, redPayload),
	)

	want := []string{decodeNormal.String(), "red", decodeFEC.String(), "prepare-dred", "dred", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	if string(fecPayload) != string([]byte{0xb2}) {
		t.Fatalf("FEC payload=%x want parsed RED primary b2", fecPayload)
	}
	if string(dredPayload) != string([]byte{0xb2}) {
		t.Fatalf("DRED payload=%x want parsed RED primary b2", dredPayload)
	}
	stats := e.Stats()
	if stats.REDFallbackFrames != 1 || stats.FECFallbackFrames != 1 || stats.DREDFrames != 1 || stats.DREDRecoveryAttempts != 1 || stats.LossPathFrames != 0 {
		t.Fatalf("stats redFallback=%d fecFallback=%d dred=%d/%d plc=%d, want 1/1/1/1/0",
			stats.REDFallbackFrames, stats.FECFallbackFrames, stats.DREDFrames, stats.DREDRecoveryAttempts, stats.LossPathFrames)
	}
}

func TestReceiveLoopFECFallsBackToDREDWhenNoREDBlock(t *testing.T) {
	var calls []string
	e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
	e.fecEnabledHook = func(_ []byte) bool { return true }
	e.prepareDREDHook = func(_ []byte, maxDREDSamples int) (int, bool) {
		calls = append(calls, "prepare-dred")
		return maxDREDSamples, true
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind.String())
		if kind == decodeFEC {
			e.mu.Lock()
			e.stats.FECRecoveryAttempts++
			e.mu.Unlock()
			return false
		}
		if kind == decodeLossPath {
			t.Fatal("PLC should not run after DRED recovers the packet")
		}
		e.bumpDecodeStats(kind)
		return true
	}
	e.decodeREDHook = func(_ []byte) bool {
		t.Fatal("RED should not run without a matching redundant block")
		return false
	}
	e.decodeDREDHook = func(int) bool {
		calls = append(calls, "dred")
		e.mu.Lock()
		e.stats.DREDRecoveryAttempts++
		e.stats.DREDFrames++
		e.stats.ConcealedFrames++
		e.mu.Unlock()
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(100, redPayloadType, mustREDPayload(t, []byte{0x01}, nil)),
		testPacket(102, redPayloadType, mustREDPayload(t, []byte{0x02}, nil)),
	)

	want := []string{decodeNormal.String(), decodeFEC.String(), "prepare-dred", "dred", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.FECRecoveryAttempts != 1 || stats.FECFallbackFrames != 1 || stats.DREDRecoveryAttempts != 1 || stats.DREDFrames != 1 || stats.REDRecoveryAttempts != 0 || stats.LossPathFrames != 0 {
		t.Fatalf("stats fec=%d/%d dred=%d/%d red=%d plc=%d, want fec 1/1 dred 1/1 red 0 plc 0",
			stats.FECRecoveryAttempts, stats.FECFallbackFrames, stats.DREDRecoveryAttempts, stats.DREDFrames, stats.REDRecoveryAttempts, stats.LossPathFrames)
	}
}

func TestReceiveLoopDREDFailureCountsErrorAndFallsBack(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{DRED: true})
	e.dredProbe = &dredPacketProbe{}
	e.prepareDREDHook = func(_ []byte, maxDREDSamples int) (int, bool) {
		return maxDREDSamples, true
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(100, redOpusPayloadType, []byte{0x01}),
		testPacket(102, redOpusPayloadType, []byte{0x02}),
	)

	want := []decodeKind{decodeNormal, decodeLossPath, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.DREDRecoveryAttempts != 1 || stats.DecodeErrors != 1 || stats.DREDFallbackFrames != 1 || stats.LossPathFrames != 1 || stats.DREDFrames != 0 {
		t.Fatalf("stats dredAttempts=%d decodeErrors=%d dredFallback=%d loss=%d dredFrames=%d, want 1/1/1/1/0",
			stats.DREDRecoveryAttempts, stats.DecodeErrors, stats.DREDFallbackFrames, stats.LossPathFrames, stats.DREDFrames)
	}
}

func TestReceiveLoopREDPayloadTypeParsesPrimaryWhenREDRecoveryDisabled(t *testing.T) {
	var calls []decodeKind
	var normalPayloads [][]byte
	e := newReceiveLoopTestEngine(engineConfig{RED: false})
	e.decodeREDHook = func(_ []byte) bool {
		t.Fatal("RED recovery should stay disabled")
		return false
	}
	e.decodeHook = func(payload []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		if kind == decodeNormal {
			normalPayloads = append(normalPayloads, append([]byte(nil), payload...))
		}
		e.bumpDecodeStats(kind)
		return true
	}

	redPayload, _ := buildREDPayload(
		[]byte{0x02},
		12*frameSamples,
		[]redHistoryFrame{{timestamp: 11 * frameSamples, payload: []byte{0xa1}}},
		1,
		frameSamples,
	)
	runReceiveLoopTest(t, e,
		testPacket(10, redPayloadType, mustREDPayload(t, []byte{0x01}, nil)),
		testPacket(12, redPayloadType, redPayload),
	)

	want := []decodeKind{decodeNormal, decodeLossPath, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	if len(normalPayloads) != 2 || string(normalPayloads[0]) != string([]byte{0x01}) || string(normalPayloads[1]) != string([]byte{0x02}) {
		t.Fatalf("normal payloads=%x want [01 02]", normalPayloads)
	}
	stats := e.Stats()
	if stats.REDRecoveryAttempts != 0 || stats.REDFallbackFrames != 0 || stats.LossPathFrames != 1 {
		t.Fatalf("stats red=%d fallback=%d loss=%d, want 0/0/1", stats.REDRecoveryAttempts, stats.REDFallbackFrames, stats.LossPathFrames)
	}
}

func TestReceiveLoopIgnoresStalePacketsWithoutRewindingExpectedSequence(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(10, redOpusPayloadType, []byte{0x10}),
		testPacket(11, redOpusPayloadType, []byte{0x11}),
		testPacket(10, redOpusPayloadType, []byte{0x10}),
		testPacket(12, redOpusPayloadType, []byte{0x12}),
	)

	want := []decodeKind{decodeNormal, decodeNormal, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.LossPathFrames != 0 || stats.PacketsReceived != 3 {
		t.Fatalf("stats loss=%d received=%d, want 0/3", stats.LossPathFrames, stats.PacketsReceived)
	}
}

func TestReceiveLoopIgnoresStaleMalformedRED(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{RED: true})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(10, redPayloadType, mustREDPayload(t, []byte{0x10}, nil)),
		testPacket(11, redPayloadType, mustREDPayload(t, []byte{0x11}, nil)),
		testPacket(10, redPayloadType, []byte{0x80 | redOpusPayloadType, 0x00}),
		testPacket(12, redPayloadType, mustREDPayload(t, []byte{0x12}, nil)),
	)

	want := []decodeKind{decodeNormal, decodeNormal, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.DecodeErrors != 0 || stats.REDFallbackFrames != 0 || stats.PacketsReceived != 3 {
		t.Fatalf("stats decodeErrors=%d redFallback=%d received=%d, want 0/0/3",
			stats.DecodeErrors, stats.REDFallbackFrames, stats.PacketsReceived)
	}
}

func TestReceiveLoopSequenceWraparoundDoesNotTriggerLoss(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(65535, redOpusPayloadType, []byte{0xff}),
		testPacket(0, redOpusPayloadType, []byte{0x00}),
	)

	want := []decodeKind{decodeNormal, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.LossPathFrames != 0 || stats.PacketsReceived != 2 {
		t.Fatalf("stats loss=%d received=%d, want 0/2", stats.LossPathFrames, stats.PacketsReceived)
	}
}

func TestReceiveLoopSequenceWraparoundMissingPacketUsesPLC(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{})
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(65534, redOpusPayloadType, []byte{0xfe}),
		testPacket(0, redOpusPayloadType, []byte{0x00}),
	)

	want := []decodeKind{decodeNormal, decodeLossPath, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.LossPathFrames != 1 || stats.ConcealedFrames != 1 || stats.PacketsReceived != 2 {
		t.Fatalf("stats loss=%d concealed=%d received=%d, want 1/1/2",
			stats.LossPathFrames, stats.ConcealedFrames, stats.PacketsReceived)
	}
}

func TestReceiveLoopHugeForwardJumpSkipsRecoverySearch(t *testing.T) {
	var calls []decodeKind
	e := newReceiveLoopTestEngine(engineConfig{RED: true, FEC: true, DRED: true})
	e.fecEnabledHook = func(_ []byte) bool {
		t.Fatal("FEC should not run for huge sequence jumps")
		return false
	}
	e.prepareDREDHook = func(_ []byte, _ int) (int, bool) {
		t.Fatal("DRED should not prepare for huge sequence jumps")
		return 0, false
	}
	e.decodeREDHook = func(_ []byte) bool {
		t.Fatal("RED should not run for huge sequence jumps")
		return false
	}
	e.decodeHook = func(_ []byte, kind decodeKind) bool {
		calls = append(calls, kind)
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e,
		testPacket(10, redPayloadType, mustREDPayload(t, []byte{0x10}, nil)),
		testPacket(200, redPayloadType, mustREDPayload(t, []byte{0x20}, nil)),
	)

	want := []decodeKind{decodeNormal, decodeNormal}
	if !sameDecodeKinds(calls, want) {
		t.Fatalf("decode calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.LossPathFrames != 0 || stats.FECRecoveryAttempts != 0 || stats.REDRecoveryAttempts != 0 || stats.DREDRecoveryAttempts != 0 {
		t.Fatalf("stats loss=%d fec=%d red=%d dred=%d, want all recovery 0",
			stats.LossPathFrames, stats.FECRecoveryAttempts, stats.REDRecoveryAttempts, stats.DREDRecoveryAttempts)
	}
}

func TestReceiveLoopPlainOpusPayloadIsNotParsedAsRED(t *testing.T) {
	e := newReceiveLoopTestEngine(engineConfig{RED: true})
	e.decodeHook = func(payload []byte, kind decodeKind) bool {
		if kind != decodeNormal {
			t.Fatalf("kind=%v want decodeNormal", kind)
		}
		if string(payload) != string([]byte{0x80, 0x01}) {
			t.Fatalf("payload=%x want raw Opus", payload)
		}
		e.bumpDecodeStats(kind)
		return true
	}

	runReceiveLoopTest(t, e, testPacket(1, redOpusPayloadType, []byte{0x80, 0x01}))

	stats := e.Stats()
	if stats.DecodeErrors != 0 || stats.REDFallbackFrames != 0 || stats.PacketsReceived != 1 {
		t.Fatalf("stats decodeErrors=%d redFallback=%d received=%d, want 0/0/1", stats.DecodeErrors, stats.REDFallbackFrames, stats.PacketsReceived)
	}
}

type fakeRTPReader struct {
	packets []*rtp.Packet
}

func (r *fakeRTPReader) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	if len(r.packets) == 0 {
		return nil, nil, io.EOF
	}
	pkt := r.packets[0]
	r.packets = r.packets[1:]
	return pkt, nil, nil
}

func newReceiveLoopTestEngine(cfg engineConfig) *engine {
	return &engine{
		cfg:    cfg,
		stopCh: make(chan struct{}),
		stats:  engineStats{Running: true},
	}
}

func runReceiveLoopTest(t *testing.T, e *engine, packets ...*rtp.Packet) {
	t.Helper()
	e.done.Add(1)
	e.receiveRTP(&fakeRTPReader{packets: packets})
	e.done.Wait()
}

func testPacket(seq uint16, payloadType uint8, payload []byte) *rtp.Packet {
	return &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    payloadType,
			SequenceNumber: seq,
			Timestamp:      uint32(seq) * frameSamples,
		},
		Payload: payload,
	}
}

func mustREDPayload(t *testing.T, primary []byte, history []redHistoryFrame) []byte {
	t.Helper()
	payload, _ := buildREDPayload(primary, frameSamples, history, 1, frameSamples)
	if _, _, err := parseREDPayload(payload); err != nil {
		t.Fatalf("buildREDPayload produced invalid RED: %v", err)
	}
	return payload
}

func (e *engine) bumpDecodeStats(kind decodeKind) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch kind {
	case decodeLossPath:
		e.stats.ConcealedFrames++
		e.stats.LossPathFrames++
	case decodeFEC:
		e.stats.ConcealedFrames++
		e.stats.FECFrames++
		e.stats.FECRecoveryAttempts++
	case decodeRED:
		e.stats.ConcealedFrames++
		e.stats.REDFrames++
		e.stats.REDRecoveryAttempts++
	case decodeDRED:
		e.stats.ConcealedFrames++
		e.stats.DREDFrames++
		e.stats.DREDRecoveryAttempts++
	default:
		e.stats.PacketsReceived++
	}
}

func (k decodeKind) String() string {
	switch k {
	case decodeNormal:
		return "normal"
	case decodeLossPath:
		return "loss"
	case decodeFEC:
		return "fec"
	case decodeRED:
		return "red"
	case decodeDRED:
		return "dred"
	default:
		return "unknown"
	}
}

func sameDecodeKinds(got, want []decodeKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
