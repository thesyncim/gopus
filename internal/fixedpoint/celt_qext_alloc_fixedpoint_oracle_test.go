//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestCELTFixedQEXTExtraAllocationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		sampleRate  int
		channels    int
		lm          int
		totalQ3     int32
		storage     int
		realistic   bool
		toneishness int32
	}{
		// These retain the original 96 kHz full-budget channel/LM matrix.
		{sampleRate: 96000, channels: 1, lm: 0, totalQ3: 50000, storage: 256},
		{sampleRate: 96000, channels: 1, lm: 3, totalQ3: 50000, storage: 256},
		{sampleRate: 96000, channels: 2, lm: 0, totalQ3: 50000, storage: 256},
		{sampleRate: 96000, channels: 2, lm: 3, totalQ3: 50000, storage: 256},
		// Native Q24/Q29 cases retain low bits that float32 cannot carry.
		{sampleRate: 48000, channels: 1, lm: 0, totalQ3: 50000, storage: 256, realistic: true},
		{sampleRate: 48000, channels: 2, lm: 3, totalQ3: 50000, storage: 256, realistic: true},
		{sampleRate: 48000, channels: 1, lm: 0, totalQ3: 1100, storage: 32, realistic: true},
		{sampleRate: 96000, channels: 2, lm: 0, totalQ3: 3400, storage: 48, realistic: true},
		// Hold tone frequency below the C threshold so the QCONST32(.98f,29)
		// comparison decides the minimum-depth branch.
		{sampleRate: 48000, channels: 1, lm: 0, totalQ3: 500, storage: 32, realistic: true, toneishness: celtToneishnessQ29 - 1},
		{sampleRate: 48000, channels: 1, lm: 0, totalQ3: 500, storage: 32, realistic: true, toneishness: celtToneishnessQ29},
		{sampleRate: 48000, channels: 1, lm: 0, totalQ3: 500, storage: 32, realistic: true, toneishness: celtToneishnessQ29 + 1},
	}
	for _, tc := range cases {
		qextEnd := 14
		if tc.sampleRate == 48000 {
			qextEnd = 2
		}
		name := fmt.Sprintf("rate_%d/channels_%d/lm_%d/budget_%d", tc.sampleRate, tc.channels, tc.lm, tc.totalQ3)
		if tc.toneishness != 0 {
			name += fmt.Sprintf("/tone_%d", tc.toneishness)
		}
		t.Run(name, func(t *testing.T) {
			mainLogE := make([]int32, tc.channels*celtNbEBands)
			qextLogE := make([]int32, tc.channels*14)
			for i := range mainLogE {
				step := int32(32768)
				if tc.realistic {
					step = 32769 + int32(i%7)*37
				}
				mainLogE[i] = -20*(1<<dbShift) + int32(i)*step
			}
			for i := range qextLogE {
				step := int32(32768)
				if tc.realistic {
					step = 16387 + int32(i%5)*29
				}
				qextLogE[i] = -18*(1<<dbShift) + int32(i)*step
			}
			toneFreq := int16(16384)
			toneQ29 := int32(1 << 27)
			if tc.realistic {
				toneFreq = 21792
				toneQ29 = 526133493
				if int32(float32(toneQ29)) == toneQ29 {
					t.Fatal("Q29 fixture is exactly representable as float32")
				}
			}
			if tc.toneishness != 0 {
				toneFreq = 1
				toneQ29 = tc.toneishness
				for i := range qextLogE {
					qextLogE[i] = -80 * (1 << dbShift)
				}
			}
			const end = celtNbEBands
			params := libopustest.CELTFixedQEXTExtraAllocParams{
				SampleRate: tc.sampleRate, FrameSize: tc.sampleRate / 50,
				Channels: tc.channels, LM: tc.lm, Start: 0, End: end, QEXTEnd: qextEnd,
				TotalQ3: tc.totalQ3, ToneFreqQ14: toneFreq, ToneishnessQ29: toneQ29,
				StorageBytes: tc.storage, MainBandLogE: mainLogE, QEXTBandLogE: qextLogE,
			}
			want, err := libopustest.ProbeCELTFixedQEXTExtraAllocation(params)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT extra allocation", err)
				return
			}

			mode, qextEdges, qextLogN, modeQEXTEnd, ok := fixedQEXTBandMode(tc.sampleRate, tc.sampleRate/400)
			if !ok || modeQEXTEnd != qextEnd {
				t.Fatalf("missing %d Hz QEXT mode geometry", tc.sampleRate)
			}
			var gotPulses, gotQuant [celtNbEBands + 14]int32
			buf := make([]byte, tc.storage)
			var enc rangecoding.Encoder
			enc.Init(buf)
			computeQEXTExtraAllocationFixed(0, end, qextEnd, tc.totalQ3, tc.channels, tc.lm,
				mainLogE, qextLogE, staticMDCT48000LogN[:], qextLogN, qextEdges,
				&mode, toneFreq, toneQ29, &enc, gotPulses[:], gotQuant[:])
			gotTell := enc.TellFrac()
			gotRange := enc.Range()
			enc.Shrink(uint32(tc.storage))
			gotBytes := enc.Done()
			if len(gotPulses) < want.TotalBands ||
				!slices.Equal(gotPulses[:want.TotalBands], want.ExtraPulses) ||
				!slices.Equal(gotQuant[:want.TotalBands], want.ExtraQuant) ||
				gotTell != int(want.TellFrac) || gotRange != want.Range || !bytes.Equal(gotBytes, want.Bytes) {
				t.Fatalf("fixed-QEXT allocation differs: tell=%d/%d range=%08x/%08x bytes=% x/% x pulses=%v/%v quant=%v/%v",
					gotTell, want.TellFrac, gotRange, want.Range, gotBytes, want.Bytes,
					gotPulses[:want.TotalBands], want.ExtraPulses, gotQuant[:want.TotalBands], want.ExtraQuant)
			}
		})
	}
}
