package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"math"
	"math/rand"
	"testing"
)

func downFIRInterpolReference(out []int16, buf []int32, firCoefs []int16, firOrder, firFracs int, maxIndexQ16, indexIncrementQ16 int32) int {
	n := 0
	for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
		p := buf[indexQ16>>16:]
		var resQ6 int32
		switch firOrder {
		case resamplerDownOrderFIR0:
			ind := int(silkSMULWB(indexQ16&0xFFFF, int32(firFracs)))
			c := firCoefs[resamplerDownOrderFIR0/2*ind:]
			for k := range resamplerDownOrderFIR0 / 2 {
				resQ6 = silkSMLAWB(resQ6, p[k], int32(c[k]))
			}
			c = firCoefs[resamplerDownOrderFIR0/2*(firFracs-1-ind):]
			for k := range resamplerDownOrderFIR0 / 2 {
				resQ6 = silkSMLAWB(resQ6, p[resamplerDownOrderFIR0-1-k], int32(c[k]))
			}
		default:
			for k := range firOrder / 2 {
				resQ6 = silkSMLAWB(resQ6, p[k]+p[firOrder-1-k], int32(firCoefs[k]))
			}
		}
		out[n] = silkSAT16(silkRSHIFT_ROUND(resQ6, 6))
		n++
	}
	return n
}

func TestDownsamplingFIRInterpolateMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5d0f))
	rates := [][2]int{
		{24000, 16000}, {16000, 12000}, {12000, 8000}, // FIR0, 2/3 and 3/4
		{16000, 8000}, {24000, 12000}, {48000, 24000}, // FIR1, 1/2
		{48000, 16000}, {24000, 8000}, {48000, 12000}, {48000, 8000}, // FIR2
	}
	for _, rt := range rates {
		r := NewDownsamplingResampler(rt[0], rt[1])
		for iter := range 50 {
			nIn := 1 + rng.Intn(480)
			buf := make([]int32, nIn+r.firOrder)
			for i := range buf {
				switch iter % 3 {
				case 0:
					buf[i] = rng.Int31n(1<<24) - 1<<23
				case 1:
					buf[i] = rng.Int31() - 1<<30
				default:
					buf[i] = int32(rng.Uint32())
				}
			}
			maxIndexQ16 := int32(nIn) << 16
			got := make([]int16, nIn)
			want := make([]int16, nIn)
			nGot := r.firInterpolate(got, buf, maxIndexQ16, r.invRatioQ16, 0)
			nWant := downFIRInterpolReference(want, buf, r.firCoefs, r.firOrder, r.firFracs, maxIndexQ16, r.invRatioQ16)
			if nGot != nWant {
				t.Fatalf("%v iter %d: got %d outputs, want %d", rt, iter, nGot, nWant)
			}
			for i := range nWant {
				if got[i] != want[i] {
					t.Fatalf("%v iter %d: out[%d]=%d want %d", rt, iter, i, got[i], want[i])
				}
			}
		}
	}
}

func TestNewLibopusResamplerEncFunctionSelect(t *testing.T) {
	type want struct {
		copy bool
		up2  bool
		down bool
	}
	cases := []struct {
		fsIn, fsOut int
		w           want
	}{
		// in 8k
		{8000, 8000, want{copy: true}}, // C
		{8000, 12000, want{}},          // UF (IIR/FIR, not copy/up2/down)
		{8000, 16000, want{up2: true}}, // U
		// in 12k
		{12000, 8000, want{down: true}},  // AF -> down_FIR
		{12000, 12000, want{copy: true}}, // C
		{12000, 16000, want{}},           // UF
		// in 16k
		{16000, 8000, want{down: true}},  // AF
		{16000, 12000, want{down: true}}, // AF
		{16000, 16000, want{copy: true}}, // C
		// in 24k
		{24000, 8000, want{down: true}},  // AF (1:3)
		{24000, 12000, want{down: true}}, // AF (1:2)
		{24000, 16000, want{down: true}}, // AF (2:3)
		// in 48k
		{48000, 8000, want{down: true}},  // AF (1:6)
		{48000, 12000, want{down: true}}, // AF (1:4)
		{48000, 16000, want{down: true}}, // AF (1:3)
	}
	for _, c := range cases {
		r := NewLibopusResamplerEnc(c.fsIn, c.fsOut)
		gotDown := r.down != nil
		if r.copyMode != c.w.copy || r.up2HQMode != c.w.up2 || gotDown != c.w.down {
			t.Errorf("%d->%d: got copy=%v up2=%v down=%v, want copy=%v up2=%v down=%v",
				c.fsIn, c.fsOut, r.copyMode, r.up2HQMode, gotDown, c.w.copy, c.w.up2, c.w.down)
		}
	}
}

