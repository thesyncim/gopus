package celt

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

const (
	libopusCELTEnergyQuantInputMagic  = "GCEI"
	libopusCELTEnergyQuantOutputMagic = "GCEO"

	libopusCELTEnergyQuantFine     = 0
	libopusCELTEnergyQuantFinalise = 1
)

var libopusCELTEnergyQuantHelper libopustest.HelperCache

type libopusCELTEnergyQuantCase struct {
	name         string
	op           int
	start        int
	end          int
	channels     int
	storage      int
	bitsLeft     int
	oldEBands    []float32
	errorVal     []float32
	prevQuant    [MaxBands]int
	fineQuant    [MaxBands]int
	extraQuant   [MaxBands]int
	finePriority [MaxBands]int
}

type libopusCELTEnergyQuantResult struct {
	encError  uint32
	oldEBands []float32
	errorVal  []float32
	packet    []byte
}

func getLibopusCELTEnergyQuantHelperPath() (string, error) {
	return libopusCELTEnergyQuantHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "celt energy quant",
		OutputBase:   "gopus_libopus_celt_energy_quant",
		SourceFile:   "libopus_celt_energy_quant_info.c",
		ProbeRelPath: "celt/quant_bands.h",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func probeLibopusCELTEnergyQuant(cases []libopusCELTEnergyQuantCase) ([]libopusCELTEnergyQuantResult, error) {
	binPath, err := getLibopusCELTEnergyQuantHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusCELTEnergyQuantInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.op))
		payload.I32(int32(tc.start))
		payload.I32(int32(tc.end))
		payload.I32(int32(tc.channels))
		payload.I32(int32(tc.storage))
		payload.I32(int32(tc.bitsLeft))
		payload.Float32s(tc.oldEBands...)
		payload.Float32s(tc.errorVal...)
		for _, v := range tc.prevQuant {
			payload.I32(int32(v))
		}
		for _, v := range tc.fineQuant {
			payload.I32(int32(v))
		}
		for _, v := range tc.extraQuant {
			payload.I32(int32(v))
		}
		for _, v := range tc.finePriority {
			payload.I32(int32(v))
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt energy quant", libopusCELTEnergyQuantOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]libopusCELTEnergyQuantResult, count)
	for i := range out {
		total := cases[i].channels * MaxBands
		out[i].encError = reader.U32()
		packetLen := int(reader.U32())
		out[i].oldEBands = make([]float32, total)
		out[i].errorVal = make([]float32, total)
		for j := range out[i].oldEBands {
			out[i].oldEBands[j] = reader.Float32()
		}
		for j := range out[i].errorVal {
			out[i].errorVal[j] = reader.Float32()
		}
		out[i].packet = append([]byte(nil), reader.Bytes(packetLen)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestLibopusCELTEnergyQuantFineMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEnergyQuantCase{
		celtEnergyQuantFineCase("fine_stereo_thresholds", 2, 0, 12, 64),
		celtEnergyQuantFineCase("fine_mono_budget_skip", 1, 2, 15, 3),
	}
	assertCELTEnergyQuantMatchesLibopus(t, cases)
}

func TestLibopusCELTEnergyFinaliseMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEnergyQuantCase{
		celtEnergyQuantFinaliseCase("finalise_stereo_residual_signs", 2, 0, 14, 64, 17),
		celtEnergyQuantFinaliseCase("finalise_mono_budget_skip", 1, 3, 17, 64, 5),
	}
	assertCELTEnergyQuantMatchesLibopus(t, cases)
}

func TestEncoderEnergyFinaliseRangeFromErrorMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEnergyQuantCase{
		celtEnergyQuantFinaliseCase("hybrid_stereo_tail", 2, HybridCELTStartBand, MaxBands, 64, 7),
	}
	want, err := probeLibopusCELTEnergyQuant(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt energy finalise range", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.channels)
			gotOld := float32sToFloat64s(tc.oldEBands)
			enc.scratch.coarseError = float32sToFloat64s(tc.errorVal)
			buf := make([]byte, tc.storage)
			var re rangecoding.Encoder
			re.Init(buf)
			enc.rangeEncoder = &re

			enc.EncodeEnergyFinaliseRangeFromError(
				gotOld, tc.start, tc.end,
				tc.fineQuant[:], tc.finePriority[:], tc.bitsLeft,
			)

			gotPacket := append([]byte(nil), re.Done()...)
			if uint32(re.Error()) != want[i].encError {
				t.Fatalf("encoder error=%d want %d", re.Error(), want[i].encError)
			}
			if !bytes.Equal(gotPacket, want[i].packet) {
				t.Fatalf("packet=%x want %x", gotPacket, want[i].packet)
			}
			assertFloat64CarriesFloat32Bits(t, "oldEBands", gotOld, want[i].oldEBands)
			assertFloat64CarriesFloat32Bits(t, "error", enc.scratch.coarseError, want[i].errorVal)
		})
	}
}

