//go:build cgo_libopus

package silk

import "testing"

func TestLTPFullEncodeTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	cfg := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := cfg.SubframeSamples
	frameSamples := numSubfr * subfrLen
	frames := 20

	trace := &EncoderTrace{
		LTP: &LTPTrace{
			CaptureResidual: true,
		},
	}
	enc.trace = trace
	enc.SetVADState(200, 0, [4]int{})

	signal := generateNLSFTraceSignal(frames*frameSamples, cfg.SampleRate)

	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		_ = enc.EncodeFrame(pcm, nil, true)

		lt := trace.LTP
		if lt == nil {
			t.Fatalf("missing LTP trace at frame %d", frame)
		}
		if lt.ResidualLen == 0 || len(lt.Residual) == 0 {
			t.Fatalf("missing residual capture at frame %d", frame)
		}
		if lt.ResidualLen > len(lt.Residual) {
			t.Fatalf("frame %d: residual length %d exceeds captured %d", frame, lt.ResidualLen, len(lt.Residual))
		}

		if len(lt.PitchLags) == 0 {
			continue
		}
		if lt.NbSubfr == 0 || lt.SubfrLen == 0 {
			t.Fatalf("frame %d: invalid LTP trace params nbSubfr=%d subfrLen=%d", frame, lt.NbSubfr, lt.SubfrLen)
		}
		if len(lt.PitchLags) != lt.NbSubfr {
			t.Fatalf("frame %d: pitch lag count %d != nbSubfr %d", frame, len(lt.PitchLags), lt.NbSubfr)
		}

		residual := lt.Residual[:lt.ResidualLen]
		libXX, libXx := libopusFindLTP(residual, lt.ResStart, lt.PitchLags, lt.SubfrLen, lt.NbSubfr)
		if len(libXX) == 0 || len(libXx) == 0 {
			t.Fatalf("frame %d: libopus findLTP failed", frame)
		}
		libRes := libopusQuantLTP(libXX, libXx, lt.SubfrLen, lt.NbSubfr, lt.SumLogGainQ7In)

		if int8(lt.PERIndex) != libRes.PerIndex {
			t.Fatalf("frame %d: PER index go=%d lib=%d", frame, lt.PERIndex, libRes.PerIndex)
		}
		if lt.PredGainQ7 != libRes.PredGainQ7 {
			t.Fatalf("frame %d: predGainQ7 go=%d lib=%d", frame, lt.PredGainQ7, libRes.PredGainQ7)
		}
		for i := 0; i < lt.NbSubfr; i++ {
			if len(lt.LTPIndex) <= i {
				t.Fatalf("frame %d: missing LTPIndex[%d]", frame, i)
			}
			if lt.LTPIndex[i] != libRes.LTPIndex[i] {
				t.Fatalf("frame %d: LTP index[%d] go=%d lib=%d", frame, i, lt.LTPIndex[i], libRes.LTPIndex[i])
			}
		}
		coeffCount := lt.NbSubfr * ltpOrderConst
		if len(lt.BQ14) < coeffCount {
			t.Fatalf("frame %d: BQ14 len=%d want=%d", frame, len(lt.BQ14), coeffCount)
		}
		for i := 0; i < coeffCount; i++ {
			if lt.BQ14[i] != libRes.BQ14[i] {
				t.Fatalf("frame %d: BQ14[%d] go=%d lib=%d", frame, i, lt.BQ14[i], libRes.BQ14[i])
			}
		}
	}
}