// TestNewLibopusResamplerEncInputDelay verifies the encoder input delay matches
// delay_matrix_enc[rateID(Fs_in)][rateID(Fs_out)] from silk/resampler.c, which is
// distinct from the decoder delay_matrix_dec used by NewLibopusResampler.
func TestNewLibopusResamplerEncInputDelay(t *testing.T) {
	cases := []struct {
		fsIn, fsOut int
		delay       int32
	}{
		{8000, 8000, 6},
		{8000, 16000, 3},
		{12000, 12000, 7},
		{16000, 16000, 10},
		{16000, 12000, 1},
		{24000, 16000, 6},
		{24000, 12000, 2},
		{48000, 16000, 12},
		{48000, 12000, 10},
		{48000, 8000, 18},
	}
	for _, c := range cases {
		r := NewLibopusResamplerEnc(c.fsIn, c.fsOut)
		if r.inputDelay != c.delay {
			t.Errorf("%d->%d enc inputDelay=%d want %d", c.fsIn, c.fsOut, r.inputDelay, c.delay)
		}
	}
}

// TestNewLibopusResamplerDecUnchanged guards that the decoder-side constructor
// still uses delay_matrix_dec, so wiring the encoder variant did not perturb the
// decoder output resampler.
func TestNewLibopusResamplerDecUnchanged(t *testing.T) {
	cases := []struct {
		fsIn, fsOut int
		delay       int32
	}{
		{8000, 8000, 4},   // delay_matrix_dec[0][0]
		{12000, 12000, 9}, // delay_matrix_dec[1][1]
		{16000, 16000, 12},
		{16000, 48000, 7},
	}
	for _, c := range cases {
		r := NewLibopusResampler(c.fsIn, c.fsOut)
		if r.inputDelay != c.delay {
			t.Errorf("%d->%d dec inputDelay=%d want %d", c.fsIn, c.fsOut, r.inputDelay, c.delay)
		}
	}
}

// TestLibopusResamplerEncIdentityCopiesInput verifies the copy path (Fs_in==Fs_out)
// reproduces the input after the libopus 1 ms input-delay priming, matching
// silk_resampler's default (copy) branch.
func TestLibopusResamplerEncIdentityCopiesInput(t *testing.T) {
	r := NewLibopusResamplerEnc(16000, 16000)
	const n = 320 // 20 ms at 16 kHz
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(int16(((i*37)%2000)-1000)) / 32768.0
	}
	out := make([]float32, n)
	got := r.ProcessInto(in, out)
	if got != n {
		t.Fatalf("identity ProcessInto returned %d, want %d", got, n)
	}
	// With a copy resampler the output equals the input shifted by inputDelay
	// (the first inputDelay samples come from the zero-initialized delay buffer).
	delay := int(r.inputDelay)
	for i := delay; i < n; i++ {
		// Quantization to int16 is lossless here (input already int16-valued).
		if out[i] != in[i-delay] {
			t.Fatalf("identity sample %d = %v, want %v (delay=%d)", i, out[i], in[i-delay], delay)
		}
	}
}

func makeSILKResamplerCorpusFrames(class string, fs, frameSamples, frameCount int) ([][]int16, error) {
	src, err := testsignal.GenerateCorpusSignal(class, fs, frameSamples*frameCount, 1)
	if err != nil {
		return nil, err
	}
	frames := make([][]int16, frameCount)
	for f := range frameCount {
		frame := make([]int16, frameSamples)
		for i := range frame {
			frame[i] = float32ToInt16(src[f*frameSamples+i])
		}
		frames[f] = frame
	}
	return frames, nil
}

const (
	libopusSILKResamplerInputMagic  = "GSRI"
	libopusSILKResamplerOutputMagic = "GSRO"
)

var libopusSILKResamplerHelper libopustest.HelperCache

type libopusSILKResamplerRecord struct {
	fsIn   int
	fsOut  int
	forEnc bool
	frames [][]int16
}

func buildLibopusSILKResamplerHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk resampler",
		OutputBase:   "gopus_libopus_silk_resampler",
		SourceFile:   "libopus_silk_resampler_info.c",
		ProbeRelPath: "silk/SigProc_FIX.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources: []string{
			"silk/resampler.c",
			"silk/resampler_private_IIR_FIR.c",
			"silk/resampler_private_up2_HQ.c",
			"silk/resampler_private_down_FIR.c",
			"silk/resampler_private_AR2.c",
			"silk/resampler_rom.c",
		},
	})
}

