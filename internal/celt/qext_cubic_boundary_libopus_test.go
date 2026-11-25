//go:build gopus_qext

package celt

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"strconv"
	"testing"
)

var qextCubicBoundaryGridHelper libopustest.HelperCache

func buildQEXTCubicBoundaryGridHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "QEXT cubic boundary grid",
		OutputBase:  "gopus_libopus_celt_qext_cubic_boundary_grid",
		SourceFile:  "libopus_celt_qext_cubic_decode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTCubicReductionBoundaryGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := qextCubicBoundaryGridHelper.Path(buildQEXTCubicBoundaryGridHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT cubic boundary grid", err)
		return
	}
	cases := []struct {
		n, res, blocks int
		gainBits       uint32
	}{
		{3, 2, 1, 0x3f123457},
		{4, 7, 1, 0x3eaaaaab},
		{5, 14, 1, 0x3f7ffdd0},
		{15, 2, 1, 0x3f400001},
		{16, 8, 1, 0x3f000001},
		{17, 14, 1, 0x3f6abcde},
		{31, 7, 2, 0x3f123457},
		{32, 14, 1, 0x3eaaaaab},
		{33, 3, 2, 0x3f7ffdd0},
		{63, 14, 1, 0x3f400001},
		{64, 8, 1, 0x3f6abcde},
	}
	for _, tc := range cases {
		t.Run("n"+strconv.Itoa(tc.n), func(t *testing.T) {
			k := 1 << tc.res
			if tc.blocks != 1 {
				k--
			}
			face := tc.n / 2
			sign := tc.n & 1
			iy := make([]int32, tc.n)
			for i := range tc.n {
				iy[i] = int32((i*97 + tc.n*31 + 13) % k)
			}
			var enc rangecoding.Encoder
			enc.Init(make([]byte, 256))
			enc.EncodeUniform(uint32(face), uint32(tc.n))
			enc.EncodeRawBits(uint32(sign), 1)
			for i, digit := range iy {
				if i != face {
					enc.EncodeRawBits(uint32(digit), uint(tc.res))
				}
			}
			packet := append([]byte(nil), enc.Done()...)
			if enc.Error() != 0 || len(packet) == 0 {
				t.Fatalf("range encode: error=%v bytes=%d", enc.Error(), len(packet))
			}
			payload := libopustest.NewOraclePayload("GQDI", uint32(tc.n), uint32(tc.res), uint32(tc.blocks), tc.gainBits, uint32(len(packet)))
			payload.Raw(packet)
			reader, err := libopustest.RunOracle(bin, payload.Bytes(), "QEXT cubic boundary grid", "GQDO")
			if err != nil {
				t.Fatal(err)
			}
			if got := reader.U32(); got != uint32(tc.n) {
				t.Fatalf("C vector length=%d want %d", got, tc.n)
			}
			wantCollapse, wantRange, wantTell := reader.U32(), reader.U32(), reader.U32()
			want := make([]uint32, tc.n)
			for i := range want {
				want[i] = reader.U32()
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			var dec rangecoding.Decoder
			dec.Init(packet)
			got := make([]celtNorm, tc.n)
			gotCollapse := cubicUnquant(got, tc.n, tc.res, tc.blocks, &dec, math.Float32frombits(tc.gainBits), nil)
			if uint32(gotCollapse) != wantCollapse || dec.Range() != wantRange || uint32(dec.TellFrac()) != wantTell {
				t.Fatalf("coder state: Go collapse=%d range=%08x tell=%d, C collapse=%d range=%08x tell=%d",
					gotCollapse, dec.Range(), dec.TellFrac(), wantCollapse, wantRange, wantTell)
			}
			for i := range got {
				if bits := math.Float32bits(float32(got[i])); bits != want[i] {
					t.Fatalf("X[%d]: Go=%08x C=%08x", i, bits, want[i])
				}
			}

			var scratch bandDecodeScratch
			dec.Init(packet)
			warm := make([]celtNorm, tc.n)
			cubicUnquant(warm, tc.n, tc.res, tc.blocks, &dec, math.Float32frombits(tc.gainBits), &scratch)
			allocs := testing.AllocsPerRun(20, func() {
				dec.Init(packet)
				cubicUnquant(warm, tc.n, tc.res, tc.blocks, &dec, math.Float32frombits(tc.gainBits), &scratch)
			})
			if allocs != 0 {
				t.Fatalf("warm cubic decode allocs/call=%g, want 0", allocs)
			}
		})
	}
}

func TestQEXTCubicBitsUsesActiveCoder(t *testing.T) {
	const n, budget, total = 4, 12 << bitRes, 18 << bitRes
	var packet [32]byte
	var enc rangecoding.Encoder
	enc.Init(packet[:])
	enc.EncodeRawBits(0, 6)
	var dec rangecoding.Decoder
	dec.Init(packet[:])

	// In celt/bands.c:quant_partition, the encoder has 11 bits left and
	// the fresh decoder has 17. After reserving n bits, divide by n-1
	// and subtract one to get the cubic resolution.
	for _, tc := range []struct {
		name string
		ctx  bandCtx
		want int
	}{
		{"encode", bandCtx{encode: true, extEnc: &enc, extTotalBits: total}, 1},
		{"decode", bandCtx{extDec: &dec, extTotalBits: total}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeQEXTCubicBits(&tc.ctx, budget, n); got != tc.want {
				t.Fatalf("cubic bits = %d, want %d", got, tc.want)
			}
		})
	}
}

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
		{"public_mono_multiframe_band0", 14, 1, 0, 0, 0x3f7ffdd0, [10]int32{0, 10831, 48, 5000, 15953, 11580, 481, 4308, 15595, 12409}},
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

var libopusQEXTCubicEncodeHelper libopustest.HelperCache

func buildLibopusQEXTCubicEncodeHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext cubic encode",
		OutputBase:  "gopus_libopus_celt_qext_cubic_encode",
		SourceFile:  "libopus_celt_qext_cubic_encode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTCubicEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTCubicEncodeHelper.Path(buildLibopusQEXTCubicEncodeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext cubic encode", err)
		return
	}
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok || cfg.EffBands != 2 {
		t.Fatalf("unexpected 48 kHz QEXT mode: %+v", cfg)
	}
	// These are the 20 active QEXT bins from an actual 5 ms packet after
	// normalise_bands. The C oracle receives the same bits as the Go encoder.
	actual5ms := [...]uint32{
		0x3ed0d715, 0xbc8faaaf, 0x3e0b71a1, 0xbe7a947b, 0xbea4ddf8,
		0x3ea5dfb2, 0xbe22534d, 0xbe310460, 0xbd05b59e, 0x3d0b517e,
		0xbe9c3d53, 0xbd44f02f, 0xbd8b2f8f, 0xbe96498a, 0xbe732a72,
		0xbd0b7242, 0x3ec84ab9, 0xbe3b6e76, 0xbe3e4f75, 0x3e1f36e6,
	}
	for _, tc := range []struct {
		name string
		lm   int
	}{
		{"base_2p5ms", 0},
		{"captured_5ms", 1},
		{"wide_10ms", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const storage = 20
			const balance = 1232
			const seed = uint32(0x00435000)
			M := 1 << tc.lm
			frameSize := cfg.ShortMDCTSize * M
			start := cfg.EBands[0] * M
			stop := cfg.EBands[2] * M
			active := make([]uint32, stop-start)
			if tc.lm == 1 {
				copy(active, actual5ms[:])
			} else {
				state := uint32(0x59f3ba21)
				for i := range active {
					state ^= state << 13
					state ^= state >> 17
					state ^= state << 5
					active[i] = math.Float32bits(float32(int32(state)>>16) * (1.0 / 65536))
				}
			}
			bandEBits := [2]uint32{0x4497681c, 0x4499f9a7}
			payload := libopustest.NewOraclePayload("GQCI", uint32(tc.lm), storage, balance, seed, uint32(len(active)))
			for _, bits := range active {
				payload.U32(bits)
			}
			for _, bits := range bandEBits {
				payload.U32(bits)
			}
			reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext cubic encode", "GQCO")
			if err != nil {
				t.Fatal(err)
			}
			refPacket := reader.Bytes(int(reader.U32()))
			refRange := reader.U32()
			refTell := reader.U32()
			refSeed := reader.U32()
			refCollapse := [2]uint32{reader.U32(), reader.U32()}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			x := make([]celtNorm, frameSize)
			for i, bits := range active {
				x[start+i] = celtNorm(math.Float32frombits(bits))
			}
			bandE := make([]celtEner, nbQEXTBands)
			for i, bits := range bandEBits {
				bandE[i] = celtEner(math.Float32frombits(bits))
			}
			var enc, dummy rangecoding.Encoder
			enc.Init(make([]byte, storage))
			dummy.Init(nil)
			gotSeed := seed
			zeros := make([]int32, nbQEXTBands)
			collapse := quantAllBandsEncodeScratchWithMode(&enc, 1, frameSize, tc.lm, 0, 2,
				x, nil, zeros, 1, spreadNormal, 0, 0, 0, zeros,
				storage*(8<<bitRes), balance, 2, false, &gotSeed, 10,
				bandE, &dummy, zeros, nil,
				cfg.EBands, cfg.LogN, cfg.CacheIndex, cfg.CacheBits)
			gotRange, gotTell := enc.Range(), enc.TellFrac()
			gotPacket := enc.Done()
			if len(gotPacket) != len(refPacket) || !bytes.Equal(gotPacket, refPacket) {
				t.Errorf("packet: Go length %d, C length %d, first difference %d", len(gotPacket), len(refPacket), firstDiffBytes(gotPacket, refPacket))
			}
			if gotRange != refRange || uint32(gotTell) != refTell || gotSeed != refSeed {
				t.Errorf("coder state: Go range %08x tell %d seed %08x, C range %08x tell %d seed %08x", gotRange, gotTell, gotSeed, refRange, refTell, refSeed)
			}
			if len(collapse) < 2 || uint32(collapse[0]) != refCollapse[0] || uint32(collapse[1]) != refCollapse[1] {
				t.Errorf("collapse: Go %v, C %v", collapse, refCollapse)
			}
		})
	}
}

