package celt

import (
	"fmt"
	"math"
	"testing"
)

func updateStereoHistoryLegacy(mem, samples []float64, frameSize, history int) {
	histL := mem[:history]
	histR := mem[history : 2*history]
	if frameSize >= history {
		src := (frameSize - history) * 2
		for i := 0; i < history; i++ {
			histL[i] = samples[src]
			histR[i] = samples[src+1]
			src += 2
		}
		return
	}
	copy(histL, histL[frameSize:])
	copy(histR, histR[frameSize:])
	src := 0
	dst := history - frameSize
	for i := 0; i < frameSize; i++ {
		histL[dst+i] = samples[src]
		histR[dst+i] = samples[src+1]
		src += 2
	}
}

func requireFloat64BitsEqual(t *testing.T, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("mismatch at %d: got=0x%x want=0x%x", i, math.Float64bits(got[i]), math.Float64bits(want[i]))
		}
	}
}

func TestStereoHistoryHelpersMatchLegacy(t *testing.T) {
	histories := []int{combFilterHistory, plcDecodeBufferSize}
	frameSizes := []int{120, 240, 960, plcDecodeBufferSize + 64}

	for _, history := range histories {
		for _, frameSize := range frameSizes {
			name := "plc"
			if history == combFilterHistory {
				name = "postfilter"
			}
			t.Run(fmt.Sprintf("%s/%d", name, frameSize), func(t *testing.T) {
				samples := make([]float64, frameSize*2)
				for i := range samples {
					switch i % 5 {
					case 0:
						samples[i] = math.Float64frombits(0x7ff8000000000000 + uint64(i))
					case 1:
						samples[i] = math.Float64frombits(0x8000000000000000 | uint64(i))
					default:
						samples[i] = float64(i)*0.125 - 9
					}
				}

				current := NewDecoder(2)
				legacy := NewDecoder(2)
				current.postfilterMem = make([]float64, combFilterHistory*2)
				legacy.postfilterMem = append([]float64(nil), current.postfilterMem...)
				current.plcDecodeMem = make([]float64, plcDecodeBufferSize*2)
				legacy.plcDecodeMem = append([]float64(nil), current.plcDecodeMem...)
				for i := range current.postfilterMem {
					current.postfilterMem[i] = float64(i%17) * -0.25
				}
				copy(legacy.postfilterMem, current.postfilterMem)
				for i := range current.plcDecodeMem {
					current.plcDecodeMem[i] = float64(i%19) * 0.375
				}
				copy(legacy.plcDecodeMem, current.plcDecodeMem)

				if history == combFilterHistory {
					current.updatePostfilterHistory(samples, frameSize, history)
					updateStereoHistoryLegacy(legacy.postfilterMem, samples, frameSize, history)
					requireFloat64BitsEqual(t, current.postfilterMem, legacy.postfilterMem)
				} else {
					current.updatePLCDecodeHistory(samples, frameSize, history)
					updateStereoHistoryLegacy(legacy.plcDecodeMem, samples, frameSize, history)
					current.materializePLCDecodeHistory()
					requireFloat64BitsEqual(t, current.plcDecodeMem, legacy.plcDecodeMem)
				}
			})
		}
	}
}