func getLibopusSILKResamplerHelperPath() (string, error) {
	return libopusSILKResamplerHelper.Path(buildLibopusSILKResamplerHelper)
}

func probeLibopusSILKResampler(records []libopusSILKResamplerRecord) ([][]int16, error) {
	binPath, err := getLibopusSILKResamplerHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKResamplerInputMagic, uint32(len(records)))
	for _, record := range records {
		if len(record.frames) == 0 {
			return nil, fmt.Errorf("empty resampler record")
		}
		frameSamples := len(record.frames[0])
		payload.U32(uint32(record.fsIn))
		payload.U32(uint32(record.fsOut))
		if record.forEnc {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(frameSamples))
		payload.U32(uint32(len(record.frames)))
		for _, frame := range record.frames {
			if len(frame) != frameSamples {
				return nil, fmt.Errorf("mixed frame lengths in %d->%d record", record.fsIn, record.fsOut)
			}
			for _, sample := range frame {
				payload.I16(sample)
			}
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk resampler", libopusSILKResamplerOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	out := make([][]int16, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]int16, n)
		for j := range out[i] {
			out[i][j] = reader.I16()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKEncoderDownsamplingResamplerMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKResamplerRecord{
		{fsIn: 24000, fsOut: 8000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x0f1e2d3c)},
		{fsIn: 24000, fsOut: 12000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x10293847)},
		{fsIn: 24000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x55667788)},
		{fsIn: 48000, fsOut: 8000, forEnc: true, frames: makeSILKResamplerFrames(480, 6, 0x89abcdef)},
		{fsIn: 48000, fsOut: 12000, forEnc: true, frames: makeSILKResamplerFrames(480, 6, 0xdecafbad)},
		{fsIn: 48000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(480, 6, 0xc001d00d)},
	}
	want, err := probeLibopusSILKResampler(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk encoder resampler", err)
	}

	for recIdx, record := range records {
		t.Run(fmt.Sprintf("%d_to_%d_%dms", record.fsIn, record.fsOut, len(record.frames[0])*1000/record.fsIn), func(t *testing.T) {
			resampler := NewDownsamplingResampler(record.fsIn, record.fsOut)
			frameOut := len(record.frames[0]) * record.fsOut / record.fsIn
			out := make([]int16, frameOut)
			wantOffset := 0
			for frameIdx, frame := range record.frames {
				clear(out)
				resampler.processWithDelay(out, frame)
				for i, got := range out {
					wantSample := want[recIdx][wantOffset+i]
					if got != wantSample {
						t.Fatalf("frame %d sample %d got %d want %d", frameIdx, i, got, wantSample)
					}
				}
				wantOffset += frameOut
			}
			if wantOffset != len(want[recIdx]) {
				t.Fatalf("checked %d output samples want %d", wantOffset, len(want[recIdx]))
			}
		})
	}
}