func TestEncoderFineEnergyRangeFromErrorMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEnergyQuantCase{
		celtEnergyQuantFineCase("hybrid_stereo_tail", 2, HybridCELTStartBand, MaxBands, 64),
	}
	for i := range cases {
		for band := range cases[i].prevQuant {
			cases[i].prevQuant[band] = 0
		}
	}
	want, err := probeLibopusCELTEnergyQuant(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt fine energy range", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.channels)
			gotOld := float32sToFloat64s(tc.oldEBands)
			enc.scratch.coarseError = float32sToFloat64s(tc.errorVal)
			buf := make([]byte, tc.storage)
			var re rangecoding.Encoder
			re.Init(buf)
			enc.rangeEncoder = &re

			enc.EncodeFineEnergyRangeFromError(gotOld, tc.start, tc.end, tc.extraQuant[:])

			gotPacket := append([]byte(nil), re.Done()...)
			if uint32(re.Error()) != want[i].encError {
				t.Fatalf("encoder error=%d want %d", re.Error(), want[i].encError)
			}
			if !bytes.Equal(gotPacket, want[i].packet) {
				t.Fatalf("packet=%x want %x", gotPacket, want[i].packet)
			}
			assertFloat64CarriesFloat32Bits(t, "oldEBands", gotOld, want[i].oldEBands)
			assertFloat64CarriesFloat32Bits(t, "error", enc.scratch.coarseError, want[i].errorVal)
		})
	}
}

func TestEncoderFineEnergyWithPrevMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEnergyQuantCase{
		celtEnergyQuantFineCase("prev_stereo_full", 2, 0, MaxBands, 64),
		celtEnergyQuantFineCase("prev_mono_budget_skip", 1, 0, MaxBands, 3),
	}
	want, err := probeLibopusCELTEnergyQuant(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt fine energy prev", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.channels)
			gotOld := float32sToFloat64s(tc.oldEBands)
			gotErr := float32sToFloat64s(tc.errorVal)
			buf := make([]byte, tc.storage)
			var re rangecoding.Encoder
			re.Init(buf)

			enc.encodeFineEnergyFromErrorWithPrevWithEncoder(&re, gotOld, MaxBands, tc.prevQuant[:], tc.extraQuant[:], gotErr)

			gotPacket := append([]byte(nil), re.Done()...)
			if uint32(re.Error()) != want[i].encError {
				t.Fatalf("encoder error=%d want %d", re.Error(), want[i].encError)
			}
			if !bytes.Equal(gotPacket, want[i].packet) {
				t.Fatalf("packet=%x want %x", gotPacket, want[i].packet)
			}
			assertFloat64CarriesFloat32Bits(t, "oldEBands", gotOld, want[i].oldEBands)
			assertFloat64CarriesFloat32Bits(t, "error", gotErr, want[i].errorVal)
		})
	}
}