func TestApplyPostfilterStereoPlanarMatchesInterleaved(t *testing.T) {
	const frameSize = 960
	const lm = 1

	left := make([]float64, frameSize)
	right := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		left[i] = math.Sin(2*math.Pi*440*float64(i)/48000) * 0.5
		right[i] = math.Sin(2*math.Pi*660*float64(i)/48000) * 0.4
	}
	interleaved := make([]float64, frameSize*2)
	InterleaveStereoInto(left, right, interleaved)

	current := NewDecoder(2)
	legacy := NewDecoder(2)
	current.postfilterMem = make([]float64, combFilterHistory*2)
	legacy.postfilterMem = make([]float64, combFilterHistory*2)
	current.plcDecodeMem = make([]float64, plcDecodeBufferSize*2)
	legacy.plcDecodeMem = make([]float64, plcDecodeBufferSize*2)
	current.postfilterScratch = make([]float64, combFilterHistory+frameSize)
	legacy.postfilterScratch = make([]float64, combFilterHistory+frameSize)
	for i := range current.postfilterMem {
		current.postfilterMem[i] = float64((i%23)-11) * 0.125
	}
	copy(legacy.postfilterMem, current.postfilterMem)
	for i := range current.plcDecodeMem {
		current.plcDecodeMem[i] = float64((i%19)-9) * 0.25
	}
	copy(legacy.plcDecodeMem, current.plcDecodeMem)

	current.postfilterPeriodOld = 42
	current.postfilterPeriod = 44
	current.postfilterGainOld = 0.18
	current.postfilterGain = 0.24
	current.postfilterTapsetOld = 1
	current.postfilterTapset = 2
	legacy.postfilterPeriodOld = current.postfilterPeriodOld
	legacy.postfilterPeriod = current.postfilterPeriod
	legacy.postfilterGainOld = current.postfilterGainOld
	legacy.postfilterGain = current.postfilterGain
	legacy.postfilterTapsetOld = current.postfilterTapsetOld
	legacy.postfilterTapset = current.postfilterTapset

	gotL := append([]float64(nil), left...)
	gotR := append([]float64(nil), right...)
	want := append([]float64(nil), interleaved...)

	current.applyPostfilterStereoPlanar(gotL, gotR, frameSize, lm, 48, 0.31, 0)
	legacy.applyPostfilter(want, frameSize, lm, 48, 0.31, 0)

	got := make([]float64, frameSize*2)
	InterleaveStereoInto(gotL, gotR, got)
	current.materializePLCDecodeHistory()
	legacy.materializePLCDecodeHistory()
	current.materializePostfilterHistoryFromPLC()
	legacy.materializePostfilterHistoryFromPLC()
	requireFloat64BitsEqual(t, got, want)
	requireFloat64BitsEqual(t, current.postfilterMem, legacy.postfilterMem)
	requireFloat64BitsEqual(t, current.plcDecodeMem, legacy.plcDecodeMem)

	if current.postfilterPeriodOld != legacy.postfilterPeriodOld ||
		current.postfilterPeriod != legacy.postfilterPeriod ||
		current.postfilterTapsetOld != legacy.postfilterTapsetOld ||
		current.postfilterTapset != legacy.postfilterTapset ||
		math.Float64bits(current.postfilterGainOld) != math.Float64bits(legacy.postfilterGainOld) ||
		math.Float64bits(current.postfilterGain) != math.Float64bits(legacy.postfilterGain) {
		t.Fatalf("postfilter state mismatch after planar path")
	}
}

func BenchmarkUpdatePLCDecodeHistoryStereoCurrent(b *testing.B) {
	benchmarkUpdateStereoHistory(b, plcDecodeBufferSize, func(d *Decoder, samples []float64, frameSize, history int) {
		d.updatePLCDecodeHistory(samples, frameSize, history)
	})
}

func BenchmarkUpdatePLCDecodeHistoryStereoLegacy(b *testing.B) {
	benchmarkUpdateStereoHistory(b, plcDecodeBufferSize, func(d *Decoder, samples []float64, frameSize, history int) {
		updateStereoHistoryLegacy(d.plcDecodeMem, samples, frameSize, history)
	})
}

func BenchmarkUpdatePostfilterHistoryStereoCurrent(b *testing.B) {
	benchmarkUpdateStereoHistory(b, combFilterHistory, func(d *Decoder, samples []float64, frameSize, history int) {
		d.updatePostfilterHistory(samples, frameSize, history)
	})
}

func BenchmarkUpdatePostfilterHistoryStereoLegacy(b *testing.B) {
	benchmarkUpdateStereoHistory(b, combFilterHistory, func(d *Decoder, samples []float64, frameSize, history int) {
		updateStereoHistoryLegacy(d.postfilterMem, samples, frameSize, history)
	})
}

func benchmarkUpdateStereoHistory(b *testing.B, history int, fn func(*Decoder, []float64, int, int)) {
	const frameSize = 960
	d := NewDecoder(2)
	samples := make([]float64, frameSize*2)
	for i := range samples {
		samples[i] = float64((i%23)-11) * 0.125
	}
	if history == combFilterHistory {
		d.postfilterMem = make([]float64, history*2)
		for i := range d.postfilterMem {
			d.postfilterMem[i] = float64(i%29) * -0.25
		}
	} else {
		d.plcDecodeMem = make([]float64, history*2)
		for i := range d.plcDecodeMem {
			d.plcDecodeMem[i] = float64(i%31) * 0.375
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(d, samples, frameSize, history)
	}
}
