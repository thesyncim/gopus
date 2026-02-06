// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
//
// This file provides the minimal tracing infrastructure for the CELT decoder.
// The default NoopTracer ensures zero overhead in production.

package celt

import "github.com/thesyncim/gopus/rangecoding"

// Decoder tracing is compiled out on the hot decode path for performance.
// Enable for diagnostics by changing this constant locally when needed.
const decodeTracingEnabled = false

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

// NoopTracer is a no-operation tracer with zero overhead.
type NoopTracer struct{}

func (t *NoopTracer) TraceHeader(frameSize, channels, lm, intra, transient int) {}
func (t *NoopTracer) TraceEnergy(band int, coarse, fine, total float64)         {}
func (t *NoopTracer) TraceAllocation(band, bits, k int)                         {}
func (t *NoopTracer) TracePVQ(band int, index uint32, k, n int, pulses []int)   {}
func (t *NoopTracer) TraceCoeffs(band int, coeffs []float64)                    {}
func (t *NoopTracer) TraceSynthesis(stage string, samples []float64)            {}

var defaultNoopTracer = &NoopTracer{}

// DefaultTracer is the global tracer used by the CELT decoder.
var DefaultTracer Tracer = defaultNoopTracer

// Cached tracer interfaces for the hot decode path.
var (
	tracerCache      Tracer
	tracerEnabled    bool
	rangeTracerCache RangeTracer
	flagTracerCache  FlagTracer
	fineBitsCache    FineBitsTracer
	lowbandCache     LowbandTracer
	energyFineCache  EnergyFineTracer
	energyFinalCache EnergyFinalTracer
	tfTracerCache    TFTracer
)

func refreshTracerCache() {
	if !decodeTracingEnabled {
		tracerCache = defaultNoopTracer
		tracerEnabled = false
		rangeTracerCache = nil
		flagTracerCache = nil
		fineBitsCache = nil
		lowbandCache = nil
		energyFineCache = nil
		energyFinalCache = nil
		tfTracerCache = nil
		return
	}

	t := DefaultTracer
	if t == nil {
		t = defaultNoopTracer
		DefaultTracer = t
	}
	tracerCache = t

	if _, ok := t.(*NoopTracer); ok {
		tracerEnabled = false
		rangeTracerCache = nil
		flagTracerCache = nil
		fineBitsCache = nil
		lowbandCache = nil
		energyFineCache = nil
		energyFinalCache = nil
		tfTracerCache = nil
		return
	}

	tracerEnabled = true
	rangeTracerCache, _ = t.(RangeTracer)
	flagTracerCache, _ = t.(FlagTracer)
	fineBitsCache, _ = t.(FineBitsTracer)
	lowbandCache, _ = t.(LowbandTracer)
	energyFineCache, _ = t.(EnergyFineTracer)
	energyFinalCache, _ = t.(EnergyFinalTracer)
	tfTracerCache, _ = t.(TFTracer)
}

// SetTracer sets the global tracer for CELT decoding.
func SetTracer(t Tracer) {
	if t == nil {
		t = defaultNoopTracer
	}
	DefaultTracer = t
	refreshTracerCache()
}

func traceHeader(frameSize, channels, lm, intra, transient int) {
	if !decodeTracingEnabled {
		return
	}
	if !tracerEnabled {
		return
	}
	tracerCache.TraceHeader(frameSize, channels, lm, intra, transient)
}

func traceEnergy(band int, coarse, fine, total float64) {
	if !decodeTracingEnabled {
		return
	}
	if !tracerEnabled {
		return
	}
	tracerCache.TraceEnergy(band, coarse, fine, total)
}

func traceAllocation(band, bits, k int) {
	if !decodeTracingEnabled {
		return
	}
	if !tracerEnabled {
		return
	}
	tracerCache.TraceAllocation(band, bits, k)
}

func tracePVQ(band int, index uint32, k, n int, pulses []int) {
	if !decodeTracingEnabled {
		return
	}
	if !tracerEnabled {
		return
	}
	tracerCache.TracePVQ(band, index, k, n, pulses)
}

func traceCoeffs(band int, coeffs []float64) {
	if !decodeTracingEnabled {
		return
	}
	if !tracerEnabled {
		return
	}
	tracerCache.TraceCoeffs(band, coeffs)
}

func traceSynthesis(stage string, samples []float64) {
	if !decodeTracingEnabled {
		return
	}
	if !tracerEnabled {
		return
	}
	tracerCache.TraceSynthesis(stage, samples)
}

func traceRange(stage string, rd *rangecoding.Decoder) {
	if !decodeTracingEnabled {
		return
	}
	if rd == nil {
		return
	}
	if tracer := rangeTracerCache; tracer != nil {
		tracer.TraceRange(stage, rd.Range(), rd.Tell(), rd.TellFrac())
	}
}

func traceFlag(name string, value int) {
	if !decodeTracingEnabled {
		return
	}
	if tracer := flagTracerCache; tracer != nil {
		tracer.TraceFlag(name, value)
	}
}

func traceFineBits(band, bits int) {
	if !decodeTracingEnabled {
		return
	}
	if tracer := fineBitsCache; tracer != nil {
		tracer.TraceFineBits(band, bits)
	}
}

func traceTF(band, val int) {
	if !decodeTracingEnabled {
		return
	}
	if tracer := tfTracerCache; tracer != nil {
		tracer.TraceTF(band, val)
	}
}

func traceLowband(band int, lowbandOffset int, effectiveLowband int, lowband []float64) {
	if !decodeTracingEnabled {
		return
	}
	if tracer := lowbandCache; tracer != nil {
		tracer.TraceLowband(band, lowbandOffset, effectiveLowband, lowband)
	}
}

func traceEnergyFine(band int, channel int, energy float64) {
	if !decodeTracingEnabled {
		return
	}
	if tracer := energyFineCache; tracer != nil {
		tracer.TraceEnergyFine(band, channel, energy)
	}
}

func traceEnergyFinal(band int, channel int, energy float64) {
	if !decodeTracingEnabled {
		return
	}
	if tracer := energyFinalCache; tracer != nil {
		tracer.TraceEnergyFinal(band, channel, energy)
	}
}

func init() {
	refreshTracerCache()
}

// boolToInt converts a boolean to int (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