func firstDiffBytes(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

func computeQEXTBandAmplitudesInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []celtEner) {
	coeffs := float32Slice(mdctCoeffs)
	computeQEXTBandAmplitudesF32Into(coeffs, cfg, end, lm, bandE)
}

func computeQEXTBandLogEInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []celtEner, bandLogE []celtGLog) {
	coeffs := float32Slice(mdctCoeffs)
	computeQEXTBandLogEF32Into(coeffs, cfg, end, lm, bandE, bandLogE)
}

func normalizeQEXTBandsInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []celtEner, norm []celtNorm) {
	coeffs := float32Slice(mdctCoeffs)
	normalizeQEXTBandsF32Into(coeffs, cfg, end, lm, bandE, norm)
}

var libopusQEXTExtBandsHelper libopustest.HelperCache

func buildLibopusQEXTExtBandsHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext ext bands",
		OutputBase:  "gopus_libopus_celt_qext_ext_bands",
		SourceFile:  "libopus_celt_qext_ext_bands_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type qextExtBandsCase struct {
	name         string
	channels     int
	lm           int
	qextEnd      int
	intensity    int
	dualStereo   int
	transient    bool
	extStorage   int
	mainStorage  int
	mainConsumed int
	xSeed        uint32
	eSeed        uint32
}

type qextExtBandsOracle struct {
	n            int
	qStartBin    int
	qStopBin     int
	extBytes     []byte
	decodedX     []float32 // C*(stop-start)
	decodedEne   []float32 // C*NB_QEXT_BANDS
	mainTellFrac int
	decQextEnd   int
	extPulses    []int32
	extQuant     []int32
}

func probeLibopusQEXTExtBands(t *testing.T, c qextExtBandsCase) qextExtBandsOracle {
	t.Helper()
	binPath, err := libopusQEXTExtBandsHelper.Path(buildLibopusQEXTExtBandsHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext ext bands", err)
	}
	payload := libopustest.NewOraclePayload("GQXI")
	payload.U32(uint32(c.channels))
	payload.U32(uint32(c.lm))
	payload.U32(uint32(c.qextEnd))
	payload.U32(uint32(c.intensity))
	payload.U32(uint32(c.dualStereo))
	payload.U32(boolToU32(c.transient))
	payload.U32(uint32(c.extStorage))
	payload.U32(uint32(c.mainStorage))
	payload.U32(uint32(c.mainConsumed))
	payload.U32(c.xSeed)
	payload.U32(c.eSeed)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext ext bands", "GQXO")
	if err != nil {
		t.Fatalf("run qext ext bands oracle: %v", err)
	}
	var o qextExtBandsOracle
	o.n = int(reader.U32())
	o.qStartBin = int(reader.U32())
	o.qStopBin = int(reader.U32())
	o.extBytes = reader.Bytes(int(reader.U32()))
	o.decodedX = make([]float32, int(reader.U32()))
	for i := range o.decodedX {
		o.decodedX[i] = reader.Float32()
	}
	o.decodedEne = make([]float32, int(reader.U32()))
	for i := range o.decodedEne {
		o.decodedEne[i] = reader.Float32()
	}
	o.mainTellFrac = int(reader.U32())
	o.decQextEnd = int(reader.U32())
	o.extPulses = make([]int32, o.decQextEnd)
	for i := range o.extPulses {
		o.extPulses[i] = int32(reader.U32())
	}
	o.extQuant = make([]int32, o.decQextEnd)
	for i := range o.extQuant {
		o.extQuant[i] = int32(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext ext bands oracle payload not fully consumed: %v", err)
	}
	return o
}

func boolToU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// advanceMainTell consumes mainConsumed raw bits from a fresh decoder over a
// mainStorage-byte buffer so its TellFrac() matches the C oracle's main coder
// (which feeds clt_compute_extra_allocation's qext_bits sizing). Raw end-bits
// advance nbits_total identically regardless of content, so the buffer contents
// are irrelevant; only the bit count matters.
func advanceMainTell(mainStorage, mainConsumed int) *rangecoding.Decoder {
	var rd rangecoding.Decoder
	rd.Init(make([]byte, mainStorage))
	left := mainConsumed
	for left > 0 {
		n := left
		if n > 16 {
			n = 16
		}
		rd.DecodeRawBits(uint(n))
		left -= n
	}
	return &rd
}

// TestQEXTExtensionBandsContentMatchesLibopusOracle drives the full libopus
// QEXT extension-band content coding chain (coarse energy + fine energy +
// theta/PVQ via quant_all_bands, all from one ext_ec) for the native 96 kHz
// mode, then replays the *decode* path through gopus's prepareQEXTDecode +
// decodeQEXTBands against the same coded bytes and main-coder tell state. The
// decoded extension X coefficients and qext band energies must match the C
// reference bit-for-bit with matching feature and scalar/SIMD builds.
func TestQEXTExtensionBandsContentMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := []qextExtBandsCase{
		{
			name: "mono_qe14", channels: 1, lm: 3, qextEnd: nbQEXTBands,
			extStorage: 128, mainStorage: 1024, mainConsumed: 2000,
			xSeed: 0x1234abcd, eSeed: 0x55aa00ff,
		},
		{
			name: "mono_qe2", channels: 1, lm: 3, qextEnd: 2,
			extStorage: 48, mainStorage: 512, mainConsumed: 1200,
			xSeed: 0x0badf00d, eSeed: 0x13371337,
		},
		{
			name: "mono_qe14_transient", channels: 1, lm: 3, qextEnd: nbQEXTBands,
			transient:  true,
			extStorage: 128, mainStorage: 1024, mainConsumed: 2000,
			xSeed: 0x77777777, eSeed: 0x88888888,
		},
		{
			name: "stereo_qe14", channels: 2, lm: 3, qextEnd: nbQEXTBands,
			intensity: nbQEXTBands, dualStereo: 0,
			extStorage: 160, mainStorage: 1024, mainConsumed: 2400,
			xSeed: 0xfeedbeef, eSeed: 0x0f0f0f0f,
		},
		{
			name: "stereo_qe14_dual", channels: 2, lm: 3, qextEnd: nbQEXTBands,
			intensity: nbQEXTBands, dualStereo: 1,
			extStorage: 160, mainStorage: 1024, mainConsumed: 2400,
			xSeed: 0xabad1dea, eSeed: 0xc0ffee11,
		},
		{
			name: "stereo_qe2", channels: 2, lm: 3, qextEnd: 2,
			intensity: 2, dualStereo: 0,
			extStorage: 64, mainStorage: 512, mainConsumed: 1500,
			xSeed: 0x90909090, eSeed: 0x12345678,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			o := probeLibopusQEXTExtBands(t, c)

			frameSize := 240 << c.lm // shortMdctSize(240) << LM = 1920 for LM=3
			if o.n != frameSize {
				t.Fatalf("oracle N=%d want %d", o.n, frameSize)
			}

			dec := NewDecoder(c.channels)
			dec.sampleRate = 96000
			dec.rng = 0

			mainRD := advanceMainTell(c.mainStorage, c.mainConsumed)
			if got := mainRD.TellFrac(); got != o.mainTellFrac {
				t.Fatalf("main TellFrac=%d want oracle %d", got, o.mainTellFrac)
			}
			qext := dec.prepareQEXTDecode(o.extBytes, mainRD, MaxBands, c.lm, frameSize)
			if qext == nil {
				t.Fatal("prepareQEXTDecode returned nil for valid qext payload")
			}
			if qext.end != c.qextEnd {
				t.Fatalf("decoded qext_end=%d want %d", qext.end, c.qextEnd)
			}
			for i := 0; i < c.qextEnd; i++ {
				if got := qext.extraPulses[MaxBands+i]; got != o.extPulses[i] {
					t.Fatalf("ext pulses[%d]=%d want %d", i, got, o.extPulses[i])
				}
				if got := qext.extraQuant[MaxBands+i]; got != o.extQuant[i] {
					t.Fatalf("ext quant[%d]=%d want %d", i, got, o.extQuant[i])
				}
			}

			// gopus's quant_all_bands convention takes the block count B directly:
			// B = M (1<<LM) when transient, else 1. libopus passes the transient
			// flag and computes B = shortBlocks ? M : 1 internally.
			blocks := 1
			if c.transient {
				blocks = 1 << c.lm
			}
			dec.decodeQEXTBands(frameSize, c.lm, blocks, spreadNormal, false, qext)

			// Compare decoded extension X over the qext band bin range.
			span := o.qStopBin - o.qStartBin
			if len(o.decodedX) != c.channels*span {
				t.Fatalf("oracle decodedX len=%d want %d", len(o.decodedX), c.channels*span)
			}
			assertQEXTFloatSlice(t, "decoded X (L)", qext.coeffsL[o.qStartBin:o.qStopBin], o.decodedX[:span])
			if c.channels == 2 {
				assertQEXTFloatSlice(t, "decoded X (R)", qext.coeffsR[o.qStartBin:o.qStopBin], o.decodedX[span:2*span])
			}

			// Compare decoded qext band energies over [0,qext_end) per channel.
			// gopus lays out qext.energies as [c*qext_end + band]; the oracle dumps
			// [c*NB_QEXT_BANDS + band].
			gotEne := make([]float32, c.channels*c.qextEnd)
			wantEne := make([]float32, c.channels*c.qextEnd)
			for ch := 0; ch < c.channels; ch++ {
				for band := 0; band < c.qextEnd; band++ {
					gotEne[ch*c.qextEnd+band] = float32(qext.energies[ch*c.qextEnd+band])
					wantEne[ch*c.qextEnd+band] = o.decodedEne[ch*nbQEXTBands+band]
				}
			}
			assertQEXTFloatSliceF32(t, "decoded qext energies", gotEne, wantEne)
		})
	}
}

