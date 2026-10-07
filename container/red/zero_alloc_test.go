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

// TestDecoderZeroAlloc checks storage reuse across successful and failed parses.
func TestDecoderZeroAlloc(t *testing.T) {
	// One redundant byte at offset 960 followed by one primary byte.
	packet := []byte{0xef, 0x0f, 0, 1, 111, 0xab, 0xcd}
	for _, tc := range []struct {
		name string
		bad  []byte
	}{
		{"valid only", nil},
		{"truncated header", []byte{0xef}},
		{"missing primary", packet[:len(packet)-1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec := NewDecoder(111)
			if _, _, err := dec.Parse(packet); err != nil {
				t.Fatal(err)
			}
			allocs := testing.AllocsPerRun(200, func() {
				if tc.bad != nil {
					primary, blocks, err := dec.Parse(tc.bad)
					if err == nil || primary != nil || blocks != nil {
						t.Fatal("malformed packet must return an error and no payloads")
					}
				}
				primary, blocks, err := dec.Parse(packet)
				if err != nil || len(primary) != 1 || primary[0] != 0xcd ||
					len(blocks) != 1 || blocks[0].TimestampOffset != 960 ||
					len(blocks[0].Payload) != 1 || blocks[0].Payload[0] != 0xab {
					t.Fatal("valid packet did not recover its primary and redundant payloads")
				}
			})
			if allocs != 0 {
				t.Fatalf("Decoder.Parse allocs/op = %g, want 0", allocs)
			}
		})
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

func BenchmarkDecoderParse(b *testing.B) {
	history := make([]Frame, MaxDepth)
	for i := range history {
		history[i] = Frame{Timestamp: uint32(i * 960), Payload: make([]byte, 80)}
	}
	p, _ := Build(make([]byte, 120), MaxDepth*960, history, MaxDepth, 960, 111)
	dec := NewDecoder(111)
	if _, _, err := dec.Parse(p); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := dec.Parse(p); err != nil {
			b.Fatal(err)
		}
	}
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