// TestSILKEncoderUpsampleResamplerMatchesLibopusOracle covers the encoder-side
// copy / up2 / IIR-FIR paths (silk_resampler_init forEnc=1) where API_fs <= the
// internal fs_kHz, i.e. native sub-48 kHz SILK input at API rates at or below
// the bandwidth's internal rate. These select delay_matrix_enc (not _dec).
func TestSILKEncoderUpsampleResamplerMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKResamplerRecord{
		{fsIn: 8000, fsOut: 8000, forEnc: true, frames: makeSILKResamplerFrames(160, 6, 0x0a0b0c0d)},   // copy
		{fsIn: 8000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(160, 6, 0x1a2b3c4d)},  // up2
		{fsIn: 8000, fsOut: 12000, forEnc: true, frames: makeSILKResamplerFrames(160, 6, 0x2a3b4c5d)},  // IIR-FIR
		{fsIn: 12000, fsOut: 12000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x3a4b5c6d)}, // copy
		{fsIn: 12000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x4a5b6c7d)}, // IIR-FIR
		{fsIn: 16000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(320, 6, 0x5a6b7c8d)}, // copy
		{fsIn: 12000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(720, 4, 0x6a7b8c9d)}, // IIR-FIR 60ms
		{fsIn: 24000, fsOut: 8000, forEnc: true, frames: makeSILKResamplerFrames(1440, 4, 0x7a8b9cad)}, // down 60ms
	}
	// Exercise the resampler on the exact corpus signal + frame layout the
	// sub-48k encode parity gate uses for the two 60 ms residual configs, so any
	// resampler divergence on real data is caught here (not just synthetic).
	// silk_wb_60ms_mono at API 12 kHz clamps to MB (Nyquist), so the SILK input
	// resampler is the 12->12 copy; silk_nb_60ms_stereo at API 24 kHz is 24->8.
	if cf, err := makeSILKResamplerCorpusFrames(testsignal.CorpusSpeechInNoiseV1, 12000, 720, 6); err == nil {
		records = append(records, libopusSILKResamplerRecord{fsIn: 12000, fsOut: 12000, forEnc: true, frames: cf})
	}
	if cf, err := makeSILKResamplerCorpusFrames(testsignal.CorpusCleanSpeechV1, 24000, 1440, 6); err == nil {
		records = append(records, libopusSILKResamplerRecord{fsIn: 24000, fsOut: 8000, forEnc: true, frames: cf})
	}
	want, err := probeLibopusSILKResampler(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk encoder upsample resampler", err)
	}

	for recIdx, record := range records {
		t.Run(fmt.Sprintf("%d_to_%d_%dms", record.fsIn, record.fsOut, len(record.frames[0])*1000/record.fsIn), func(t *testing.T) {
			resampler := NewLibopusResamplerEnc(record.fsIn, record.fsOut)
			frameOut := len(record.frames[0]) * record.fsOut / record.fsIn
			out := make([]float32, frameOut)
			wantOffset := 0
			for frameIdx, frame := range record.frames {
				n := resampler.ProcessInt16Into(frame, out)
				if n != frameOut {
					t.Fatalf("frame %d output samples=%d want %d", frameIdx, n, frameOut)
				}
				for i := range n {
					wantSample := want[recIdx][wantOffset+i]
					wantFloat := float32(wantSample) * (1.0 / 32768.0)
					if math.Float32bits(out[i]) != math.Float32bits(wantFloat) {
						t.Fatalf("frame %d sample %d got %08x(%0.10g) want %08x(%0.10g) from int16 %d",
							frameIdx, i,
							math.Float32bits(out[i]), out[i],
							math.Float32bits(wantFloat), wantFloat,
							wantSample)
					}
				}
				wantOffset += n
			}
			if wantOffset != len(want[recIdx]) {
				t.Fatalf("checked %d output samples want %d", wantOffset, len(want[recIdx]))
			}
		})
	}
}

func makeSILKResamplerFrames(frameSamples, frameCount int, seed uint32) [][]int16 {
	frames := make([][]int16, frameCount)
	for frame := range frames {
		samples := make([]int16, frameSamples)
		for i := range samples {
			seed = 1664525*seed + 1013904223
			v := int32(seed>>16) - 32768
			v = v/2 + int32((frame*37+i*11)%2048) - 1024
			if i%31 == 0 {
				v = int32((frame+i)%5-2) * 12000
			}
			if v > 30000 {
				v = 30000
			} else if v < -30000 {
				v = -30000
			}
			samples[i] = int16(v)
		}
		frames[frame] = samples
	}
	return frames
}

