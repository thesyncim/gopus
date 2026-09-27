//go:build gopus_qext

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var libopusQEXTCubicDecodeHelper libopustest.HelperCache

func buildLibopusQEXTCubicDecodeHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext cubic decode",
		OutputBase:  "gopus_libopus_celt_qext_cubic_decode",
		SourceFile:  "libopus_celt_qext_cubic_decode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTCubicDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTCubicDecodeHelper.Path(buildLibopusQEXTCubicDecodeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext cubic decode", err)
		return
	}
	for _, tc := range []struct {
		name       string
		res, B     int
		face, sign int
		gainBits   uint32
		iy         [10]int32
	}{
		// These leaves are from an active 20 ms stereo QEXT packet. Their C
		// 1.f/sqrt(sum) mag differs from a float32 reciprocal square root.
		{"stereo_band0", 4, 1, 0, 0, 0x3eacaf87, [10]int32{0, 2, 7, 4, 6, 2, 1, 4, 11, 3}},
		{"stereo_band1", 4, 1, 2, 1, 0x3ed04695, [10]int32{8, 0, 0, 5, 9, 9, 1, 0, 11, 15}},
		{"stereo_low_res", 3, 1, 1, 0, 0x3e7774ba, [10]int32{4, 0, 7, 5, 4, 4, 4, 3, 2, 3}},
		{"large_norm", 11, 1, 1, 0, 0x36905829, [10]int32{1628, 0, 392, 16, 1682, 2014, 340, 51, 1733, 1977}},
		{"signed_last_face", 3, 1, 9, 1, 0x3e4fe736, [10]int32{3, 1, 1, 1, 1, 3, 6, 4, 0, 0}},
		{"transient_block", 4, 8, 3, 1, 0x3eb425cc, [10]int32{6, 8, 15, 0, 1, 14, 3, 6, 7, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const n = 10
			var enc rangecoding.Encoder
			enc.Init(make([]byte, 128))
			enc.EncodeUniform(uint32(tc.face), n)
			enc.EncodeRawBits(uint32(tc.sign), 1)
			for i, digit := range tc.iy {
				if i != tc.face {
					enc.EncodeRawBits(uint32(digit), uint(tc.res))
				}
			}
			packet := append([]byte(nil), enc.Done()...)
			if enc.Error() != 0 || len(packet) == 0 {
				t.Fatalf("range encode: error=%v bytes=%d", enc.Error(), len(packet))
			}

			payload := libopustest.NewOraclePayload("GQDI", n, uint32(tc.res), uint32(tc.B), tc.gainBits, uint32(len(packet)))
			payload.Raw(packet)
			reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext cubic decode", "GQDO")
			if err != nil {
				t.Fatal(err)
			}
			if got := reader.U32(); got != n {
				t.Fatalf("C vector length %d, want %d", got, n)
			}
			refCollapse, refRange, refTell := reader.U32(), reader.U32(), reader.U32()
			var refBits [n]uint32
			for i := range refBits {
				refBits[i] = reader.U32()
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			var dec rangecoding.Decoder
			dec.Init(packet)
			var got [n]celtNorm
			gotCollapse := cubicUnquant(got[:], n, tc.res, tc.B, &dec, math.Float32frombits(tc.gainBits), nil)
			if uint32(gotCollapse) != refCollapse || dec.Range() != refRange || uint32(dec.TellFrac()) != refTell {
				t.Errorf("coder state: Go collapse=%d range=%08x tell=%d, C collapse=%d range=%08x tell=%d",
					gotCollapse, dec.Range(), dec.TellFrac(), refCollapse, refRange, refTell)
			}
			for i, sample := range got {
				if bits := math.Float32bits(sample); bits != refBits[i] {
					t.Errorf("X[%d]: Go=%08x C=%08x", i, bits, refBits[i])
				}
			}
			var scratch bandDecodeScratch
			dec.Init(packet)
			cubicUnquant(got[:], n, tc.res, tc.B, &dec, math.Float32frombits(tc.gainBits), &scratch)
			allocs := testing.AllocsPerRun(20, func() {
				dec.Init(packet)
				cubicUnquant(got[:], n, tc.res, tc.B, &dec, math.Float32frombits(tc.gainBits), &scratch)
			})
			if allocs != 0 {
				t.Fatalf("warm cubic decode allocs/call=%g, want 0", allocs)
			}
		})
	}
}
