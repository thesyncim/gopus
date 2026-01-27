// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
//
// This file provides debug tracing infrastructure for the CELT decoder pipeline
// to identify where decoded values diverge from reference (libopus).
//
// Tracing is zero-overhead when disabled (NoopTracer is the default).

package celt

import (
	"fmt"
	"io"
	"strings"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Tracer defines the interface for CELT decoder debug tracing.
// Implement this interface to capture intermediate decoding values
// for comparison with libopus reference output.
//
// All methods should be lightweight - in production, DefaultTracer
// is NoopTracer which has zero overhead.
type Tracer interface {
	// TraceHeader logs frame header parameters after decoding.
	// frameSize: output samples (120, 240, 480, 960)
	// channels: 1 (mono) or 2 (stereo)
	// lm: log2 of frame size ratio (0-3)
	// intra: 1 if intra frame (no inter-frame prediction)
	// transient: 1 if transient mode (short blocks)
	TraceHeader(frameSize, channels, lm, intra, transient int)

	// TraceEnergy logs per-band energy values.
	// band: band index (0-20)
	// coarse: coarse energy prediction (from inter-frame/inter-band)
	// fine: fine energy refinement
	// total: final energy = coarse + fine quantization
	TraceEnergy(band int, coarse, fine, total float64)

	// TraceAllocation logs bit allocation results per band.
	// band: band index
	// bits: bits allocated to this band
	// k: number of PVQ pulses derived from bits
	TraceAllocation(band, bits, k int)

	// TracePVQ logs PVQ decoding results.
	// band: band index
	// index: CWRS index decoded from bitstream
	// k: number of pulses
	// n: band width (dimensions)
	// pulses: decoded pulse vector (integer values)
	TracePVQ(band int, index uint32, k, n int, pulses []int)

	// TraceCoeffs logs denormalized MDCT coefficients per band.
	// band: band index
	// coeffs: denormalized coefficients (after scaling by energy)
	TraceCoeffs(band int, coeffs []float64)

	// TraceSynthesis logs synthesis stage outputs.
	// stage: synthesis stage name (e.g., "imdct", "window", "overlap", "final")
	// samples: output samples at this stage (first N for brevity)
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

// LowbandTracer is an optional interface for logging lowband/folding details.
type LowbandTracer interface {
	TraceLowband(band int, lowbandOffset int, effectiveLowband int, lowband []float64)
}

// EnergyFineTracer is an optional interface for logging fine energy values.
// This is emitted after fine energy refinement is applied.
type EnergyFineTracer interface {
	TraceEnergyFine(band int, channel int, energy float64)
}

// EnergyFinalTracer is an optional interface for logging energy finalise values.
// This is emitted when leftover bits refine band energies.
type EnergyFinalTracer interface {
	TraceEnergyFinal(band int, channel int, energy float64)
}

// NoopTracer is a no-operation tracer that does nothing.
// This is the default tracer to ensure zero overhead in production.
type NoopTracer struct{}

// TraceHeader implements Tracer with no operation.
func (t *NoopTracer) TraceHeader(frameSize, channels, lm, intra, transient int) {}

// TraceEnergy implements Tracer with no operation.
func (t *NoopTracer) TraceEnergy(band int, coarse, fine, total float64) {}

// TraceAllocation implements Tracer with no operation.
func (t *NoopTracer) TraceAllocation(band, bits, k int) {}

// TracePVQ implements Tracer with no operation.
func (t *NoopTracer) TracePVQ(band int, index uint32, k, n int, pulses []int) {}

// TraceCoeffs implements Tracer with no operation.
func (t *NoopTracer) TraceCoeffs(band int, coeffs []float64) {}

// TraceSynthesis implements Tracer with no operation.
func (t *NoopTracer) TraceSynthesis(stage string, samples []float64) {}

// LogTracer implements Tracer by writing formatted output to an io.Writer.
// Output format: [CELT:stage] key=value key=value ...
// Arrays are truncated to first 8 values with "..." suffix.
type LogTracer struct {
	W io.Writer // Output destination
}

// TraceHeader logs frame header with format:
// [CELT:header] frameSize=960 channels=2 lm=3 intra=0 transient=0
func (t *LogTracer) TraceHeader(frameSize, channels, lm, intra, transient int) {
	fmt.Fprintf(t.W, "[CELT:header] frameSize=%d channels=%d lm=%d intra=%d transient=%d\n",
		frameSize, channels, lm, intra, transient)
}

// TraceEnergy logs per-band energy with format:
// [CELT:energy] band=0 coarse=-12.30 fine=0.20 total=-12.10
func (t *LogTracer) TraceEnergy(band int, coarse, fine, total float64) {
	fmt.Fprintf(t.W, "[CELT:energy] band=%d coarse=%.4f fine=%.4f total=%.4f\n",
		band, coarse, fine, total)
}

// TraceAllocation logs bit allocation with format:
// [CELT:alloc] band=0 bits=24 k=3
func (t *LogTracer) TraceAllocation(band, bits, k int) {
	fmt.Fprintf(t.W, "[CELT:alloc] band=%d bits=%d k=%d\n", band, bits, k)
}

// TracePVQ logs PVQ decode with format:
// [CELT:pvq] band=0 index=1234 k=3 n=8 pulses=[1,-1,0,1,0,0,0,0]
func (t *LogTracer) TracePVQ(band int, index uint32, k, n int, pulses []int) {
	pStr := formatIntSlice(pulses, 8)
	fmt.Fprintf(t.W, "[CELT:pvq] band=%d index=%d k=%d n=%d pulses=%s\n",
		band, index, k, n, pStr)
}

// TraceCoeffs logs denormalized coefficients with format:
// [CELT:coeffs] band=0 coeffs=[0.12,-0.08,0.03,0.15,-0.02,0.01,-0.04,0.07...]
func (t *LogTracer) TraceCoeffs(band int, coeffs []float64) {
	cStr := formatFloatSlice(coeffs, 8)
	fmt.Fprintf(t.W, "[CELT:coeffs] band=%d coeffs=%s\n", band, cStr)
}

// TraceSynthesis logs synthesis stage with format:
// [CELT:synthesis] stage=imdct samples=[0.001,-0.002,0.003...]
func (t *LogTracer) TraceSynthesis(stage string, samples []float64) {
	sStr := formatFloatSlice(samples, 8)
	fmt.Fprintf(t.W, "[CELT:synthesis] stage=%s samples=%s\n", stage, sStr)
}

// TraceRange logs range decoder state with format:
// [stage] rng=0x%08X tell=%d tell_frac=%d
func (t *LogTracer) TraceRange(stage string, rng uint32, tell, tellFrac int) {
	fmt.Fprintf(t.W, "[%s] rng=0x%08X tell=%d tell_frac=%d\n", stage, rng, tell, tellFrac)
}

// formatIntSlice formats an int slice for tracing.
// Truncates to maxLen elements with "..." suffix if longer.
func formatIntSlice(v []int, maxLen int) string {
	if len(v) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteByte('[')

	n := len(v)
	truncated := false
	if n > maxLen {
		n = maxLen
		truncated = true
	}

	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", v[i])
	}

	if truncated {
		sb.WriteString("...")
	}
	sb.WriteByte(']')

	return sb.String()
}

// formatFloatSlice formats a float64 slice for tracing.
// Truncates to maxLen elements with "..." suffix if longer.
// Uses 4 decimal places for readability.
func formatFloatSlice(v []float64, maxLen int) string {
	if len(v) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteByte('[')

	n := len(v)
	truncated := false
	if n > maxLen {
		n = maxLen
		truncated = true
	}

	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%.4f", v[i])
	}

	if truncated {
		sb.WriteString("...")
	}
	sb.WriteByte(']')

	return sb.String()
}

// DefaultTracer is the global tracer used by the CELT decoder.
// Set to NoopTracer by default for zero overhead.
// Use SetTracer to enable tracing for debugging.
var DefaultTracer Tracer = &NoopTracer{}

// SetTracer sets the global tracer for CELT decoding.
// Pass &NoopTracer{} to disable tracing (default).
// Pass &LogTracer{W: os.Stderr} to enable trace logging.
//
// Example:
//
//	var buf bytes.Buffer
//	celt.SetTracer(&celt.LogTracer{W: &buf})
//	defer celt.SetTracer(&celt.NoopTracer{})
//	// ... decode frames ...
//	fmt.Println(buf.String())
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

// boolToInt converts a boolean to int (0 or 1) for trace output.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