func TestSILKDecoderResamplerProcessInt16IntoMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKResamplerRecord{
		{fsIn: 8000, fsOut: 48000, frames: makeSILKResamplerFrames(80, 8, 0x13579bdf)},
		{fsIn: 8000, fsOut: 48000, frames: makeSILKResamplerFrames(160, 5, 0x2468ace0)},
		{fsIn: 8000, fsOut: 8000, frames: makeSILKResamplerFrames(160, 5, 0x0f1e2d3c)},
		{fsIn: 12000, fsOut: 48000, frames: makeSILKResamplerFrames(120, 8, 0x10203040)},
		{fsIn: 12000, fsOut: 8000, frames: makeSILKResamplerFrames(240, 5, 0x45670123)},
		{fsIn: 12000, fsOut: 12000, frames: makeSILKResamplerFrames(240, 5, 0x76543210)},
		{fsIn: 12000, fsOut: 24000, frames: makeSILKResamplerFrames(240, 5, 0x6789abcd)},
		{fsIn: 16000, fsOut: 48000, frames: makeSILKResamplerFrames(160, 8, 0x55667788)},
		{fsIn: 16000, fsOut: 48000, frames: makeSILKResamplerFrames(320, 5, 0xa5a55a5a)},
		{fsIn: 16000, fsOut: 8000, frames: makeSILKResamplerFrames(320, 5, 0x31415926)},
		{fsIn: 16000, fsOut: 12000, frames: makeSILKResamplerFrames(320, 5, 0x27182818)},
		{fsIn: 16000, fsOut: 16000, frames: makeSILKResamplerFrames(320, 5, 0xabcdef01)},
		{fsIn: 16000, fsOut: 24000, frames: makeSILKResamplerFrames(320, 5, 0x1234fedc)},
	}
	want, err := probeLibopusSILKResampler(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk resampler", err)
	}

	for recIdx, record := range records {
		t.Run(fmt.Sprintf("%d_to_%d_%dms", record.fsIn, record.fsOut, len(record.frames[0])*1000/record.fsIn), func(t *testing.T) {
			resampler := NewLibopusResampler(record.fsIn, record.fsOut)
			frameOut := len(record.frames[0]) * record.fsOut / record.fsIn
			out := make([]float32, frameOut)
			wantOffset := 0
			for frameIdx, frame := range record.frames {
				n := resampler.ProcessInt16Into(frame, out)
				if n != frameOut {
					t.Fatalf("frame %d output samples=%d want %d", frameIdx, n, frameOut)
				}
				for i := range n {
					wantSample := want[recIdx][wantOffset+i]
					wantFloat := float32(wantSample) * (1.0 / 32768.0)
					if math.Float32bits(out[i]) != math.Float32bits(wantFloat) {
						t.Fatalf("frame %d sample %d got %08x(%0.10g) want %08x(%0.10g) from int16 %d",
							frameIdx, i,
							math.Float32bits(out[i]), out[i],
							math.Float32bits(wantFloat), wantFloat,
							wantSample)
					}
				}
				wantOffset += n
			}
			if wantOffset != len(want[recIdx]) {
				t.Fatalf("checked %d output samples want %d", wantOffset, len(want[recIdx]))
			}
		})
	}
}

func TestSILKDecoderResamplerBothOutputsMatchLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKResamplerRecord{
		{fsIn: 12000, fsOut: 8000, frames: makeSILKResamplerFrames(240, 8, 0x45670123)},
		{fsIn: 16000, fsOut: 8000, frames: makeSILKResamplerFrames(320, 8, 0x31415926)},
		{fsIn: 16000, fsOut: 12000, frames: makeSILKResamplerFrames(320, 8, 0x27182818)},
	}
	want, err := probeLibopusSILKResampler(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk resampler", err)
	}
	for recIdx, record := range records {
		for _, floatInput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d_to_%d_floatInput_%t", record.fsIn, record.fsOut, floatInput), func(t *testing.T) {
				resampler := NewLibopusResampler(record.fsIn, record.fsOut)
				frameOut := len(record.frames[0]) * record.fsOut / record.fsIn
				outFloat := make([]float32, frameOut)
				outInt := make([]int16, frameOut)
				inputFloat := make([]float32, len(record.frames[0]))
				process := func(frame []int16) int {
					if !floatInput {
						return resampler.ProcessInt16IntoBoth(frame, outFloat, outInt)
					}
					for i, sample := range frame {
						inputFloat[i] = float32(sample) * (1.0 / 32768.0)
					}
					return resampler.ProcessIntoBoth(inputFloat, outFloat, outInt)
				}
				for frameIdx, frame := range record.frames {
					if n := process(frame); n != frameOut {
						t.Fatalf("frame%d output samples=%d want%d", frameIdx, n, frameOut)
					}
					for i := range frameOut {
						expected := want[recIdx][frameIdx*frameOut+i]
						expectedFloat := float32(expected) * (1.0 / 32768.0)
						if outInt[i] != expected || math.Float32bits(outFloat[i]) != math.Float32bits(expectedFloat) {
							t.Fatalf("frame%d sample%d int16=%d want%d float=%08x want%08x", frameIdx, i, outInt[i], expected, math.Float32bits(outFloat[i]), math.Float32bits(expectedFloat))
						}
					}
				}
				if allocs := testing.AllocsPerRun(100, func() { process(record.frames[0]) }); allocs != 0 {
					t.Fatalf("warm resampler allocations=%g want0", allocs)
				}
			})
		}
	}
}