func assertQEXTFloatSlice(t *testing.T, label string, got []celtNorm, want []float32) {
	t.Helper()
	g := make([]float32, len(got))
	for i := range got {
		g[i] = float32(got[i])
	}
	assertQEXTFloatSliceF32(t, label, g, want)
}

// assertQEXTFloatSliceF32 compares the selected C decoder's exact float bits.
func assertQEXTFloatSliceF32(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length Go=%d C=%d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s sample %d Go=%08x C=%08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

var libopusQEXTExtModeHelper libopustest.HelperCache

func buildLibopusQEXTExtModeHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext extmode",
		OutputBase:  "gopus_libopus_celt_qext_extmode",
		SourceFile:  "libopus_celt_qext_extmode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// qextExtModeOracle is the libopus compute_qext_mode() result plus the
// precomputed qext_cache PulseCache for the native 96 kHz mode.
type qextExtModeOracle struct {
	BaseFs, BaseShortMdctSize int
	NbEBands, EffEBands       int
	EBands, LogN              []int16
	CacheSize                 int
	CacheIndex                []int16
	CacheBits                 []uint8
	CacheCaps                 []uint8
}

func probeLibopusQEXTExtMode(t *testing.T) qextExtModeOracle {
	t.Helper()
	binPath, err := libopusQEXTExtModeHelper.Path(buildLibopusQEXTExtModeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext extmode", err)
	}
	payload := libopustest.NewOraclePayload("GQEI", 1)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext extmode", "GQEO")
	if err != nil {
		t.Fatalf("run qext extmode oracle: %v", err)
	}
	var o qextExtModeOracle
	o.BaseFs = int(reader.U32())
	o.BaseShortMdctSize = int(reader.U32())
	o.NbEBands = int(reader.U32())
	o.EffEBands = int(reader.U32())
	o.EBands = make([]int16, int(reader.U32()))
	for i := range o.EBands {
		o.EBands[i] = reader.I16()
	}
	o.LogN = make([]int16, int(reader.U32()))
	for i := range o.LogN {
		o.LogN[i] = reader.I16()
	}
	o.CacheSize = int(reader.U32())
	o.CacheIndex = make([]int16, int(reader.U32()))
	for i := range o.CacheIndex {
		o.CacheIndex[i] = reader.I16()
	}
	o.CacheBits = reader.Bytes(int(reader.U32()))
	o.CacheCaps = reader.Bytes(int(reader.U32()))
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext extmode oracle payload not fully consumed: %v", err)
	}
	return o
}

// TestComputeQEXTModeConfigMatchesLibopusOracle verifies that gopus's
// computeQEXTModeConfig() for the native 96 kHz mode reproduces libopus's
// compute_qext_mode() output (extension eBands, logN, nbEBands, effEBands)
// and the precomputed qext_cache PulseCache (index/bits/caps) exactly. These
// integer tables are platform-independent, so any mismatch fails on all archs.
func TestComputeQEXTModeConfigMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	o := probeLibopusQEXTExtMode(t)

	if o.BaseFs != 96000 || o.BaseShortMdctSize != 240 {
		t.Fatalf("unexpected base mode from oracle: Fs=%d shortMdctSize=%d", o.BaseFs, o.BaseShortMdctSize)
	}

	cfg, ok := computeQEXTModeConfig(o.BaseFs, o.BaseShortMdctSize)
	if !ok {
		t.Fatalf("computeQEXTModeConfig(%d,%d)=false want true", o.BaseFs, o.BaseShortMdctSize)
	}

	if got := nbQEXTBands; got != o.NbEBands {
		t.Fatalf("nbEBands: gopus NB_QEXT_BANDS=%d oracle=%d", got, o.NbEBands)
	}
	if cfg.EffBands != o.EffEBands {
		t.Fatalf("effEBands: gopus=%d oracle=%d", cfg.EffBands, o.EffEBands)
	}

	// The C eBands table has nbEBands+1 edges; gopus stores the full 15-entry
	// source table (qextEBands240). Both must agree edge-for-edge.
	if len(cfg.EBands) != len(o.EBands) {
		t.Fatalf("len(eBands): gopus=%d oracle=%d", len(cfg.EBands), len(o.EBands))
	}
	for i := range o.EBands {
		if int16(cfg.EBands[i]) != o.EBands[i] {
			t.Fatalf("eBands[%d]: gopus=%d oracle=%d", i, cfg.EBands[i], o.EBands[i])
		}
	}

	if len(cfg.LogN) != len(o.LogN) {
		t.Fatalf("len(logN): gopus=%d oracle=%d", len(cfg.LogN), len(o.LogN))
	}
	for i := range o.LogN {
		if int16(cfg.LogN[i]) != o.LogN[i] {
			t.Fatalf("logN[%d]: gopus=%d oracle=%d", i, cfg.LogN[i], o.LogN[i])
		}
	}

	// The precomputed qext_cache PulseCache.
	if len(cfg.CacheIndex) != len(o.CacheIndex) {
		t.Fatalf("len(cache.index): gopus=%d oracle=%d", len(cfg.CacheIndex), len(o.CacheIndex))
	}
	for i := range o.CacheIndex {
		if cfg.CacheIndex[i] != o.CacheIndex[i] {
			t.Fatalf("cache.index[%d]: gopus=%d oracle=%d", i, cfg.CacheIndex[i], o.CacheIndex[i])
		}
	}

	if len(cfg.CacheBits) != o.CacheSize {
		t.Fatalf("len(cache.bits): gopus=%d oracle cache.size=%d", len(cfg.CacheBits), o.CacheSize)
	}
	if len(cfg.CacheBits) != len(o.CacheBits) {
		t.Fatalf("len(cache.bits): gopus=%d oracle=%d", len(cfg.CacheBits), len(o.CacheBits))
	}
	for i := range o.CacheBits {
		if cfg.CacheBits[i] != o.CacheBits[i] {
			t.Fatalf("cache.bits[%d]: gopus=%d oracle=%d", i, cfg.CacheBits[i], o.CacheBits[i])
		}
	}

	if len(cfg.CacheCaps) != len(o.CacheCaps) {
		t.Fatalf("len(cache.caps): gopus=%d oracle=%d", len(cfg.CacheCaps), len(o.CacheCaps))
	}
	for i := range o.CacheCaps {
		if cfg.CacheCaps[i] != o.CacheCaps[i] {
			t.Fatalf("cache.caps[%d]: gopus=%d oracle=%d", i, cfg.CacheCaps[i], o.CacheCaps[i])
		}
	}
}

var libopusQEXTExtraAllocHelper libopustest.HelperCache

func buildLibopusQEXTExtraAllocHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext extra alloc",
		OutputBase:  "gopus_libopus_celt_qext_extra_alloc",
		SourceFile:  "libopus_celt_qext_extra_alloc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type qextExtraAllocCase struct {
	name         string
	channels     int
	lm           int
	start        int
	end          int
	qextEnd      int
	totalQ3      int32
	toneFreq     float32
	toneishness  float32
	storageBytes int
	bandLogE     []float32
	qextBandLogE []float32
}

type qextExtraAllocOracle struct {
	totBands  int
	encPulses []int32
	encQuant  []int32
	encBytes  []byte
	encTell   uint32
	decPulses []int32
	decQuant  []int32
}

