package multistream

import "testing"

// TestMultistreamEncodeDecodeAllocGuard pins the steady-state per-call
// allocation budget of the multistream orchestration layer (the per-stream
// loop, packet split/reassembly, surround routing, and projection
// demix-matrix multiply). The encoder and decoder own reusable scratch sized
// once, so after warm-up a repeated Encode/Decode of a fixed frame must stay
// at or below these counts. The remaining allocations are the freshly
// allocated result slices that escape to the caller plus the elementary
// per-stream decode outputs (owned by the celt/silk/hybrid decoders); the
// multistream glue itself adds no per-call allocation.
//
// The thresholds are upper bounds: a regression that reintroduces per-call
// scratch allocation in the packet split/assemble, surround/projection mixing,
// soft-clip, or demix paths trips this guard. Bump deliberately only when the
// escaping result-slice shape changes.
func TestMultistreamEncodeDecodeAllocGuard(t *testing.T) {
	const runs = 50
	const frameSize = 960

	t.Run("surround", func(t *testing.T) {
		cases := []struct {
			name      string
			channels  int
			maxEncode float64
			maxDecode float64
		}{
			{"5.1", 6, 13, 9},
			{"7.1", 8, 17, 11},
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
				var pkt []byte
				for range 5 {
					pkt, err = enc.Encode(pcm, frameSize)
					if err != nil {
						t.Fatalf("warm-up Encode: %v", err)
					}
					if _, err := dec.DecodeToFloat32(pkt, frameSize); err != nil {
						t.Fatalf("warm-up Decode: %v", err)
					}
				}

				encAllocs := testing.AllocsPerRun(runs, func() {
					pkt, _ = enc.Encode(pcm, frameSize)
				})
				if encAllocs > tc.maxEncode {
					t.Errorf("encode allocs/op = %.0f, want <= %.0f", encAllocs, tc.maxEncode)
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
			maxEncode      float64
			maxDecodeF32   float64
			maxDecodeInt16 float64
		}{
			{"foa", 4, 1, 5, 6},
			{"soa", 9, 1, 11, 12},
			{"toa", 16, 1, 17, 18},
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
				var pkt []byte
				for range 5 {
					pkt, err = enc.Encode(pcm, frameSize)
					if err != nil {
						t.Fatalf("warm-up Encode: %v", err)
					}
					if _, err := dec.DecodeToFloat32(pkt, frameSize); err != nil {
						t.Fatalf("warm-up DecodeToFloat32: %v", err)
					}
					if _, err := dec.DecodeToInt16(pkt, frameSize); err != nil {
						t.Fatalf("warm-up DecodeToInt16: %v", err)
					}
				}

				encAllocs := testing.AllocsPerRun(runs, func() {
					pkt, _ = enc.Encode(pcm, frameSize)
				})
				if encAllocs > tc.maxEncode {
					t.Errorf("encode allocs/op = %.0f, want <= %.0f", encAllocs, tc.maxEncode)
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