func TestDownsamplingResamplerAR2Filter(t *testing.T) {
	// Create a resampler for 48kHz -> 16kHz (1:3 ratio)
	// This uses the 1:3 coefficients with AR2 filter
	r := NewDownsamplingResampler(48000, 16000)

	// Verify coefficients are loaded correctly
	// From libopus resampler_rom.c silk_Resampler_1_3_COEFS:
	// First 2 values are AR2 coefficients: 16102, -15162 (Q14)
	expectedAR2_0 := int16(16102)
	expectedAR2_1 := int16(-15162)

	if r.coefs[0] != expectedAR2_0 {
		t.Errorf("AR2 coef[0]: expected %d, got %d", expectedAR2_0, r.coefs[0])
	}
	if r.coefs[1] != expectedAR2_1 {
		t.Errorf("AR2 coef[1]: expected %d, got %d", expectedAR2_1, r.coefs[1])
	}

	t.Logf("AR2 coefficients: A0=%d, A1=%d (Q14)", r.coefs[0], r.coefs[1])
}

// TestDownsamplingResamplerQuality tests the quality of downsampling.
func TestDownsamplingResamplerQuality(t *testing.T) {
	// Create a 1kHz sine wave at 48kHz sample rate
	inputRate := 48000
	outputRate := 16000
	duration := 0.2 // 200ms
	freq := 1000.0  // 1kHz

	nSamplesIn := int(float64(inputRate) * duration)

	input := make([]float32, nSamplesIn)
	for i := range nSamplesIn {
		ti := float64(i) / float64(inputRate)
		input[i] = float32(0.9 * math.Sin(2*math.Pi*freq*ti))
	}

	// Resample
	r := NewDownsamplingResampler(inputRate, outputRate)
	output := r.Process(input)

	if len(output) == 0 {
		t.Fatal("No output from resampler")
	}

	t.Logf("Input: %d samples @ %dHz, Output: %d samples @ %dHz",
		nSamplesIn, inputRate, len(output), outputRate)

	// Skip first 100 samples for filter settling
	skip := 100

	// Compare RMS energy
	var inputRMS, outputRMS float64
	for _, v := range input {
		inputRMS += float64(v) * float64(v)
	}
	inputRMS = math.Sqrt(inputRMS / float64(len(input)))

	for _, v := range output[skip:] {
		outputRMS += float64(v) * float64(v)
	}
	outputRMS = math.Sqrt(outputRMS / float64(len(output)-skip))

	t.Logf("Input RMS: %.4f", inputRMS)
	t.Logf("Output RMS: %.4f", outputRMS)
	t.Logf("Output/Input ratio: %.4f", outputRMS/inputRMS)

	// Check that output energy is reasonable (should be close to input)
	energyRatio := outputRMS / inputRMS
	if energyRatio < 0.8 || energyRatio > 1.2 {
		t.Errorf("Energy ratio %.4f is too far from 1.0", energyRatio)
	}

	// Measure frequency preservation via zero crossings
	zeroCrossings := 0
	for i := skip + 1; i < len(output); i++ {
		if (output[i-1] >= 0 && output[i] < 0) || (output[i-1] < 0 && output[i] >= 0) {
			zeroCrossings++
		}
	}

	// Expected zero crossings: 2 per period * frequency * duration (after skip)
	outputDuration := float64(len(output)-skip) / float64(outputRate)
	expectedCrossings := 2 * freq * outputDuration
	crossingRatio := float64(zeroCrossings) / expectedCrossings

	t.Logf("Zero crossings: %d, expected ~%.0f, ratio: %.4f", zeroCrossings, expectedCrossings, crossingRatio)

	// The frequency should be preserved (crossings within 5%)
	if crossingRatio < 0.95 || crossingRatio > 1.05 {
		t.Errorf("Zero crossing ratio %.4f indicates frequency distortion", crossingRatio)
	}

}

