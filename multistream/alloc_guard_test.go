package multistream

import "testing"

// warmMultistreamEncodeZeroAllocs warms the encoder up on both entry points
// and asserts the steady-state float and 16-bit encodes allocate nothing. It
// returns a packet of the frame for the decode side.
func warmMultistreamEncodeZeroAllocs(t *testing.T, enc *Encoder, pcm []float32, frameSize int) []byte {
	t.Helper()
	pcm16 := make([]int16, len(pcm))
	for i, v := range pcm {
		pcm16[i] = int16(v * 32767)
	}
	out := make([]byte, 4000*enc.Streams())
	var n int
	for range 5 {
		var err error
		if _, err = enc.EncodeInt16(pcm16, frameSize, out); err != nil {
			t.Fatalf("warm-up EncodeInt16: %v", err)
		}
		if n, err = enc.Encode(pcm, frameSize, out); err != nil {
			t.Fatalf("warm-up Encode: %v", err)
		}
	}
	pkt := append([]byte(nil), out[:n]...)
	if allocs := testing.AllocsPerRun(50, func() {
		if _, err := enc.Encode(pcm, frameSize, out); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}); allocs != 0 {
		t.Errorf("Encode allocs/op = %.0f, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(50, func() {
		if _, err := enc.EncodeInt16(pcm16, frameSize, out); err != nil {
			t.Fatalf("EncodeInt16: %v", err)
		}
	}); allocs != 0 {
		t.Errorf("EncodeInt16 allocs/op = %.0f, want 0", allocs)
	}
	return pkt
}

// TestMultistreamEncodeDecodeAllocGuard pins the steady-state per-call
// allocation budget of the multistream orchestration layer (the per-stream
// loop, packet split/reassembly, surround routing, and projection
// mixing/demixing). The encoder writes into the caller's buffer and is
// strictly zero-alloc after warm-up. On the decode side the remaining
// allocations are the freshly allocated result slices that escape to the
// caller plus the elementary per-stream decode outputs (owned by the
// celt/silk/hybrid decoders); the multistream glue itself adds no per-call
// allocation.
//
// The decode thresholds are upper bounds: a regression that reintroduces
// per-call scratch allocation in the packet split, soft-clip, or demix paths
// trips this guard. Bump deliberately only when the escaping result-slice
// shape changes.
func TestMultistreamEncodeDecodeAllocGuard(t *testing.T) {
	const runs = 50
	const frameSize = 960

	t.Run("surround", func(t *testing.T) {
		cases := []struct {
			name      string
			channels  int
			maxDecode float64
		}{
			{"5.1", 6, 9},
			{"7.1", 8, 11},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				enc, err := NewEncoderDefault(48000, tc.channels)
				if err != nil {
					t.Fatalf("NewEncoderDefault: %v", err)
				}
				enc.SetBitrate(tc.channels * 64000)
				streams, coupled, mapping, err := DefaultMapping(tc.channels)
				if err != nil {
					t.Fatalf("DefaultMapping: %v", err)
				}
				dec, err := NewDecoder(48000, tc.channels, streams, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}

				pcm := generateMultichannelSine(tc.channels, frameSize)
				pkt := warmMultistreamEncodeZeroAllocs(t, enc, pcm, frameSize)
				for range 5 {
					if _, err := dec.DecodeToFloat32(pkt, frameSize); err != nil {
						t.Fatalf("warm-up Decode: %v", err)
					}
				}

				decAllocs := testing.AllocsPerRun(runs, func() {
					dec.DecodeToFloat32(pkt, frameSize)
				})
				if decAllocs > tc.maxDecode {
					t.Errorf("decode allocs/op = %.0f, want <= %.0f", decAllocs, tc.maxDecode)
				}
			})
		}
	})

	t.Run("projection", func(t *testing.T) {
		cases := []struct {
			name           string
			channels       int
			maxDecodeF32   float64
			maxDecodeInt16 float64
		}{
			{"foa", 4, 5, 6},
			{"soa", 9, 11, 12},
			{"toa", 16, 17, 18},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				enc, err := NewProjectionEncoder(48000, tc.channels)
				if err != nil {
					t.Fatalf("NewProjectionEncoder: %v", err)
				}
				enc.SetBitrate(tc.channels * 32000)
				demix := enc.GetDemixingMatrix()
				dec, err := NewProjectionDecoder(48000, tc.channels, enc.Streams(), enc.CoupledStreams(), demix)
				if err != nil {
					t.Fatalf("NewProjectionDecoder: %v", err)
				}

				pcm := generateMultichannelSine(tc.channels, frameSize)
				pkt := warmMultistreamEncodeZeroAllocs(t, enc, pcm, frameSize)
				for range 5 {
					if _, err := dec.DecodeToFloat32(pkt, frameSize); err != nil {
						t.Fatalf("warm-up DecodeToFloat32: %v", err)
					}
					if _, err := dec.DecodeToInt16(pkt, frameSize); err != nil {
						t.Fatalf("warm-up DecodeToInt16: %v", err)
					}
				}

				decF32Allocs := testing.AllocsPerRun(runs, func() {
					dec.DecodeToFloat32(pkt, frameSize)
				})
				if decF32Allocs > tc.maxDecodeF32 {
					t.Errorf("decode_f32 allocs/op = %.0f, want <= %.0f", decF32Allocs, tc.maxDecodeF32)
				}
				decInt16Allocs := testing.AllocsPerRun(runs, func() {
					dec.DecodeToInt16(pkt, frameSize)
				})
				if decInt16Allocs > tc.maxDecodeInt16 {
					t.Errorf("decode_i16 allocs/op = %.0f, want <= %.0f", decInt16Allocs, tc.maxDecodeInt16)
				}
			})
		}
	})
}
