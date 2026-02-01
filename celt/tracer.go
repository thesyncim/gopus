// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
//
// This file provides the minimal tracing infrastructure for the CELT decoder.
// The default NoopTracer ensures zero overhead in production.

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

// NoopTracer is a no-operation tracer with zero overhead.
type NoopTracer struct{}

func (t *NoopTracer) TraceHeader(frameSize, channels, lm, intra, transient int) {}
func (t *NoopTracer) TraceEnergy(band int, coarse, fine, total float64)         {}
func (t *NoopTracer) TraceAllocation(band, bits, k int)                         {}
func (t *NoopTracer) TracePVQ(band int, index uint32, k, n int, pulses []int)   {}
func (t *NoopTracer) TraceCoeffs(band int, coeffs []float64)                    {}
func (t *NoopTracer) TraceSynthesis(stage string, samples []float64)            {}

// DefaultTracer is the global tracer used by the CELT decoder.
var DefaultTracer Tracer = &NoopTracer{}

// SetTracer sets the global tracer for CELT decoding.
func SetTracer(t Tracer) {
	if t == nil {
		DefaultTracer = &NoopTracer{}
	} else {
		DefaultTracer = t
	}
}

func traceRange(stage string, rd *rangecoding.Decoder) {
	if rd == nil {
		return
	}
	if tracer, ok := DefaultTracer.(RangeTracer); ok {
		tracer.TraceRange(stage, rd.Range(), rd.Tell(), rd.TellFrac())
	}
}

func traceFlag(name string, value int) {
	if tracer, ok := DefaultTracer.(FlagTracer); ok {
		tracer.TraceFlag(name, value)
	}
}

func traceLowband(band int, lowbandOffset int, effectiveLowband int, lowband []float64) {
	if tracer, ok := DefaultTracer.(LowbandTracer); ok {
		tracer.TraceLowband(band, lowbandOffset, effectiveLowband, lowband)
	}
}

func traceEnergyFine(band int, channel int, energy float64) {
	if tracer, ok := DefaultTracer.(EnergyFineTracer); ok {
		tracer.TraceEnergyFine(band, channel, energy)
	}
}

func traceEnergyFinal(band int, channel int, energy float64) {
	if tracer, ok := DefaultTracer.(EnergyFinalTracer); ok {
		tracer.TraceEnergyFinal(band, channel, energy)
	}
}

// boolToInt converts a boolean to int (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
