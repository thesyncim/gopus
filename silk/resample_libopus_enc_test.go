package silk

import "testing"

// TestNewLibopusResamplerEncFunctionSelect verifies the encoder input resampler
// (silk_resampler_init forEnc=1) selects the same resampler function as libopus
// for every API_fs_Hz -> fs_kHz pair: copy when equal, direct 2x when
// Fs_out==2*Fs_in, IIR/FIR for other upsampling, and down_FIR when downsampling.
func TestNewLibopusResamplerEncFunctionSelect(t *testing.T) {
	type want struct {
		copy bool
		up2  bool
		down bool
	}
	cases := []struct {
		fsIn, fsOut int
		w           want
	}{
		// in 8k
		{8000, 8000, want{copy: true}}, // C
		{8000, 12000, want{}},          // UF (IIR/FIR, not copy/up2/down)
		{8000, 16000, want{up2: true}}, // U
		// in 12k
		{12000, 8000, want{down: true}},  // AF -> down_FIR
		{12000, 12000, want{copy: true}}, // C
		{12000, 16000, want{}},           // UF
		// in 16k
		{16000, 8000, want{down: true}},  // AF
		{16000, 12000, want{down: true}}, // AF
		{16000, 16000, want{copy: true}}, // C
		// in 24k
		{24000, 8000, want{down: true}},  // AF (1:3)
		{24000, 12000, want{down: true}}, // AF (1:2)
		{24000, 16000, want{down: true}}, // AF (2:3)
		// in 48k
		{48000, 8000, want{down: true}},  // AF (1:6)
		{48000, 12000, want{down: true}}, // AF (1:4)
		{48000, 16000, want{down: true}}, // AF (1:3)
	}
	for _, c := range cases {
		r := NewLibopusResamplerEnc(c.fsIn, c.fsOut)
		gotDown := r.down != nil
		if r.copyMode != c.w.copy || r.up2HQMode != c.w.up2 || gotDown != c.w.down {
			t.Errorf("%d->%d: got copy=%v up2=%v down=%v, want copy=%v up2=%v down=%v",
				c.fsIn, c.fsOut, r.copyMode, r.up2HQMode, gotDown, c.w.copy, c.w.up2, c.w.down)
		}
	}
}

// TestNewLibopusResamplerEncInputDelay verifies the encoder input delay matches
// delay_matrix_enc[rateID(Fs_in)][rateID(Fs_out)] from silk/resampler.c, which is
// distinct from the decoder delay_matrix_dec used by NewLibopusResampler.
func TestNewLibopusResamplerEncInputDelay(t *testing.T) {
	cases := []struct {
		fsIn, fsOut int
		delay       int32
	}{
		{8000, 8000, 6},
		{8000, 16000, 3},
		{12000, 12000, 7},
		{16000, 16000, 10},
		{16000, 12000, 1},
		{24000, 16000, 6},
		{24000, 12000, 2},
		{48000, 16000, 12},
		{48000, 12000, 10},
		{48000, 8000, 18},
	}
	for _, c := range cases {
		r := NewLibopusResamplerEnc(c.fsIn, c.fsOut)
		if r.inputDelay != c.delay {
			t.Errorf("%d->%d enc inputDelay=%d want %d", c.fsIn, c.fsOut, r.inputDelay, c.delay)
		}
	}
}

// TestNewLibopusResamplerDecUnchanged guards that the decoder-side constructor
// still uses delay_matrix_dec, so wiring the encoder variant did not perturb the
// decoder output resampler.
func TestNewLibopusResamplerDecUnchanged(t *testing.T) {
	cases := []struct {
		fsIn, fsOut int
		delay       int32
	}{
		{8000, 8000, 4},   // delay_matrix_dec[0][0]
		{12000, 12000, 9}, // delay_matrix_dec[1][1]
		{16000, 16000, 12},
		{16000, 48000, 7},
	}
	for _, c := range cases {
		r := NewLibopusResampler(c.fsIn, c.fsOut)
		if r.inputDelay != c.delay {
			t.Errorf("%d->%d dec inputDelay=%d want %d", c.fsIn, c.fsOut, r.inputDelay, c.delay)
		}
	}
}

// TestLibopusResamplerEncIdentityCopiesInput verifies the copy path (Fs_in==Fs_out)
// reproduces the input after the libopus 1 ms input-delay priming, matching
// silk_resampler's default (copy) branch.
func TestLibopusResamplerEncIdentityCopiesInput(t *testing.T) {
	r := NewLibopusResamplerEnc(16000, 16000)
	const n = 320 // 20 ms at 16 kHz
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(int16(((i*37)%2000)-1000)) / 32768.0
	}
	out := make([]float32, n)
	got := r.ProcessInto(in, out)
	if got != n {
		t.Fatalf("identity ProcessInto returned %d, want %d", got, n)
	}
	// With a copy resampler the output equals the input shifted by inputDelay
	// (the first inputDelay samples come from the zero-initialized delay buffer).
	delay := int(r.inputDelay)
	for i := delay; i < n; i++ {
		// Quantization to int16 is lossless here (input already int16-valued).
		if out[i] != in[i-delay] {
			t.Fatalf("identity sample %d = %v, want %v (delay=%d)", i, out[i], in[i-delay], delay)
		}
	}
}
