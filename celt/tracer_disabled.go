//go:build !gopus_tmp_env

package celt

import "github.com/thesyncim/gopus/rangecoding"

// Tracer defines the interface for CELT decoder debug tracing.
type Tracer interface {
	TraceHeader(frameSize, channels, lm, intra, transient int)
	TraceEnergy(band int, coarse, fine, total float64)
	TraceAllocation(band, bits, k int)
	TracePVQ(band int, index uint32, k, n int, pulses []int)
	TraceCoeffs(band int, coeffs []float64)
	TraceSynthesis(stage string, samples []float64)
}

// RangeTracer is an optional interface for logging range decoder state.
type RangeTracer interface {
	TraceRange(stage string, rng uint32, tell, tellFrac int)
}

// FlagTracer is an optional interface for logging boolean flags/decisions.
type FlagTracer interface {
	TraceFlag(name string, value int)
}

// FineBitsTracer is an optional interface for logging fine energy bits per band.
type FineBitsTracer interface {
	TraceFineBits(band int, bits int)
}

// LowbandTracer is an optional interface for logging lowband/folding details.
type LowbandTracer interface {
	TraceLowband(band int, lowbandOffset int, effectiveLowband int, lowband []float64)
}

// EnergyFineTracer is an optional interface for logging fine energy values.
type EnergyFineTracer interface {
	TraceEnergyFine(band int, channel int, energy float64)
}

// EnergyFinalTracer is an optional interface for logging energy finalise values.
type EnergyFinalTracer interface {
	TraceEnergyFinal(band int, channel int, energy float64)
}

// TFTracer is an optional interface for logging TF resolution values.
type TFTracer interface {
	TraceTF(band int, val int)
}

// NoopTracer is a no-operation tracer.
type NoopTracer struct{}

func (t *NoopTracer) TraceHeader(frameSize, channels, lm, intra, transient int) {}
func (t *NoopTracer) TraceEnergy(band int, coarse, fine, total float64)         {}
func (t *NoopTracer) TraceAllocation(band, bits, k int)                         {}
func (t *NoopTracer) TracePVQ(band int, index uint32, k, n int, pulses []int)   {}
func (t *NoopTracer) TraceCoeffs(band int, coeffs []float64)                    {}
func (t *NoopTracer) TraceSynthesis(stage string, samples []float64)            {}

// SetTracer is a no-op in production builds.
func SetTracer(_ Tracer) {}

func traceHeader(_ int, _ int, _ int, _ int, _ int) {}

func traceEnergy(_ int, _ float64, _ float64, _ float64) {}

func traceAllocation(_ int, _ int, _ int) {}

func tracePVQ(_ int, _ uint32, _ int, _ int, _ []int) {}

func traceCoeffs(_ int, _ []float64) {}

func traceSynthesis(_ string, _ []float64) {}

func traceRange(_ string, _ *rangecoding.Decoder) {}

func traceFlag(_ string, _ int) {}

func traceFineBits(_ int, _ int) {}

func traceTF(_ int, _ int) {}

func traceLowband(_ int, _ int, _ int, _ []float64) {}

func traceEnergyFine(_ int, _ int, _ float64) {}

func traceEnergyFinal(_ int, _ int, _ float64) {}

// boolToInt converts a boolean to int (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
