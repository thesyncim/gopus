package celt

import (
	"encoding/hex"
	"math"
	"runtime"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const selectedAutocorrProjectionStream5Packet = "f47e514de2f9194f4026d0a4b5842deb193aac7ed9983382a85471106bf1dded268f39daeec89ea9"

var selectedPLCReadAutocorrHelper libopustest.HelperCache

func selectedPLCReadAutocorrConfig() libopustest.CHelperConfig {
	cfg := libopustest.CHelperConfig{
		Label:       "selected CELT raw autocorrelation",
		OutputBase:  "gopus_selected_celt_raw_autocorr",
		SourceFile:  "libopus_celt_plc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return cfg
}

func buildSelectedPLCReadAutocorrHelper() (string, error) {
	return libopustest.BuildCHelper(selectedPLCReadAutocorrConfig())
}

func probeSelectedPLCRawAutocorr(t *testing.T, x []celtSig, lag int) []float32 {
	t.Helper()
	binPath, err := selectedPLCReadAutocorrHelper.Path(buildSelectedPLCReadAutocorrHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected CELT raw autocorrelation", err)
	}
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeRawAutocorr)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(lag))
	payload.U32(0)
	for _, sample := range x {
		payload.Float32(float32(sample))
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "selected CELT raw autocorrelation", "GCPO")
	if err != nil {
		libopustest.HelperUnavailable(t, "selected CELT raw autocorrelation", err)
	}
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeRawAutocorr {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeRawAutocorr)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func selectedPLCRawAutocorrWindowedInput(frame []celtSig, window []float32, overlap int) []celtSig {
	x := append([]celtSig(nil), frame...)
	for i := 0; i < overlap && i < len(window) && i < len(x)/2; i++ {
		w := float32(window[i])
		x[i] = celtSig(float32(x[i]) * w)
		x[len(x)-1-i] = celtSig(float32(x[len(x)-1-i]) * w)
	}
	return x
}

func assertSelectedPLCACEqual(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x %.10g, selected C=%08x %.10g", label, i,
				math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}

func makeSelectedPLCTailSignal(seed uint32) []celtSig {
	x := make([]celtSig, celtPLCLPCOrder+1)
	state := seed
	for i := 1; i < len(x); i++ {
		state = 1664525*state + 1013904223
		exponent := uint32(120) + ((state >> 25) & 15)
		bits := exponent<<23 | (state & 0x007fffff)
		if state&0x100 != 0 {
			bits |= 0x80000000
		}
		x[i] = celtSig(math.Float32frombits(bits))
	}
	return x
}

func TestSelectedSIMDPLCRawAutocorrMatchesArchive(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("selected ARM64 archive witness")
	}
	cfg := selectedPLCReadAutocorrConfig()
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if cfg.DREDQEXTRef {
		variant, err = libopustooling.ResolveLibopusDREDQEXTReferenceVariant()
	} else if cfg.QEXTRef {
		variant, err = libopustooling.ResolveLibopusQEXTReferenceVariant()
	}
	if err != nil {
		t.Fatalf("resolve paired libopus archive: %v", err)
	}
	if variant != libopustooling.LibopusReferenceSIMD &&
		variant != libopustooling.LibopusReferenceQEXTSIMD &&
		variant != libopustooling.LibopusReferenceDREDQEXTSIMD {
		t.Skipf("selected SIMD archive witness requires the SIMD Go lane, got %s", variant)
	}
	libopustest.RequireOracle(t)
	t.Logf("calling _celt_autocorr from selected %s libopus archive", variant)

	t.Run("projection_stream5_public_history", func(t *testing.T) {
		packet, err := hex.DecodeString(selectedAutocorrProjectionStream5Packet)
		if err != nil {
			t.Fatal(err)
		}
		dec := NewDecoder(2)
		if _, err := dec.DecodeFrame(packet[1:], 480); err != nil {
			t.Fatalf("decode projection stream 5 CELT seed: %v", err)
		}
		decodeBufferSize := dec.plcDecodeBufferLen()
		maxPeriod := dec.plcCombFilterMaxPeriod()
		window := dec.scratchIMDCTF32.modeWindow(Overlap)
		for ch := range int(dec.channels) {
			hist := dec.decodeMemChannel(ch)[:decodeBufferSize]
			exc := hist[decodeBufferSize-maxPeriod-celtPLCLPCOrder:]
			frame := exc[celtPLCLPCOrder:]
			windowed := selectedPLCRawAutocorrWindowedInput(frame, window, dec.synthOverlapLen())

			var got [celtPLCLPCOrder + 1]float32
			dec.computePLCRawAutocorr(windowed, nil, got[:])
			want := probeSelectedPLCRawAutocorr(t, windowed, celtPLCLPCOrder)
			assertSelectedPLCACEqual(t, "raw autocorrelation", got[:], want)
			dec.computePLCRawAutocorr(windowed, nil, got[:])
			if allocs := testing.AllocsPerRun(100, func() {
				dec.computePLCRawAutocorr(windowed, nil, got[:])
			}); allocs != 0 {
				t.Fatalf("warmed raw autocorrelation allocs/run=%g, want 0", allocs)
			}
		}
	})

	t.Run("remainder_patterns", func(t *testing.T) {
		dec := NewDecoder(1)
		for _, tc := range []struct {
			name  string
			seed  uint32
			scale float32
		}{
			{name: "midrange_a", seed: 0x4a91, scale: 1800},
			{name: "midrange_b", seed: 0xa535, scale: 2600},
			{name: "highrange", seed: 0x7777, scale: 7200},
			{name: "lowrange", seed: 0x19d3, scale: 140},
		} {
			t.Run(tc.name, func(t *testing.T) {
				x := makeCELTPLCTestSignal(1024, tc.seed, tc.scale)
				var got [celtPLCLPCOrder + 1]float32
				dec.computePLCRawAutocorr(x, nil, got[:])
				want := probeSelectedPLCRawAutocorr(t, x, celtPLCLPCOrder)
				assertSelectedPLCACEqual(t, "raw autocorrelation", got[:], want)
			})
		}
	})

	t.Run("isolated_tail_lengths", func(t *testing.T) {
		// With n=order+1 and x[0]=0, the vector prefix contributes zero. Each
		// output lag then exercises only the C tail of length order-lag.
		dec := NewDecoder(1)
		for _, seed := range []uint32{0x143e, 0x9c72, 0x15a91, 0xb734e} {
			t.Run(strconv.FormatUint(uint64(seed), 16), func(t *testing.T) {
				x := makeSelectedPLCTailSignal(seed)
				var got [celtPLCLPCOrder + 1]float32
				dec.computePLCRawAutocorr(x, nil, got[:])
				want := probeSelectedPLCRawAutocorr(t, x, celtPLCLPCOrder)
				assertSelectedPLCACEqual(t, "isolated raw autocorrelation tails", got[:], want)
			})
		}
	})
}