func probeLibopusQEXTExtraAlloc(t *testing.T, c qextExtraAllocCase) qextExtraAllocOracle {
	t.Helper()
	binPath, err := libopusQEXTExtraAllocHelper.Path(buildLibopusQEXTExtraAllocHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext extra alloc", err)
	}
	payload := libopustest.NewOraclePayload("GQAI")
	payload.U32(uint32(c.channels))
	payload.U32(uint32(c.lm))
	payload.U32(uint32(c.start))
	payload.U32(uint32(c.end))
	payload.U32(uint32(c.qextEnd))
	payload.I32(c.totalQ3)
	payload.Float32(c.toneFreq)
	payload.Float32(c.toneishness)
	payload.U32(uint32(c.storageBytes))
	payload.U32(uint32(len(c.bandLogE)))
	payload.Float32s(c.bandLogE...)
	payload.U32(uint32(len(c.qextBandLogE)))
	payload.Float32s(c.qextBandLogE...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext extra alloc", "GQAO")
	if err != nil {
		t.Fatalf("run qext extra alloc oracle: %v", err)
	}
	var o qextExtraAllocOracle
	o.totBands = int(reader.U32())
	o.encPulses = make([]int32, int(reader.U32()))
	for i := range o.encPulses {
		o.encPulses[i] = reader.I32()
	}
	o.encQuant = make([]int32, int(reader.U32()))
	for i := range o.encQuant {
		o.encQuant[i] = reader.I32()
	}
	o.encBytes = reader.Bytes(int(reader.U32()))
	o.encTell = reader.U32()
	o.decPulses = make([]int32, int(reader.U32()))
	for i := range o.decPulses {
		o.decPulses[i] = reader.I32()
	}
	o.decQuant = make([]int32, int(reader.U32()))
	for i := range o.decQuant {
		o.decQuant[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext extra alloc oracle payload not fully consumed: %v", err)
	}
	return o
}

// makeQEXTLogE builds reproducible main/qext band-energy vectors for the given
// channel count using a deterministic slope plus a per-channel offset.
func makeQEXTLogE(channels, bands int, base, slope, chanOffset float32) []float32 {
	out := make([]float32, channels*bands)
	for c := 0; c < channels; c++ {
		for i := 0; i < bands; i++ {
			out[c*bands+i] = base + slope*float32(i) + chanOffset*float32(c)
		}
	}
	return out
}

// TestComputeQEXTExtraAllocationMatchesLibopusOracle drives gopus's QEXT
// extension-band allocation (computeQEXTExtraAllocationEncode/Decode) with the
// same inputs as libopus clt_compute_extra_allocation() and asserts the
// per-band extra_pulses/extra_quant arrays and the encoded range-coder bytes
// match integer-for-integer on both the encode and decode side. These are
// integer tables, so a mismatch fails on every platform.
func TestComputeQEXTExtraAllocationMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	cfg, ok := computeQEXTModeConfig(96000, 240)
	if !ok {
		t.Fatal("computeQEXTModeConfig(96000,240)=false want true")
	}

	cases := []qextExtraAllocCase{
		{
			name: "mono_qe14_high", channels: 1, lm: 3, start: 0, end: MaxBands,
			qextEnd: nbQEXTBands, totalQ3: 4096, toneFreq: 0.5, toneishness: 0.3,
			storageBytes: 96,
			bandLogE:     makeQEXTLogE(1, MaxBands, 0.25, 0.05, 0),
			qextBandLogE: makeQEXTLogE(1, nbQEXTBands, -0.2, 0.08, 0),
		},
		{
			name: "stereo_qe14_high", channels: 2, lm: 3, start: 0, end: MaxBands,
			qextEnd: nbQEXTBands, totalQ3: 6144, toneFreq: 0.9, toneishness: 0.42,
			storageBytes: 96,
			bandLogE:     makeQEXTLogE(2, MaxBands, 0.25, 0.05, -0.1),
			qextBandLogE: makeQEXTLogE(2, nbQEXTBands, -0.2, 0.08, -0.05),
		},
		{
			name: "mono_qe2_low", channels: 1, lm: 3, start: 0, end: MaxBands,
			qextEnd: 2, totalQ3: 1536, toneFreq: 1.5, toneishness: 0.99,
			storageBytes: 48,
			bandLogE:     makeQEXTLogE(1, MaxBands, 0.1, 0.03, 0),
			qextBandLogE: makeQEXTLogE(1, nbQEXTBands, -0.4, 0.06, 0),
		},
		{
			name: "stereo_qe2_low", channels: 2, lm: 3, start: 0, end: MaxBands,
			qextEnd: 2, totalQ3: 2048, toneFreq: 0.2, toneishness: 0.7,
			storageBytes: 48,
			bandLogE:     makeQEXTLogE(2, MaxBands, 0.0, 0.04, 0.07),
			qextBandLogE: makeQEXTLogE(2, nbQEXTBands, -0.3, 0.05, 0.04),
		},
		{
			name: "mono_qe14_tiny_budget", channels: 1, lm: 3, start: 0, end: MaxBands,
			qextEnd: nbQEXTBands, totalQ3: 64, toneFreq: 0.5, toneishness: 0.3,
			storageBytes: 16,
			bandLogE:     makeQEXTLogE(1, MaxBands, 0.25, 0.05, 0),
			qextBandLogE: makeQEXTLogE(1, nbQEXTBands, -0.2, 0.08, 0),
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			o := probeLibopusQEXTExtraAlloc(t, c)
			if o.totBands != c.end+c.qextEnd {
				t.Fatalf("oracle totBands=%d want %d", o.totBands, c.end+c.qextEnd)
			}

			mainLogE := toCeltGLog(c.bandLogE)
			qextLogE := toCeltGLog(c.qextBandLogE)

			// Encode side.
			var enc rangecoding.Encoder
			encBuf := make([]byte, c.storageBytes)
			enc.Init(encBuf)

			encPulses := make([]int32, MaxBands+nbQEXTBands)
			encQuant := make([]int32, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationEncode(
				c.start, c.end, c.qextEnd, int(c.totalQ3), c.channels, c.lm,
				mainLogE, qextLogE, &cfg, c.toneFreq, c.toneishness,
				&enc, encPulses, encQuant,
			)
			enc.Done()
			// Compare the whole finalized storage buffer: the C oracle dumps all
			// storageBytes after ec_enc_done, and this allocation path uses no raw
			// back-of-buffer bits, so the buffers must be byte-identical.
			gotBytes := append([]byte(nil), enc.Buffer()[:c.storageBytes]...)

			// gopus indexes qext bands at [end .. end+qextEnd); the oracle dumps a
			// contiguous [start .. end+qextEnd) array. Build the comparison slice.
			gotEncPulses := gatherQEXTAlloc(encPulses, c.end, c.qextEnd)
			gotEncQuant := gatherQEXTAlloc(encQuant, c.end, c.qextEnd)

			assertIntSliceEqual(t, "enc extra_pulses", gotEncPulses, o.encPulses)
			assertIntSliceEqual(t, "enc extra_quant", gotEncQuant, o.encQuant)

			if !bytes.Equal(gotBytes, o.encBytes) {
				t.Fatalf("enc bytes mismatch\n gopus=%x\noracle=%x", gotBytes, o.encBytes)
			}

			// Decode side: replay the same storage-sized buffer so the
			// ec_tell_frac storage gate matches the C reference exactly.
			decBuf := make([]byte, c.storageBytes)
			copy(decBuf, o.encBytes)
			var dec rangecoding.Decoder
			dec.Init(decBuf)

			decPulses := make([]int32, MaxBands+nbQEXTBands)
			decQuant := make([]int32, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationDecodeWithMode(
				c.start, c.end, c.qextEnd, int(c.totalQ3), c.channels, c.lm,
				&dec, decPulses, decQuant, &cfg,
			)
			gotDecPulses := gatherQEXTAlloc(decPulses, c.end, c.qextEnd)
			gotDecQuant := gatherQEXTAlloc(decQuant, c.end, c.qextEnd)

			assertIntSliceEqual(t, "dec extra_pulses", gotDecPulses, o.decPulses)
			assertIntSliceEqual(t, "dec extra_quant", gotDecQuant, o.decQuant)
		})
	}
}

// gatherQEXTAlloc returns the contiguous [0..end+qextEnd) view of a gopus
// extra-allocation array, which stores main bands at [0,end) and qext bands at
// [MaxBands, MaxBands+qextEnd). end equals MaxBands for the 96 kHz mode, so the
// two regions are already contiguous.
func gatherQEXTAlloc(arr []int32, end, qextEnd int) []int32 {
	out := make([]int32, end+qextEnd)
	copy(out[:end], arr[:end])
	copy(out[end:], arr[MaxBands:MaxBands+qextEnd])
	return out
}

func toCeltGLog(in []float32) []celtGLog {
	out := make([]celtGLog, len(in))
	for i, v := range in {
		out[i] = celtGLog(v)
	}
	return out
}

func assertIntSliceEqual(t *testing.T, label string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: gopus=%d oracle=%d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] mismatch: gopus=%d oracle=%d\n gopus=%v\noracle=%v",
				label, i, got[i], want[i], got, want)
		}
	}
}

var libopusQEXTFinaliseHelper libopustest.HelperCache

func buildLibopusQEXTFinaliseHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext energy finalise",
		OutputBase:  "gopus_libopus_celt_qext_finalise",
		SourceFile:  "libopus_celt_qext_finalise_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTEnergyFinaliseFromBackupMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTFinaliseHelper.Path(buildLibopusQEXTFinaliseHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext energy finalise", err)
		return
	}
	for _, tc := range []struct {
		name     string
		channels int
		start    int
		end      int
		bitsLeft int
		withOld  bool
	}{
		{"mono_full_backup", 1, 0, MaxBands, 13, false},
		{"stereo_full_backup", 2, 0, MaxBands, 23, false},
		{"mono_narrow_backup", 1, 0, 13, 11, false},
		{"stereo_narrow_backup", 2, 0, 13, 17, false},
		{"stereo_hybrid_backup", 2, 17, MaxBands, 9, false},
		{"mono_full_history", 1, 0, MaxBands, 13, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const storage = 32
			var fineQuant, finePriority [MaxBands]int32
			for i := range fineQuant {
				fineQuant[i] = int32(1 + i%5)
				finePriority[i] = int32(i % 2)
			}
			old := make([]celtGLog, tc.channels*tc.end)
			residual := make([]celtGLog, len(old))
			for i := range old {
				old[i] = celtGLog(float32((i*7)%31-15) * 0.125)
				residual[i] = celtGLog(float32((i*11)%43-21) * (1.0 / 64.0))
			}
			oldBefore := append([]celtGLog(nil), old...)

			withOld := uint32(0)
			if tc.withOld {
				withOld = 1
			}
			payload := libopustest.NewOraclePayload("GQFI", uint32(tc.channels), uint32(tc.start), uint32(tc.end), uint32(tc.bitsLeft), storage, withOld)
			for _, v := range fineQuant {
				payload.I32(v)
			}
			for _, v := range finePriority {
				payload.I32(v)
			}
			for _, v := range old {
				payload.Float32(float32(v))
			}
			for _, v := range residual {
				payload.Float32(float32(v))
			}
			reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext energy finalise", "GQFO")
			if err != nil {
				t.Fatal(err)
			}
			refPacket := reader.Bytes(int(reader.U32()))
			refRange, refTell := reader.U32(), reader.U32()
			refOld := make([]uint32, len(old))
			refResidual := make([]uint32, len(residual))
			for i := range refOld {
				refOld[i] = math.Float32bits(reader.Float32())
			}
			for i := range refResidual {
				refResidual[i] = math.Float32bits(reader.Float32())
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			var enc rangecoding.Encoder
			enc.Init(make([]byte, storage))
			// Keep the C coder's full storage layout, including the gap before
			// raw end bits. Done otherwise compacts the returned Go slice.
			enc.Shrink(storage)
			var history []celtGLog
			if tc.withOld {
				history = old
			}
			encodeEnergyFinaliseResidual(&enc, history, residual, tc.start, tc.end, tc.channels,
				fineQuant[:], finePriority[:], tc.bitsLeft)
			gotRange, gotTell := enc.Range(), enc.TellFrac()
			gotPacket := enc.Done()
			if len(gotPacket) != len(refPacket) || !bytes.Equal(gotPacket, refPacket) {
				t.Errorf("packet: Go %x, C %x", gotPacket, refPacket)
			}
			if gotRange != refRange || uint32(gotTell) != refTell {
				t.Errorf("coder state: Go range %08x tell %d, C range %08x tell %d", gotRange, gotTell, refRange, refTell)
			}
			for i := range old {
				if got := math.Float32bits(float32(old[i])); got != refOld[i] {
					t.Errorf("history[%d]: Go %08x, C %08x", i, got, refOld[i])
				}
				if got := math.Float32bits(float32(residual[i])); got != refResidual[i] {
					t.Errorf("backup residual[%d]: Go %08x, C %08x", i, got, refResidual[i])
				}
				if !tc.withOld && old[i] != oldBefore[i] {
					t.Errorf("backup path modified history[%d]", i)
				}
			}
		})
	}
}