func assertCELTEnergyQuantMatchesLibopus(t *testing.T, cases []libopusCELTEnergyQuantCase) {
	t.Helper()
	want, err := probeLibopusCELTEnergyQuant(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt energy quant", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOld := float32sToFloat64s(tc.oldEBands)
			gotErr := float32sToFloat64s(tc.errorVal)
			buf := make([]byte, tc.storage)
			var re rangecoding.Encoder
			re.Init(buf)
			switch tc.op {
			case libopusCELTEnergyQuantFine:
				QuantFineEnergy(&re, tc.start, tc.end, gotOld, gotErr, tc.prevQuant[:], tc.extraQuant[:], tc.channels)
			case libopusCELTEnergyQuantFinalise:
				QuantEnergyFinalise(&re, tc.start, tc.end, gotOld, gotErr, tc.fineQuant[:], tc.finePriority[:], tc.bitsLeft, tc.channels)
			default:
				t.Fatalf("unknown op %d", tc.op)
			}
			gotPacket := append([]byte(nil), re.Done()...)
			if uint32(re.Error()) != want[i].encError {
				t.Fatalf("encoder error=%d want %d", re.Error(), want[i].encError)
			}
			if !bytes.Equal(gotPacket, want[i].packet) {
				t.Fatalf("packet=%x want %x", gotPacket, want[i].packet)
			}
			assertFloat64CarriesFloat32Bits(t, "oldEBands", gotOld, want[i].oldEBands)
			assertFloat64CarriesFloat32Bits(t, "error", gotErr, want[i].errorVal)
		})
	}
}

func assertFloat64CarriesFloat32Bits(t *testing.T, name string, got []float64, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", name, len(got), len(want))
	}
	for i := range got {
		want64 := float64(want[i])
		if got[i] != want64 {
			t.Fatalf("%s[%d]=%08x %.10g want %08x %.10g",
				name, i, math.Float32bits(float32(got[i])), got[i],
				math.Float32bits(want[i]), want64)
		}
	}
}

func float32sToFloat64s(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func celtEnergyQuantFineCase(name string, channels, start, end, storage int) libopusCELTEnergyQuantCase {
	tc := celtEnergyQuantBaseCase(name, libopusCELTEnergyQuantFine, channels, start, end, storage)
	for i := 0; i < MaxBands; i++ {
		tc.prevQuant[i] = i % 4
		tc.extraQuant[i] = 1 + i%8
	}
	for c := 0; c < channels; c++ {
		for band := 0; band < MaxBands; band++ {
			idx := c*MaxBands + band
			prev := tc.prevQuant[band]
			extra := tc.extraQuant[band]
			level := (band*3 + c + 1) % (1 << extra)
			threshold := (float64(level)/float64(uint(1)<<extra) - 0.5) / float64(uint(1)<<prev)
			err := float32(threshold)
			if (band+c)%2 == 0 {
				err = math.Nextafter32(err, float32(math.Inf(1)))
			} else {
				err = math.Nextafter32(err, float32(math.Inf(-1)))
			}
			tc.errorVal[idx] = err
		}
	}
	return tc
}

func celtEnergyQuantFinaliseCase(name string, channels, start, end, storage, bitsLeft int) libopusCELTEnergyQuantCase {
	tc := celtEnergyQuantBaseCase(name, libopusCELTEnergyQuantFinalise, channels, start, end, storage)
	tc.bitsLeft = bitsLeft
	signs := []float32{
		float32(math.Copysign(0, -1)),
		0,
		math.SmallestNonzeroFloat32,
		-math.SmallestNonzeroFloat32,
		float32(math.Nextafter32(0, 1)),
		float32(math.Nextafter32(0, -1)),
		0.03125,
		-0.03125,
	}
	for i := 0; i < MaxBands; i++ {
		tc.fineQuant[i] = i % maxFineBits
		tc.finePriority[i] = i % 2
	}
	for c := 0; c < channels; c++ {
		for band := 0; band < MaxBands; band++ {
			idx := c*MaxBands + band
			tc.errorVal[idx] = signs[(band+c)%len(signs)]
		}
	}
	return tc
}

func celtEnergyQuantBaseCase(name string, op, channels, start, end, storage int) libopusCELTEnergyQuantCase {
	total := channels * MaxBands
	tc := libopusCELTEnergyQuantCase{
		name:      name,
		op:        op,
		start:     start,
		end:       end,
		channels:  channels,
		storage:   storage,
		oldEBands: make([]float32, total),
		errorVal:  make([]float32, total),
	}
	for c := 0; c < channels; c++ {
		for band := 0; band < MaxBands; band++ {
			idx := c*MaxBands + band
			tc.oldEBands[idx] = float32(0.03125*float64((idx%9)-4) + 0.00073*float64(idx+1))
			tc.errorVal[idx] = float32(0.05 * math.Sin(float64(idx+1)))
		}
	}
	if len(tc.oldEBands) != total || len(tc.errorVal) != total {
		panic(fmt.Sprintf("bad energy quant case %s", name))
	}
	return tc
}
