package celt

import (
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var fixedZeroAllocHelper libopustest.HelperCache

func buildFixedZeroAllocHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "selected fixed CELT nonpositive allocation",
		OutputBase:  "gopus_libopus_celt_alloc_zero_fixed",
		SourceFile:  "libopus_celt_alloc_zero_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestAllocationNonpositiveBudgetMatchesFixedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name                            string
		start, end, lm, channels, total int
		prev, signalBandwidth, trim     int
	}{
		{"mono_2p5_zero_first", 0, 21, 0, 1, 0, 0, 21, 5},
		{"mono_2p5_negative_prev", 0, 21, 0, 1, -8, 21, 21, 5},
		{"stereo_2p5_zero", 0, 21, 0, 2, 0, 0, 21, 5},
		{"stereo_10_negative", 0, 21, 2, 2, -64, 1, 18, 7},
		{"hybrid_20_zero", 17, 21, 3, 1, 0, 2, 21, 5},
		{"hybrid_20_negative_stereo", 17, 21, 3, 2, -1, 21, 20, 3},
	}
	bin, err := fixedZeroAllocHelper.Path(buildFixedZeroAllocHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed CELT allocation", err)
	}
	payload := libopustest.NewOraclePayload("GAZI", uint32(len(cases)))
	for _, c := range cases {
		for _, v := range [...]int{c.start, c.end, c.lm, c.channels, c.total,
			c.prev, c.signalBandwidth, c.trim} {
			payload.I32(int32(v))
		}
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "selected fixed CELT allocation", "GAZO")
	if err != nil {
		t.Fatalf("run selected fixed allocation oracle: %v", err)
	}
	reader.Count(len(cases))
	for _, c := range cases {
		wantCoded, wantBalance := int(reader.I32()), int(reader.I32())
		wantIntensity, wantDual := int(reader.I32()), reader.I32() != 0
		wantTell, wantRange := int(reader.U32()), reader.U32()
		var wantCaps, wantPulses, wantFine, wantPriority [21]int32
		for i := range wantCaps {
			wantCaps[i] = reader.I32()
		}
		for i := range wantPulses {
			wantPulses[i] = reader.I32()
		}
		for i := range wantFine {
			wantFine[i] = reader.I32()
		}
		for i := range wantPriority {
			wantPriority[i] = reader.I32()
		}
		t.Run(c.name, func(t *testing.T) {
			caps := InitCaps(c.end, c.lm, c.channels)
			offsets := make([]int32, c.end)
			for _, into := range []bool{false, true} {
				var coder rangecoding.Encoder
				var packet [64]byte
				coder.Init(packet[:])
				var got *AllocationResult
				if into {
					var scratch AllocEncodeScratch
					got = ComputeAllocationWithEncoderStartInto(&scratch, &coder, c.start, c.total,
						c.end, c.channels, caps, offsets, c.trim, c.end, false, c.lm,
						c.prev, c.signalBandwidth)
				} else {
					result := ComputeAllocationWithEncoderStart(&coder, c.start, c.total,
						c.end, c.channels, caps, offsets, c.trim, c.end, false, c.lm,
						c.prev, c.signalBandwidth)
					got = &result
				}
				if got.CodedBands != wantCoded || got.Balance != wantBalance ||
					got.Intensity != wantIntensity || got.DualStereo != wantDual ||
					coder.TellFrac() != wantTell || coder.Range() != wantRange ||
					!reflect.DeepEqual(got.Caps, wantCaps[:c.end]) ||
					!reflect.DeepEqual(got.BandBits, wantPulses[:c.end]) ||
					!reflect.DeepEqual(got.FineBits, wantFine[:c.end]) ||
					!reflect.DeepEqual(got.FinePriority, wantPriority[:c.end]) {
					t.Errorf("into=%t coded=%d/%d balance=%d/%d intensity=%d/%d dual=%t/%t tell=%d/%d range=%08x/%08x caps=%v/%v pulses=%v/%v fine=%v/%v priority=%v/%v",
						into, got.CodedBands, wantCoded, got.Balance, wantBalance,
						got.Intensity, wantIntensity, got.DualStereo, wantDual,
						coder.TellFrac(), wantTell, coder.Range(), wantRange,
						got.Caps, wantCaps[:c.end], got.BandBits, wantPulses[:c.end],
						got.FineBits, wantFine[:c.end], got.FinePriority, wantPriority[:c.end])
				}
			}
			var scratch AllocEncodeScratch
			var coder rangecoding.Encoder
			var packet [64]byte
			encode := func() {
				coder.Init(packet[:])
				ComputeAllocationWithEncoderStartInto(&scratch, &coder, c.start, c.total,
					c.end, c.channels, caps, offsets, c.trim, c.end, false, c.lm,
					c.prev, c.signalBandwidth)
			}
			encode()
			if allocs := testing.AllocsPerRun(100, encode); allocs != 0 {
				t.Fatalf("warm Into allocation=%g want 0", allocs)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