func TestQEXTReservedBytesWithoutExtraBandMode(t *testing.T) {
	// celt_encoder.c reserves extension bytes at narrow bandwidth, but
	// creates the extra-band mode only when end == mode->nbEBands.
	for _, tc := range []struct {
		name     string
		channels int
	}{{"mono", 1}, {"stereo", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			channels := tc.channels
			enc := NewEncoder(channels)
			enc.SetBandwidth(CELTNarrowband)
			enc.SetBitrate(256000)
			enc.SetQEXTEnabled(true)
			history := enc.ensureQEXTOldBandE(channels)
			for i := range history {
				history[i] = celtGLog(float32(i+1) * 0.125)
			}
			historyBefore := append([]celtGLog(nil), history...)
			const frameSize = 960
			pcm := make([]float32, frameSize*channels)
			for i := range frameSize {
				for c := range channels {
					pcm[i*channels+c] = float32(0.37 * math.Sin(2*math.Pi*(703+float64(c)*157)*float64(i)/48000))
				}
			}
			for range 4 {
				if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
					t.Fatal(err)
				}
				if len(enc.LastQEXTPayload()) == 0 {
					t.Fatal("narrowband frame did not reserve extension bytes")
				}
				for i, want := range historyBefore {
					if history[i] != want {
						t.Fatalf("narrowband frame modified extra-band history[%d]: got %08x want %08x", i,
							math.Float32bits(float32(history[i])), math.Float32bits(float32(want)))
					}
				}
			}
		})
	}
}

var qextFineEnergyHelper libopustest.HelperCache

func buildQEXTFineEnergyHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext fine energy",
		OutputBase:  "gopus_libopus_celt_qext_fine_energy",
		SourceFile:  "libopus_celt_qext_fine_energy_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTFineEnergyStridedStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	path, err := qextFineEnergyHelper.Path(buildQEXTFineEnergyHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext fine energy", err)
		return
	}
	for _, tc := range []struct {
		fs, end, physical, coded int
	}{
		{48000, 2, 2, 1},
		{48000, 2, 2, 2},
		{96000, nbQEXTBands, 2, 2},
	} {
		name := fmt.Sprintf("fs%d-physical%d-coded%d-end%d", tc.fs, tc.physical, tc.coded, tc.end)
		t.Run(name, func(t *testing.T) {
			const storage = 64
			oldC := make([]float32, nbQEXTBands*tc.coded)
			errorC := make([]float32, len(oldC))
			for i := range oldC {
				oldC[i] = float32(i%nbQEXTBands)*0.125 - 1.5 + float32(i/nbQEXTBands)*0.375
				errorC[i] = float32((i*13)%17-8) * 0.045
			}
			var fineBits [nbQEXTBands]uint32
			for band := range fineBits {
				if band%3 != 0 {
					fineBits[band] = uint32(1 + band%4)
				}
			}

			payload := libopustest.NewOraclePayload("GQFI")
			payload.U32(uint32(tc.coded))
			payload.U32(uint32(tc.fs))
			payload.U32(uint32(tc.end))
			payload.U32(storage)
			payload.Float32s(oldC...)
			payload.Float32s(errorC...)
			payload.U32s(fineBits[:]...)
			reader, err := libopustest.RunOracle(path, payload.Bytes(), "celt qext fine energy", "GQFO")
			if err != nil {
				t.Fatal(err)
			}
			wantError := reader.U32()
			wantRange := reader.U32()
			wantPacketLen := int(reader.U32())
			wantOld := make([]float32, len(oldC))
			wantResidual := make([]float32, len(oldC))
			for i := range wantOld {
				wantOld[i] = reader.Float32()
			}
			for i := range wantResidual {
				wantResidual[i] = reader.Float32()
			}
			wantPacket := append([]byte(nil), reader.Bytes(wantPacketLen)...)
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			enc := NewEncoder(tc.physical)
			enc.SetStreamChannels(tc.coded)
			state := enc.ensureQEXTOldBandE(tc.physical)
			for i := range state {
				state[i] = -99
			}
			for ch := range tc.coded {
				for band := range nbQEXTBands {
					state[ch*MaxBands+band] = celtGLog(oldC[ch*nbQEXTBands+band])
				}
			}
			residual := make([]celtGLog, tc.end*tc.coded)
			for ch := range tc.coded {
				for band := range tc.end {
					residual[ch*tc.end+band] = celtGLog(errorC[ch*nbQEXTBands+band])
				}
			}
			bits := make([]int32, nbQEXTBands)
			for i, v := range fineBits {
				bits[i] = int32(v)
			}
			buffer := make([]byte, storage)
			var re rangecoding.Encoder
			re.Init(buffer)
			enc.encodeFineEnergyFromErrorWithEncoder(&re, state[:MaxBands*tc.coded], tc.end, MaxBands, bits[:tc.end], residual)
			gotRange := re.Range()
			gotPacket := re.Done()
			if uint32(re.Error()) != wantError || gotRange != wantRange || !bytes.Equal(gotPacket, wantPacket) {
				t.Fatalf("coder: error %d/%d range %08x/%08x packet %x/%x", re.Error(), wantError, gotRange, wantRange, gotPacket, wantPacket)
			}
			for ch := range tc.coded {
				for band := range nbQEXTBands {
					idxC := ch*nbQEXTBands + band
					idxGo := ch*MaxBands + band
					if math.Float32bits(float32(state[idxGo])) != math.Float32bits(wantOld[idxC]) {
						t.Fatalf("oldBandE ch%d band%d: Go %08x C %08x", ch, band, math.Float32bits(float32(state[idxGo])), math.Float32bits(wantOld[idxC]))
					}
					if band < tc.end && math.Float32bits(float32(residual[ch*tc.end+band])) != math.Float32bits(wantResidual[idxC]) {
						t.Fatalf("error ch%d band%d: Go %08x C %08x", ch, band, math.Float32bits(float32(residual[ch*tc.end+band])), math.Float32bits(wantResidual[idxC]))
					}
				}
			}
			for ch := range tc.physical {
				for band := nbQEXTBands; band < MaxBands; band++ {
					if state[ch*MaxBands+band] != -99 {
						t.Fatalf("untouched Go history ch%d band%d changed", ch, band)
					}
				}
			}
			if tc.coded == 1 {
				for band := range MaxBands {
					if state[MaxBands+band] != -99 {
						t.Fatalf("uncoded physical channel band%d changed", band)
					}
				}
			}
		})
	}
}

func TestComputeQEXTModeConfig48k(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}
	if cfg.ShortMDCTSize != 120 {
		t.Fatalf("ShortMDCTSize=%d want 120", cfg.ShortMDCTSize)
	}
	if cfg.EffBands != 2 {
		t.Fatalf("EffBands=%d want 2", cfg.EffBands)
	}
	if len(cfg.EBands) != len(qextEBands240) {
		t.Fatalf("len(EBands)=%d want %d", len(cfg.EBands), len(qextEBands240))
	}
	if len(cfg.LogN) != nbQEXTBands {
		t.Fatalf("len(LogN)=%d want %d", len(cfg.LogN), nbQEXTBands)
	}
	if cfg.EBands[0] != 100 || cfg.EBands[2] != 120 || cfg.EBands[14] != 240 {
		t.Fatalf("unexpected qext eBands: first=%d third=%d last=%d", cfg.EBands[0], cfg.EBands[2], cfg.EBands[14])
	}
	for i, got := range cfg.LogN {
		if got != 27 {
			t.Fatalf("LogN[%d]=%d want 27", i, got)
		}
	}
	if len(cfg.CacheIndex) != len(qextCacheIndex50) || len(cfg.CacheBits) != len(qextCacheBits50) || len(cfg.CacheCaps) != len(qextCacheCaps50) {
		t.Fatalf("unexpected cache lengths: index=%d bits=%d caps=%d", len(cfg.CacheIndex), len(cfg.CacheBits), len(cfg.CacheCaps))
	}
}

func TestComputeQEXTModeConfig96k(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(96000, 240)
	if !ok {
		t.Fatal("computeQEXTModeConfig(96000,240)=false want true")
	}
	if cfg.EffBands != nbQEXTBands {
		t.Fatalf("EffBands=%d want %d", cfg.EffBands, nbQEXTBands)
	}
	if cfg.EBands[0] != 100 || cfg.EBands[14] != 240 {
		t.Fatalf("unexpected qext eBands for 96k mode: first=%d last=%d", cfg.EBands[0], cfg.EBands[14])
	}
}