// TestDownsamplingResamplerSNR measures the signal-to-noise ratio of the resampler.
// Note: Measuring absolute SNR against an ideal signal is challenging due to filter delay.
// Instead, we verify that the resampler preserves signal energy reasonably.
func TestDownsamplingResamplerSNR(t *testing.T) {
	// Test multiple ratios
	testCases := []struct {
		name    string
		inRate  int
		outRate int
		freq    float64 // Test frequency
	}{
		{"48kHz->16kHz (1:3)", 48000, 16000, 1000},
		{"48kHz->12kHz (1:4)", 48000, 12000, 1000},
		{"48kHz->8kHz (1:6)", 48000, 8000, 1000},
		{"48kHz->24kHz (1:2)", 48000, 24000, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate input signal (sine wave)
			duration := 0.2 // 200ms for better frequency measurement
			nSamplesIn := int(float64(tc.inRate) * duration)

			input := make([]float32, nSamplesIn)
			for i := range nSamplesIn {
				ti := float64(i) / float64(tc.inRate)
				input[i] = float32(0.9 * math.Sin(2*math.Pi*tc.freq*ti))
			}

			// Resample
			r := NewDownsamplingResampler(tc.inRate, tc.outRate)
			output := r.Process(input)

			// Skip initial transient (filter settling time)
			// The FIR filter has order 18/24/36, so skip ~50 output samples
			skip := 100
			if skip >= len(output) {
				t.Skipf("Not enough samples for analysis")
				return
			}

			// Measure input and output RMS energy
			var inputRMS, outputRMS float64
			for _, v := range input {
				inputRMS += float64(v) * float64(v)
			}
			inputRMS = math.Sqrt(inputRMS / float64(len(input)))

			for _, v := range output[skip:] {
				outputRMS += float64(v) * float64(v)
			}
			outputRMS = math.Sqrt(outputRMS / float64(len(output)-skip))

			energyRatio := outputRMS / inputRMS
			t.Logf("Input RMS: %.4f, Output RMS: %.4f, Ratio: %.4f", inputRMS, outputRMS, energyRatio)

			// The energy should be preserved reasonably well (within 20% or so)
			if energyRatio < 0.5 || energyRatio > 1.5 {
				t.Errorf("Energy ratio %.4f is too far from 1.0 - resampler may have significant issues", energyRatio)
			}

			// Verify that the output frequency is correct by counting zero crossings
			zeroCrossings := 0
			for i := skip + 1; i < len(output); i++ {
				if (output[i-1] >= 0 && output[i] < 0) || (output[i-1] < 0 && output[i] >= 0) {
					zeroCrossings++
				}
			}

			// Expected zero crossings: 2 per period * frequency * duration (after skip)
			outputDuration := float64(len(output)-skip) / float64(tc.outRate)
			expectedCrossings := 2 * tc.freq * outputDuration
			crossingRatio := float64(zeroCrossings) / expectedCrossings

			t.Logf("Zero crossings: %d, expected ~%.0f, ratio: %.4f", zeroCrossings, expectedCrossings, crossingRatio)

			// The frequency should be preserved (crossings within 10%)
			if crossingRatio < 0.9 || crossingRatio > 1.1 {
				t.Errorf("Zero crossing ratio %.4f indicates frequency distortion", crossingRatio)
			}
		})
	}
}

// TestAR2FilterImplementation tests the AR2 filter against expected behavior.
// This is a direct comparison with the libopus silk_resampler_private_AR2 function.
func TestAR2FilterImplementation(t *testing.T) {
	// Create resampler to get access to AR2 filter
	r := NewDownsamplingResampler(48000, 16000)

	// Test with a simple impulse
	input := make([]int16, 10)
	input[0] = 32767 // Impulse

	// Expected behavior from libopus:
	// out32 = S[0] + (in[k] << 8)
	// out_Q8[k] = out32
	// out32_shifted = out32 << 2
	// S[0] = S[1] + silk_SMULWB(out32_shifted, A_Q14[0])
	// S[1] = silk_SMULWB(out32_shifted, A_Q14[1])

	// Get the AR2 coefficients
	A0 := int32(r.coefs[0]) // 16102 in Q14
	A1 := int32(r.coefs[1]) // -15162 in Q14

	t.Logf("AR2 coefficients: A0=%d, A1=%d", A0, A1)

	// Manually compute expected output using libopus algorithm
	S := [2]int32{0, 0}
	expectedOut := make([]int32, len(input))

	for k := range input {
		// libopus AR2 algorithm:
		out32 := S[0] + (int32(input[k]) << 8)
		expectedOut[k] = out32

		out32Shifted := out32 << 2
		S[0] = S[1] + silkSMULWB(out32Shifted, A0)
		S[1] = silkSMULWB(out32Shifted, A1)
	}

	// Now run through the resampler's AR2
	r.sIIR = [2]int32{0, 0}
	actualOut := make([]int32, len(input))
	r.ar2Filter(actualOut, input)

	// Compare
	t.Logf("Impulse response comparison:")
	for i := range input {
		t.Logf("  [%d] expected=%d, actual=%d, diff=%d",
			i, expectedOut[i], actualOut[i], actualOut[i]-expectedOut[i])
	}

	// Check if they match
	matches := 0
	for i := range input {
		if actualOut[i] == expectedOut[i] {
			matches++
		}
	}

	if matches != len(input) {
		t.Errorf("AR2 filter implementation does not match libopus! %d/%d samples match",
			matches, len(input))
	} else {
		t.Logf("AR2 filter implementation matches libopus!")
	}
}

