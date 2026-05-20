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

func TestReceiveLoopREDFECDREDPriority(t *testing.T) {
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

	want := []string{decodeNormal.String(), "prepare-dred", "red", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.FECRecoveryAttempts != 0 || stats.FECFallbackFrames != 0 || stats.REDRecoveryAttempts != 1 || stats.REDFrames != 1 || stats.DREDRecoveryAttempts != 0 || stats.LossPathFrames != 0 {
		t.Fatalf("stats fec=%d/%d red=%d/%d dred=%d plc=%d, want fec 0/0 red 1/1 dred 0 plc 0",
			stats.FECRecoveryAttempts, stats.FECFallbackFrames, stats.REDRecoveryAttempts, stats.REDFrames, stats.DREDRecoveryAttempts, stats.LossPathFrames)
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

	want := []string{decodeNormal.String(), "prepare-dred", decodeFEC.String(), "dred", decodeNormal.String()}
	if !sameStrings(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	stats := e.Stats()
	if stats.FECRecoveryAttempts != 1 || stats.FECFallbackFrames != 1 || stats.DREDRecoveryAttempts != 1 || stats.DREDFrames != 1 || stats.REDRecoveryAttempts != 0 || stats.LossPathFrames != 0 {
		t.Fatalf("stats fec=%d/%d dred=%d/%d red=%d plc=%d, want fec 1/1 dred 1/1 red 0 plc 0",
			stats.FECRecoveryAttempts, stats.FECFallbackFrames, stats.DREDRecoveryAttempts, stats.DREDFrames, stats.REDRecoveryAttempts, stats.LossPathFrames)
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