func TestComputeQEXTModeConfig180Path(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(96000, 180)
	if !ok {
		t.Fatal("computeQEXTModeConfig(96000,180)=false want true")
	}
	if cfg.EffBands != nbQEXTBands {
		t.Fatalf("EffBands=%d want %d", cfg.EffBands, nbQEXTBands)
	}
	if cfg.EBands[0] != 74 || cfg.EBands[14] != 180 {
		t.Fatalf("unexpected qext eBands 180-path: first=%d last=%d", cfg.EBands[0], cfg.EBands[14])
	}
	if got, want := cfg.LogN[0], 24; got != want {
		t.Fatalf("LogN[0]=%d want %d", got, want)
	}
	if got, want := cfg.LogN[len(cfg.LogN)-1], 21; got != want {
		t.Fatalf("LogN[last]=%d want %d", got, want)
	}
}

func TestComputeQEXTModeConfigRejectsUnknownModeConfig(t *testing.T) {
	if _, ok := computeQEXTModeConfig(48000, 96); ok {
		t.Fatal("computeQEXTModeConfig(48000,96)=true want false")
	}
}

func TestComputeQEXTReservationTruncatesFloatCompoundAssignment(t *testing.T) {
	// celt_encoder.c computes qext_bytes += scale * delta, where the float
	// result is converted back to int only after it is added to qext_bytes.
	// With this input, the initial reservation is 1042 and the adjustment is
	// -521.25: C truncates 520.75 to 520, rather than rounding the adjustment
	// to -521 and producing 521.
	mainBytes, payloadBytes, paddingBytes := computeQEXTReservation(1503, 2, 960, 1, 48000, 0.5, 0)
	if mainBytes != 979 || payloadBytes != 519 || paddingBytes != 3 {
		t.Fatalf("computeQEXTReservation()=(%d,%d,%d), want (979,519,3)", mainBytes, payloadBytes, paddingBytes)
	}
}

func TestQEXTDepthRoundTrip(t *testing.T) {
	depths := []int{0, 7, 7, 48, 12, 0, 48, 3, 3, 0}

	var enc rangecoding.Encoder
	buf := make([]byte, 64)
	enc.Init(buf)
	lastEnc := 0
	for _, depth := range depths {
		encodeQEXTDepth(&enc, depth, 48, &lastEnc)
	}
	packet := append([]byte(nil), enc.Done()...)

	var dec rangecoding.Decoder
	dec.Init(packet)
	lastDec := 0
	for i, want := range depths {
		if got := decodeQEXTDepth(&dec, 48, &lastDec); got != want {
			t.Fatalf("decodeQEXTDepth[%d]=%d want %d", i, got, want)
		}
	}
}

func TestQEXTHeaderRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		channels int
		hdr      qextHeader
	}{
		{
			name:     "mono_2band_inter",
			channels: 1,
			hdr: qextHeader{
				EndBands: 2,
			},
		},
		{
			name:     "stereo_2band_intensity0",
			channels: 2,
			hdr: qextHeader{
				EndBands:   2,
				Intensity:  0,
				DualStereo: true, // ignored when intensity=0
			},
		},
		{
			name:     "stereo_fullband_dual_intra",
			channels: 2,
			hdr: qextHeader{
				EndBands:   nbQEXTBands,
				Intensity:  nbQEXTBands,
				DualStereo: true,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var enc rangecoding.Encoder
			buf := make([]byte, 16)
			enc.Init(buf)
			encodeQEXTHeader(&enc, tc.channels, tc.hdr)
			payload := append([]byte(nil), enc.Done()...)

			var dec rangecoding.Decoder
			dec.Init(payload)
			got := decodeQEXTHeader(&dec, tc.channels, len(payload))

			if got.EndBands != tc.hdr.EndBands {
				t.Fatalf("EndBands=%d want %d", got.EndBands, tc.hdr.EndBands)
			}
			if tc.channels == 2 && got.Intensity != tc.hdr.Intensity {
				t.Fatalf("Intensity=%d want %d", got.Intensity, tc.hdr.Intensity)
			}
			wantDual := tc.hdr.DualStereo && tc.hdr.Intensity != 0
			if got.DualStereo != wantDual {
				t.Fatalf("DualStereo=%v want %v", got.DualStereo, wantDual)
			}
		})
	}
}

func TestComputeThetaExtUsesQEXTForMonoSplit(t *testing.T) {
	x := []celtNorm{0.99, 0.10, 0.05, 0.02}
	y := []celtNorm{0.10, 0.05, 0.02, 0.01}

	var mainEnc rangecoding.Encoder
	mainBuf := make([]byte, 32)
	mainEnc.Init(mainBuf)
	var extEnc rangecoding.Encoder
	extBuf := make([]byte, 32)
	extEnc.Init(extBuf)

	fillEnc := 1
	bitsEnc := 80
	extBitsEnc := 80
	encCtx := &bandCtx{
		re:            &mainEnc,
		extEnc:        &extEnc,
		encode:        true,
		band:          0,
		intensity:     MaxBands,
		remainingBits: 1 << 20,
		extTotalBits:  len(extBuf) * 8 << bitRes,
	}
	var encSplit splitCtx
	computeThetaExt(encCtx, &encSplit, append([]celtNorm(nil), x...), append([]celtNorm(nil), y...), len(x), &bitsEnc, &extBitsEnc, 1, 1, 0, false, &fillEnc)

	if got := extEnc.TellFrac(); got <= 0 {
		t.Fatal("mono split did not emit any QEXT theta refinement bits")
	}
	if extBitsEnc >= 80 {
		t.Fatalf("mono split ext budget not consumed: got %d want < 80", extBitsEnc)
	}

	mainPayload := append([]byte(nil), mainEnc.Done()...)
	extPayload := append([]byte(nil), extEnc.Done()...)
	if len(extPayload) == 0 {
		t.Fatal("mono split produced empty extension payload")
	}

	var mainDec rangecoding.Decoder
	mainDec.Init(mainPayload)
	var extDec rangecoding.Decoder
	extDec.Init(extPayload)

	fillDec := 1
	bitsDec := 80
	extBitsDec := 80
	decCtx := &bandCtx{
		rd:            &mainDec,
		extDec:        &extDec,
		encode:        false,
		band:          0,
		intensity:     MaxBands,
		remainingBits: 1 << 20,
		extTotalBits:  len(extPayload) * 8 << bitRes,
	}
	var decSplit splitCtx
	computeThetaExt(decCtx, &decSplit, make([]celtNorm, len(x)), make([]celtNorm, len(y)), len(x), &bitsDec, &extBitsDec, 1, 1, 0, false, &fillDec)

	if decSplit.itheta != encSplit.itheta {
		t.Fatalf("itheta=%d want %d", decSplit.itheta, encSplit.itheta)
	}
	if got := extDec.TellFrac(); got <= 0 {
		t.Fatal("mono split decode did not consume any QEXT theta refinement bits")
	}
	if extBitsDec >= 80 {
		t.Fatalf("mono split decode ext budget not consumed: got %d want < 80", extBitsDec)
	}
}

func TestComputeThetaExtWithoutRefinementUsesQuantizedQ30(t *testing.T) {
	x := []celtNorm{0.99, 0.10, 0.05, 0.02}
	y := []celtNorm{0.10, 0.05, 0.02, 0.01}

	var mainEnc rangecoding.Encoder
	mainBuf := make([]byte, 32)
	mainEnc.Init(mainBuf)
	var extEnc rangecoding.Encoder
	extBuf := make([]byte, 32)
	extEnc.Init(extBuf)

	fill := 1
	bits := 80
	ctx := &bandCtx{
		re:            &mainEnc,
		extEnc:        &extEnc,
		encode:        true,
		band:          0,
		intensity:     MaxBands,
		remainingBits: 1 << 20,
		extTotalBits:  len(extBuf) * 8 << bitRes,
	}
	var split splitCtx
	extTellBefore := extEnc.TellFrac()
	computeThetaExt(ctx, &split, append([]celtNorm(nil), x...), append([]celtNorm(nil), y...), len(x), &bits, nil, 1, 1, 0, false, &fill)

	if got, want := split.ithetaQ30, split.itheta<<16; got != want {
		t.Fatalf("ithetaQ30=%d want quantized %d when no QEXT refinement is coded", got, want)
	}
	if got := extEnc.TellFrac(); got != extTellBefore {
		t.Fatalf("extension coder tell=%d want unchanged %d without ext budget", got, extTellBefore)
	}
}

