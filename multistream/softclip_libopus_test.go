package multistream

import (
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"testing"
)

func TestFloat32ToInt16SoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		samples  []float32
	}{
		{
			name:     "six channel clipped segments",
			n:        5,
			channels: 6,
			mem:      []float32{0, -0.04, 0.06, 0, 0.08, -0.03},
			samples: []float32{
				1.35, -1.6, 0.2, 0.95, -1.25, 1.9,
				1.7, -1.2, -0.3, 1.2, -1.55, 1.5,
				0.8, -0.4, -1.4, 1.6, -0.6, 0.1,
				-1.3, 1.4, -1.8, 0.4, 1.2, -1.1,
				0.2, 0.1, -0.5, -1.7, 0.8, -0.2,
			},
		},
		{
			name:     "three channel carryover",
			n:        8,
			channels: 3,
			mem:      []float32{-0.12, 0.09, -0.02},
			samples: []float32{
				0.7, -0.7, 0.5,
				0.4, -0.4, 0.25,
				-0.2, 0.2, -0.1,
				1.3, -1.4, 1.6,
				1.7, -1.2, 1.3,
				0.6, -0.5, -0.6,
				-1.5, 1.8, -1.9,
				0.3, -0.2, 0.1,
			},
		},
		{
			name:     "hard clamp before quant",
			n:        4,
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{2.5, -2.5, 3, -3, 1.2, -1.2, 0, 0.5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantFloat, wantMem, err := libopustest.ProbeSoftClip(tc.n, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeCELTDispatch, wantFloat)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}

			gotMem := append([]float32(nil), tc.mem...)
			clipped := append([]float32(nil), tc.samples...)
			opusmath.PCMSoftClip(clipped, tc.n, tc.channels, gotMem)
			got := float32ToInt16(clipped)
			if len(got) != len(want) {
				t.Fatalf("output len=%d want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
				}
			}
			for i := range wantMem {
				if math.Float32bits(gotMem[i]) != math.Float32bits(wantMem[i]) {
					t.Fatalf("mem[%d]=%g want %g", i, gotMem[i], wantMem[i])
				}
			}
		})
	}
}

func TestMultistreamSoftClipLifecycleMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	path, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
		Label: "multistream clipping lifecycle", OutputBase: "gopus_ms_clip_sequence",
		SourceFile: "libopus_multistream_softclip_sequence.c",
		CFlags:     []string{"-O3", "-DNDEBUG"}, Libs: []string{"-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream clipping lifecycle", err)
	}
	r, err := libopustest.RunOracle(path, nil, "multistream clipping lifecycle", "MSCO")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.U32(); got != 12 {
		t.Fatalf("C packet count=%d, want 12", got)
	}
	packets := make([][]byte, 12)
	for i := range packets {
		n := r.Count(-1)
		if n <= 0 || n > 1500 {
			t.Fatalf("invalid C packet size %d", n)
		}
		packets[i] = append([]byte(nil), r.Bytes(n)...)
	}
	for scenario, name := range []string{"int16_float_int16", "int16_int24_int16", "projection_reset", "projection_loss"} {
		t.Run(name, func(t *testing.T) {
			var d *Decoder
			if scenario < 2 {
				d, err = NewDecoder(48000, 1, 1, 0, []byte{0})
			} else {
				d, err = NewProjectionDecoder(48000, 1, 1, 0, []byte{255, 127})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetGain(4608); err != nil {
				t.Fatal(err)
			}
			prime := 1
			if scenario == 3 {
				prime = 8
			}
			steps := prime + 2
			if scenario < 2 {
				steps++
			}
			if got := r.U32(); int(got) != steps {
				t.Fatalf("C steps=%d, want %d", got, steps)
			}
			for step := range steps {
				format, packet := 1, 1
				if step < prime {
					packet = step
				} else if step == prime && scenario < 2 {
					if err := d.SetGain(0); err != nil {
						t.Fatal(err)
					}
					format, packet = scenario*2, 0
				} else if step == prime && scenario == 2 {
					d.Reset()
					if err := d.SetGain(0); err != nil {
						t.Fatal(err)
					}
				} else if step == prime && scenario == 3 {
					packet = -1
				}
				var data []byte
				if packet >= 0 {
					data = packets[packet]
				}
				var got []uint32
				switch format {
				case 0:
					pcm, e := d.DecodeToFloat32(data, 960)
					if e != nil {
						t.Fatal(e)
					}
					for _, v := range pcm {
						got = append(got, math.Float32bits(v))
					}
				case 1:
					pcm, e := d.DecodeToInt16(data, 960)
					if e != nil {
						t.Fatal(e)
					}
					for _, v := range pcm {
						got = append(got, uint32(int32(v)))
					}
				case 2:
					pcm, e := d.DecodeToInt24(data, 960)
					if e != nil {
						t.Fatal(e)
					}
					for _, v := range pcm {
						got = append(got, uint32(v))
					}
				}
				wantFormat, n, finalRange := r.U32(), r.U32(), r.U32()
				if int(wantFormat) != format || n != 960 || len(got) != int(n) || d.FinalRange() != finalRange {
					t.Fatalf("step %d format=%d/%d count=%d/%d range=%08x/%08x", step, format, wantFormat, len(got), n, d.FinalRange(), finalRange)
				}
				for i, bits := range got {
					if want := r.U32(); bits != want {
						t.Errorf("step %d sample %d Go=%08x C=%08x", step, i, bits, want)
					}
				}
			}
		})
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
