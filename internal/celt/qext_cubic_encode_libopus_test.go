//go:build gopus_qext

package celt

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

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
