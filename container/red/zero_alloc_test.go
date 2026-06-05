package red

import "testing"

func zaHistory() []Frame {
	return []Frame{
		{Timestamp: 4000, Payload: make([]byte, 80)},
		{Timestamp: 3040, Payload: make([]byte, 80)},
		{Timestamp: 2080, Payload: make([]byte, 80)},
	}
}

func zaPayload() []byte {
	out, _ := Build(make([]byte, 120), 4960, zaHistory(), 3, 960, 111)
	return out
}

// TestZeroAllocHotPaths locks the allocation-free contract: ParseInto and
// BuildAppend with reused buffers, and FindRecovery, must not allocate.
func TestZeroAllocHotPaths(t *testing.T) {
	p := zaPayload()
	hist := zaHistory()
	primary := make([]byte, 120)
	blockBuf := make([]Block, 0, MaxDepth)
	outBuf := make([]byte, 0, 512)

	if n := testing.AllocsPerRun(200, func() {
		_, blockBuf, _ = ParseInto(p, 111, blockBuf[:0])
	}); n != 0 {
		t.Errorf("ParseInto allocs/op = %v, want 0", n)
	}
	if n := testing.AllocsPerRun(200, func() {
		outBuf, _ = BuildAppend(outBuf[:0], primary, 4960, hist, 3, 960, 111)
	}); n != 0 {
		t.Errorf("BuildAppend allocs/op = %v, want 0", n)
	}
	_, blocks, _ := Parse(p, 111)
	if n := testing.AllocsPerRun(200, func() {
		_ = FindRecovery(blocks, 1, 960, 4960, 4000)
	}); n != 0 {
		t.Errorf("FindRecovery allocs/op = %v, want 0", n)
	}
}

// TestAppendHistoryZeroAlloc locks the steady-state contract: once the history
// window is full, AppendHistory recycles buffers and allocates nothing.
func TestAppendHistoryZeroAlloc(t *testing.T) {
	payload := make([]byte, 80)
	var hist []Frame
	for i := range MaxDepth + 2 { // fill the window
		hist = AppendHistory(hist, payload, uint32(i*960), MaxDepth)
	}
	ts := uint32(MaxDepth * 960)
	if n := testing.AllocsPerRun(200, func() {
		ts += 960
		hist = AppendHistory(hist, payload, ts, MaxDepth)
	}); n != 0 {
		t.Errorf("AppendHistory allocs/op = %v, want 0", n)
	}
}

// TestDecoderZeroAlloc locks the steady-state contract for the high-level
// Decoder: once warm, Parse reuses its block slice and allocates nothing.
func TestDecoderZeroAlloc(t *testing.T) {
	p := zaPayload()
	dec := NewDecoder(111)
	_, _, _ = dec.Parse(p) // warm
	if n := testing.AllocsPerRun(200, func() {
		_, _, _ = dec.Parse(p)
	}); n != 0 {
		t.Errorf("Decoder.Parse allocs/op = %v, want 0", n)
	}
}

// TestEncoderZeroAlloc locks the steady-state contract for the high-level
// Encoder: once the history window is warm, Encode reuses its output buffer and
// history and allocates nothing.
func TestEncoderZeroAlloc(t *testing.T) {
	primary := make([]byte, 120)
	enc := NewEncoder(111, 960, 3)
	ts := uint32(0)
	for range MaxDepth + 2 { // warm history + buffers
		ts += 960
		_, _ = enc.Encode(primary, ts)
	}
	if n := testing.AllocsPerRun(200, func() {
		ts += 960
		_, _ = enc.Encode(primary, ts)
	}); n != 0 {
		t.Errorf("Encoder.Encode allocs/op = %v, want 0", n)
	}
}

// TestParseIntoMatchesParse checks the reused-buffer path produces identical
// results to the convenience wrapper.
func TestParseIntoMatchesParse(t *testing.T) {
	p := zaPayload()
	wantPrimary, wantBlocks, wantErr := Parse(p, 111)
	gotPrimary, gotBlocks, gotErr := ParseInto(p, 111, make([]Block, 0, MaxDepth))
	if gotErr != wantErr {
		t.Fatalf("err: got %v want %v", gotErr, wantErr)
	}
	if string(gotPrimary) != string(wantPrimary) {
		t.Fatalf("primary mismatch")
	}
	if len(gotBlocks) != len(wantBlocks) {
		t.Fatalf("blocks len: got %d want %d", len(gotBlocks), len(wantBlocks))
	}
	for i := range gotBlocks {
		if gotBlocks[i].PayloadType != wantBlocks[i].PayloadType ||
			gotBlocks[i].TimestampOffset != wantBlocks[i].TimestampOffset ||
			string(gotBlocks[i].Payload) != string(wantBlocks[i].Payload) {
			t.Fatalf("block %d mismatch", i)
		}
	}
}

// TestBuildAppendMatchesBuild checks the reused-buffer path produces identical
// bytes to the convenience wrapper.
func TestBuildAppendMatchesBuild(t *testing.T) {
	hist := zaHistory()
	primary := make([]byte, 120)
	for i := range primary {
		primary[i] = byte(i)
	}
	want, wantN := Build(primary, 4960, hist, 3, 960, 111)
	got, gotN := BuildAppend(make([]byte, 0, 512), primary, 4960, hist, 3, 960, 111)
	if gotN != wantN || string(got) != string(want) {
		t.Fatalf("BuildAppend mismatch: gotN=%d wantN=%d bytesEqual=%v", gotN, wantN, string(got) == string(want))
	}
}

func BenchmarkParse(b *testing.B) {
	p := zaPayload()
	dst := make([]Block, 0, MaxDepth)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, dst, _ = ParseInto(p, 111, dst[:0])
	}
	_ = dst
}

func BenchmarkBuild(b *testing.B) {
	hist := zaHistory()
	primary := make([]byte, 120)
	buf := make([]byte, 0, 512)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf, _ = BuildAppend(buf[:0], primary, 4960, hist, 3, 960, 111)
	}
	_ = buf
}

func BenchmarkFindRecovery(b *testing.B) {
	p := zaPayload()
	_, blocks, _ := Parse(p, 111)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FindRecovery(blocks, 1, 960, 4960, 4000)
	}
}
