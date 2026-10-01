package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Exercise clipping memory at public API boundaries against persistent C
// decoders. The selected C build generates the shared packets independently.
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