func TestDownsamplingResamplerZeroStateMatchesFreshResampler(t *testing.T) {
	dirty := NewDownsamplingResampler(48000, 16000)
	fresh := NewDownsamplingResampler(48000, 16000)

	warm := make([]float32, 480)
	for i := range warm {
		warm[i] = float32((i%31)-15) / 31.0
	}
	warmOut := make([]float32, 160)
	dirty.ProcessInto(warm, warmOut)

	dirty.SetState(DownsamplingResamplerState{})

	in := make([]float32, 480)
	for i := 168; i < len(in); i++ {
		in[i] = float32(((i*17)%29)-14) / 29.0
	}

	got := make([]float32, 160)
	want := make([]float32, 160)
	nGot := dirty.ProcessInto(in, got)
	nWant := fresh.ProcessInto(in, want)
	if nGot != nWant {
		t.Fatalf("output len=%d want=%d", nGot, nWant)
	}
	for i := range nWant {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%.9f want %.9f", i, got[i], want[i])
		}
	}
}

// TestResampleStereoInt16MatchesProcessInt16Into checks that resampling both
// channels with ResampleStereoInt16 and interleaving them with
// InterleaveInt16AsFloat32 matches ProcessInt16Into per channel followed by an
// interleave, sample for sample and across calls, and allocates nothing.
func TestResampleStereoInt16MatchesProcessInt16Into(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5e7e))
	for _, rates := range [][2]int{{8000, 8000}, {16000, 16000}, {8000, 12000}, {12000, 24000}, {16000, 24000}, {16000, 48000}, {8000, 48000}, {12000, 48000}} {
		fsIn, fsOut := rates[0], rates[1]
		gotL, gotR := NewLibopusResampler(fsIn, fsOut), NewLibopusResampler(fsIn, fsOut)
		wantL, wantR := NewLibopusResampler(fsIn, fsOut), NewLibopusResampler(fsIn, fsOut)
		for iter := range 40 {
			n := fsIn / 1000 * (5 + 5*rng.Intn(4))
			if iter%7 == 0 {
				n = 1 + rng.Intn(fsIn/1000)
			}
			left := make([]int16, n)
			right := make([]int16, n)
			for i := range left {
				left[i] = int16(rng.Uint32())
				right[i] = int16(rng.Uint32())
			}
			outL, outR, ok := ResampleStereoInt16(gotL, gotR, left, right)
			if !ok {
				t.Fatalf("%d->%d: ResampleStereoInt16 declined an upsampling configuration", fsIn, fsOut)
			}
			m := min(len(outL), len(outR))
			got := make([]float32, 2*m)
			InterleaveInt16AsFloat32(got, outL[:m], outR[:m])

			bufL := make([]float32, 2*m+8)
			bufR := make([]float32, 2*m+8)
			nL := wantL.ProcessInt16Into(left, bufL)
			nR := wantR.ProcessInt16Into(right, bufR)
			if nL != m || nR != m {
				t.Fatalf("%d->%d iter %d: ProcessInt16Into wrote %d/%d, want %d", fsIn, fsOut, iter, nL, nR, m)
			}
			for i := range m {
				if math.Float32bits(got[2*i]) != math.Float32bits(bufL[i]) || math.Float32bits(got[2*i+1]) != math.Float32bits(bufR[i]) {
					t.Fatalf("%d->%d iter %d: sample %d = (%g, %g), want (%g, %g)", fsIn, fsOut, iter, i, got[2*i], got[2*i+1], bufL[i], bufR[i])
				}
			}
		}
		left := make([]int16, fsIn/100)
		right := make([]int16, fsIn/100)
		dst := make([]float32, 2*fsOut/100)
		if allocs := testing.AllocsPerRun(100, func() {
			outL, outR, _ := ResampleStereoInt16(gotL, gotR, left, right)
			InterleaveInt16AsFloat32(dst, outL, outR)
		}); allocs != 0 {
			t.Fatalf("%d->%d: %v allocs per run, want 0", fsIn, fsOut, allocs)
		}
	}
	if _, _, ok := ResampleStereoInt16(NewLibopusResampler(48000, 16000), NewLibopusResampler(48000, 16000), make([]int16, 480), make([]int16, 480)); ok {
		t.Fatal("ResampleStereoInt16 accepted a down_FIR configuration")
	}
}