func TestAlgUnquantIntoQEXTN2LargeEnergyUsesWideAccumulator(t *testing.T) {
	const (
		n         = 2
		k         = 128
		extraBits = 12
		gain      = 1.0
	)
	basePulses := []int{-3, -125}
	up := (1 << extraBits) - 1
	refine := -1436

	index := EncodePulses(basePulses, n, k)
	if want := k; abs(basePulses[0])+abs(basePulses[1]) != want {
		t.Fatalf("invalid base pulses: %v", basePulses)
	}

	var mainEnc rangecoding.Encoder
	mainBuf := make([]byte, 32)
	mainEnc.Init(mainBuf)
	mainEnc.EncodeUniform(index, PVQ_V(n, k))
	mainPayload := append([]byte(nil), mainEnc.Done()...)

	var extEnc rangecoding.Encoder
	extBuf := make([]byte, 32)
	extEnc.Init(extBuf)
	extEnc.EncodeUniform(uint32(refine+(up-1)/2), uint32(up))
	extPayload := append([]byte(nil), extEnc.Done()...)

	var mainDec rangecoding.Decoder
	mainDec.Init(mainPayload)
	var extDec rangecoding.Decoder
	extDec.Init(extPayload)

	got := make([]celtNorm, n)
	collapse := algUnquantInto(got, &mainDec, 0, n, k, spreadNone, 1, gain, &extDec, extraBits, nil)
	if collapse != 1 {
		t.Fatalf("collapse=%d want 1", collapse)
	}

	wantPulses := []float64{-10849, -513311}
	energy := float32(wantPulses[0])*float32(wantPulses[0]) + float32(wantPulses[1])*float32(wantPulses[1])
	scale := celtRSqrt(energy) * float32(gain)
	want0 := float64(celtNorm(float32(wantPulses[0]) * scale))
	want1 := float64(celtNorm(float32(wantPulses[1]) * scale))

	if diff := math.Abs(float64(got[0]) - want0); diff > 1e-12 {
		t.Fatalf("got[0]=%.15f want %.15f diff %.3e", got[0], want0, diff)
	}
	if diff := math.Abs(float64(got[1]) - want1); diff > 1e-12 {
		t.Fatalf("got[1]=%.15f want %.15f diff %.3e", got[1], want1, diff)
	}

	wantNorm := want0*want0 + want1*want1
	norm := float64(got[0])*float64(got[0]) + float64(got[1])*float64(got[1])
	if diff := math.Abs(norm - wantNorm); diff > 1e-12 {
		t.Fatalf("norm=%.15f want %.15f diff %.3e", norm, wantNorm, diff)
	}
}

func TestComputeQEXTBandLogEInto(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	coeffs := make([]float64, 960)
	for i := cfg.EBands[0] << 3; i < cfg.EBands[1]<<3; i++ {
		coeffs[i] = 0.5
	}
	for i := cfg.EBands[1] << 3; i < cfg.EBands[2]<<3; i++ {
		coeffs[i] = 0.25
	}

	bandE := make([]celtEner, cfg.EffBands)
	bandLogE := make([]celtGLog, cfg.EffBands)
	computeQEXTBandLogEInto(coeffs, &cfg, cfg.EffBands, 3, bandE, bandLogE)

	if bandE[0] <= bandE[1] {
		t.Fatalf("bandE=%v want band 0 > band 1", bandE)
	}
	if bandLogE[0] <= bandLogE[1] {
		t.Fatalf("bandLogE=%v want band 0 > band 1", bandLogE)
	}
}

func TestNormalizeQEXTBandsInto(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	coeffs := make([]float64, 960)
	for i := cfg.EBands[0] << 3; i < cfg.EBands[1]<<3; i++ {
		coeffs[i] = 0.5
	}
	for i := cfg.EBands[1] << 3; i < cfg.EBands[2]<<3; i++ {
		coeffs[i] = 0.25
	}

	bandE := make([]celtEner, cfg.EffBands)
	computeQEXTBandAmplitudesInto(coeffs, &cfg, cfg.EffBands, 3, bandE)

	norm := make([]celtNorm, len(coeffs))
	normalizeQEXTBandsInto(coeffs, &cfg, cfg.EffBands, 3, bandE, norm)

	firstIdx := cfg.EBands[0] << 3
	secondIdx := cfg.EBands[1] << 3
	if norm[firstIdx] <= 0 || norm[secondIdx] <= 0 {
		t.Fatalf("normalized coeffs not populated: first=%.6f second=%.6f", norm[firstIdx], norm[secondIdx])
	}
	if norm[0] != 0 {
		t.Fatalf("unexpected lowband write before qext range: norm[0]=%.6f", norm[0])
	}
}

func TestEnsureScratchSkipsQEXTBuffersWhenDisabled(t *testing.T) {
	enc := NewEncoder(2)
	enc.EnsureScratch(960)

	if enc.scratch.qext != nil {
		t.Fatalf("qext scratch allocated while disabled: %+v", enc.scratch.qext)
	}

	enc.SetQEXTEnabled(true)
	enc.EnsureScratch(960)

	if enc.scratch.qext == nil {
		t.Fatal("qext scratch not allocated when enabled")
	}
	if got := len(enc.scratch.qext.buf); got == 0 {
		t.Fatal("qextBuf not allocated when enabled")
	}
	if got := len(enc.scratch.qext.normL); got == 0 {
		t.Fatal("qextNormL not allocated when enabled")
	}
	if got := len(enc.scratch.qext.extraBits); got == 0 {
		t.Fatal("qextExtraBits not allocated when enabled")
	}
}

func TestCELTDecoderQEXTStateStaysDormantUntilPayload(t *testing.T) {
	dec := NewDecoder(1)
	if dec.qext != nil && len(dec.qext.oldBandE) != 0 {
		t.Fatalf("NewDecoder allocated qextOldBandE=%d want dormant", len(dec.qext.oldBandE))
	}

	var mainRD rangecoding.Decoder
	mainRD.Init([]byte{0xff})
	if qext := dec.prepareQEXTDecode(nil, &mainRD, MaxBands, 0, 120); qext != nil {
		t.Fatal("prepareQEXTDecode(nil) returned QEXT state")
	}
	if dec.qext != nil && len(dec.qext.oldBandE) != 0 {
		t.Fatalf("empty QEXT payload allocated qextOldBandE=%d want dormant", len(dec.qext.oldBandE))
	}
}

func TestComputeQEXTExtraAllocationEncodeZeroBudget(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	extraPulses := make([]int32, MaxBands+nbQEXTBands)
	extraQuant := make([]int32, MaxBands+nbQEXTBands)
	computeQEXTExtraAllocationEncode(0, MaxBands, 2, 0, 2, 0, make([]celtGLog, MaxBands*2), make([]celtGLog, nbQEXTBands*2), &cfg, 0, 0, nil, extraPulses, extraQuant)

	for i := range extraPulses {
		if extraPulses[i] != 0 || extraQuant[i] != 0 {
			t.Fatalf("zero-budget extra allocation[%d]=(%d,%d) want (0,0)", i, extraPulses[i], extraQuant[i])
		}
	}
}

func TestComputeQEXTExtraAllocationEncodeFixture(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	mainLogE := make([]celtGLog, MaxBands*2)
	for i := 0; i < MaxBands; i++ {
		mainLogE[i] = celtGLog(0.25 + 0.05*float32(i))
		mainLogE[MaxBands+i] = celtGLog(0.15 + 0.04*float32(i))
	}
	qextLogE := make([]celtGLog, nbQEXTBands*2)
	for i := 0; i < nbQEXTBands; i++ {
		qextLogE[i] = celtGLog(-0.20 + 0.08*float32(i))
		qextLogE[nbQEXTBands+i] = celtGLog(-0.10 + 0.05*float32(i))
	}

	var enc rangecoding.Encoder
	buf := make([]byte, 64)
	enc.Init(buf)

	extraPulses := make([]int32, MaxBands+nbQEXTBands)
	extraQuant := make([]int32, MaxBands+nbQEXTBands)
	computeQEXTExtraAllocationEncode(0, MaxBands, 2, 96<<bitRes, 2, 0, mainLogE, qextLogE, &cfg, 0.9, 0.42, &enc, extraPulses, extraQuant)

	wantQuant := []int32{2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 2}
	wantPulses := []int32{0, 0, 0, 0, 0, 0, 0, 0, 8, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 288, 252}
	wantExt := []byte{0xab, 0xae, 0x86, 0x59, 0x2f, 0x77, 0xd7, 0x05, 0xd3, 0xf7}

	if got := extraQuant[:MaxBands+2]; !intSliceEqual(got, wantQuant) {
		t.Fatalf("extraQuant=%v want %v", got, wantQuant)
	}
	if got := extraPulses[:MaxBands+2]; !intSliceEqual(got, wantPulses) {
		t.Fatalf("extraPulses=%v want %v", got, wantPulses)
	}
	if got := enc.Done(); !bytes.Equal(got, wantExt) {
		t.Fatalf("ext bytes=%x want %x", got, wantExt)
	}

	nonZero := 0
	for i := 0; i < MaxBands+2; i++ {
		if extraQuant[i] > 0 || extraPulses[i] > 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("fixture extra allocation produced all zeros")
	}
}

func TestComputeQEXTExtraAllocationDecodeRoundTrip(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			mainLogE := make([]celtGLog, MaxBands*channels)
			for i := 0; i < MaxBands; i++ {
				mainLogE[i] = celtGLog(0.25 + 0.05*float32(i))
				if channels == 2 {
					mainLogE[MaxBands+i] = celtGLog(0.15 + 0.04*float32(i))
				}
			}

			var enc rangecoding.Encoder
			buf := make([]byte, 64)
			enc.Init(buf)

			extraPulsesEnc := make([]int32, MaxBands+nbQEXTBands)
			extraQuantEnc := make([]int32, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationEncode(0, MaxBands, 0, 96<<bitRes, channels, 0, mainLogE, nil, nil, 0, 0, &enc, extraPulsesEnc, extraQuantEnc)

			enc.Done()
			payload := append([]byte(nil), enc.Buffer()[:enc.Storage()]...)
			if len(payload) == 0 {
				t.Fatal("encode-side QEXT extra allocation emitted empty payload")
			}

			var dec rangecoding.Decoder
			dec.Init(payload)

			extraPulsesDec := make([]int32, MaxBands)
			extraQuantDec := make([]int32, MaxBands)
			computeQEXTExtraAllocationDecodeWithMode(0, MaxBands, 0, len(payload)*8<<bitRes, channels, 0, &dec, extraPulsesDec, extraQuantDec, nil)

			if got, want := extraQuantDec, extraQuantEnc[:MaxBands]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraQuant=%v want %v", got, want)
			}
			if got, want := extraPulsesDec, extraPulsesEnc[:MaxBands]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraPulses=%v want %v", got, want)
			}
		})
	}
}

func TestComputeQEXTExtraAllocationDecodeWithModeRoundTrip(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			mainLogE := make([]celtGLog, MaxBands*channels)
			for i := 0; i < MaxBands; i++ {
				mainLogE[i] = celtGLog(0.25 + 0.05*float32(i))
				if channels == 2 {
					mainLogE[MaxBands+i] = celtGLog(0.15 + 0.04*float32(i))
				}
			}
			qextLogE := make([]celtGLog, nbQEXTBands*channels)
			for i := 0; i < nbQEXTBands; i++ {
				qextLogE[i] = celtGLog(-0.20 + 0.08*float32(i))
				if channels == 2 {
					qextLogE[nbQEXTBands+i] = celtGLog(-0.10 + 0.05*float32(i))
				}
			}

			var enc rangecoding.Encoder
			buf := make([]byte, 128)
			enc.Init(buf)

			extraPulsesEnc := make([]int32, MaxBands+nbQEXTBands)
			extraQuantEnc := make([]int32, MaxBands+nbQEXTBands)
			qextEnd := 2
			computeQEXTExtraAllocationEncode(0, MaxBands, qextEnd, 96<<bitRes, channels, 0, mainLogE, qextLogE, &cfg, 0.9, 0.42, &enc, extraPulsesEnc, extraQuantEnc)

			enc.Done()
			payload := append([]byte(nil), enc.Buffer()[:enc.Storage()]...)
			if len(payload) == 0 {
				t.Fatal("encode-side QEXT extra allocation with mode emitted empty payload")
			}

			var dec rangecoding.Decoder
			dec.Init(payload)

			extraPulsesDec := make([]int32, MaxBands+nbQEXTBands)
			extraQuantDec := make([]int32, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationDecodeWithMode(0, MaxBands, qextEnd, len(payload)*8<<bitRes, channels, 0, &dec, extraPulsesDec, extraQuantDec, &cfg)

			if got, want := extraQuantDec[:MaxBands+qextEnd], extraQuantEnc[:MaxBands+qextEnd]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraQuant=%v want %v", got, want)
			}
			if got, want := extraPulsesDec[:MaxBands+qextEnd], extraPulsesEnc[:MaxBands+qextEnd]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraPulses=%v want %v", got, want)
			}
		})
	}
}

func TestEncodeFrameRetainsQEXTPayload(t *testing.T) {
	pcm := generateSineWave(440.0, 960)

	encA := NewEncoder(1)
	encA.SetQEXTEnabled(true)
	encA.SetBitrate(256000)

	packetA, err := encA.EncodeFrame(float32Slice(pcm), 960)
	if err != nil {
		t.Fatalf("EncodeFrame(qext A) failed: %v", err)
	}
	if len(packetA) == 0 {
		t.Fatal("EncodeFrame(qext A) returned empty packet")
	}
	payloadA := encA.LastQEXTPayload()
	if len(payloadA) == 0 {
		t.Fatal("EncodeFrame(qext A) produced empty retained payload")
	}

	encB := NewEncoder(1)
	encB.SetQEXTEnabled(true)
	encB.SetBitrate(256000)

	packetB, err := encB.EncodeFrame(float32Slice(pcm), 960)
	if err != nil {
		t.Fatalf("EncodeFrame(qext B) failed: %v", err)
	}
	if !bytes.Equal(packetA, packetB) {
		t.Fatalf("main packet mismatch:\nA=%x\nB=%x", packetA, packetB)
	}
	payloadB := encB.LastQEXTPayload()
	if !bytes.Equal(payloadA, payloadB) {
		t.Fatalf("retained qext payload mismatch:\nA=%x\nB=%x", payloadA, payloadB)
	}
}

func TestEncodeFrameRetainedQEXTPayloadCarriesHeader(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm := make([]float64, 960*channels)
			for i := 0; i < 960; i++ {
				pcm[i*channels] = 0.45
				if channels == 2 {
					pcm[i*channels+1] = 0.30
				}
			}

			enc := NewEncoder(channels)
			enc.SetQEXTEnabled(true)
			enc.SetBitrate(256000)

			if _, err := enc.EncodeFrame(float32Slice(pcm), 960); err != nil {
				t.Fatalf("EncodeFrame(qext) failed: %v", err)
			}
			payload := enc.LastQEXTPayload()
			if len(payload) == 0 {
				t.Fatal("EncodeFrame(qext) produced empty retained payload")
			}

			var dec rangecoding.Decoder
			dec.Init(payload)
			hdr := decodeQEXTHeader(&dec, channels, len(payload))
			if hdr.EndBands != 2 {
				t.Fatalf("EndBands=%d want 2", hdr.EndBands)
			}
			if channels == 2 && hdr.Intensity != hdr.EndBands {
				t.Fatalf("Intensity=%d want %d", hdr.Intensity, hdr.EndBands)
			}
		})
	}
}

func TestEncodeFrameClearsQEXTPayloadWhenDisabled(t *testing.T) {
	pcm := generateSineWave(440.0, 960)

	enc := NewEncoder(1)
	enc.SetQEXTEnabled(true)
	enc.SetBitrate(256000)

	if _, err := enc.EncodeFrame(float32Slice(pcm), 960); err != nil {
		t.Fatalf("EncodeFrame(qext on) failed: %v", err)
	}
	if len(enc.LastQEXTPayload()) == 0 {
		t.Fatal("EncodeFrame(qext on) produced empty retained payload")
	}

	enc.SetQEXTEnabled(false)
	if _, err := enc.EncodeFrame(float32Slice(pcm), 960); err != nil {
		t.Fatalf("EncodeFrame(qext off) failed: %v", err)
	}
	if payload := enc.LastQEXTPayload(); len(payload) != 0 {
		t.Fatalf("EncodeFrame(qext off) retained stale payload: %x", payload)
	}
}

func TestHandleChannelTransitionLeavesQEXTOldBandEMonoToStereoUnchanged(t *testing.T) {
	dec := NewDecoder(2)

	dec.ensureQEXTState()
	dec.prevStreamChannels = 1
	dec.qext.oldBandE = make([]celtGLog, MaxBands)
	for i := 0; i < MaxBands; i++ {
		dec.qext.oldBandE[i] = celtGLog(float32(i+1) * 0.125)
	}

	if got := dec.handleChannelTransition(2); !got {
		t.Fatal("expected mono-to-stereo transition")
	}

	if len(dec.qext.oldBandE) != MaxBands {
		t.Fatalf("oldBandE len=%d want %d", len(dec.qext.oldBandE), MaxBands)
	}
	for i := 0; i < MaxBands; i++ {
		want := celtGLog(float32(i+1) * 0.125)
		if got := dec.qext.oldBandE[i]; got != want {
			t.Fatalf("oldBandE[%d]=%v want %v", i, got, want)
		}
	}
}

func TestHandleChannelTransitionStereoToMonoLeavesQEXTOldBandEUnchanged(t *testing.T) {
	dec := NewDecoder(2)

	dec.ensureQEXTState()
	dec.prevStreamChannels = 2
	dec.qext.oldBandE = make([]celtGLog, MaxBands*2)
	for i := 0; i < MaxBands; i++ {
		dec.qext.oldBandE[i] = celtGLog(i)
		dec.qext.oldBandE[MaxBands+i] = celtGLog(MaxBands - i)
	}

	if got := dec.handleChannelTransition(1); !got {
		t.Fatal("expected stereo-to-mono transition")
	}

	if len(dec.qext.oldBandE) != MaxBands*2 {
		t.Fatalf("oldBandE len=%d want %d", len(dec.qext.oldBandE), MaxBands*2)
	}
	for i := 0; i < MaxBands; i++ {
		left := celtGLog(i)
		right := celtGLog(MaxBands - i)
		if got := dec.qext.oldBandE[i]; got != left {
			t.Fatalf("oldBandE[%d]=%v want %v", i, got, left)
		}
		if got := dec.qext.oldBandE[MaxBands+i]; got != right {
			t.Fatalf("oldBandE[%d]=%v want %v", MaxBands+i, got, right)
		}
	}
}

func intSliceEqual(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var libopusQEXTThetaGainHelper libopustest.HelperCache

func buildLibopusQEXTThetaGainHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext theta gains",
		OutputBase:  "gopus_libopus_celt_qext_theta_gain",
		SourceFile:  "libopus_celt_qext_theta_gain_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTThetaGainsMatchLibopusWithoutExtensionCoder(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTThetaGainHelper.Path(buildLibopusQEXTThetaGainHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext theta gains", err)
		return
	}
	if !celtQEXTFloatMath {
		t.Fatal("QEXT build must select Q30 stereo gains")
	}
	// The middle angles are the two theta RDO trials from a stereo 5 ms
	// packet's N=2 band. The context has no secondary coder at that band.
	thetaQ30 := [...]int32{0, 134217728, 317849600, 324927488, 536870912, 805306368, 1 << 30}
	payload := libopustest.NewOraclePayload("GQTG", uint32(len(thetaQ30)))
	for _, theta := range thetaQ30 {
		payload.U32(uint32(theta))
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext theta gains", "GQTO")
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.Count(len(thetaQ30)); got != len(thetaQ30) {
		t.Fatalf("theta gain count %d, want %d", got, len(thetaQ30))
	}
	for _, theta := range thetaQ30 {
		wantMid, wantSide := reader.Float32(), reader.Float32()
		gotMid, gotSide := thetaSplitGains(&splitCtx{ithetaQ30: int(theta)}, celtQEXTFloatMath)
		if math.Float32bits(gotMid) != math.Float32bits(wantMid) || math.Float32bits(gotSide) != math.Float32bits(wantSide) {
			t.Errorf("theta_q30=%d: Go mid/side %08x/%08x, C %08x/%08x", theta,
				math.Float32bits(gotMid), math.Float32bits(gotSide), math.Float32bits(wantMid), math.Float32bits(wantSide))
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
