package celt

import (
	"fmt"
	"os"
)

// celtETrace dumps the encoder's persistent per-band energy memory after a frame,
// gated on GOPUS_CELT_ETRACE (treated as a file path; the matching libopus trace
// appends to GOPUS_CELT_ETRACE_C). Used to bisect the post-silence recovery-frame
// divergence against the env-gated libopus celt_encoder.c trace. Trace-only.
func (e *Encoder) celtETrace(silence bool, isTransient, transientGotDisabled bool, nbBands, codedChannels int) {
	path := os.Getenv("GOPUS_CELT_ETRACE")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	si := 0
	if silence {
		si = 1
	}
	ti := 0
	if isTransient {
		ti = 1
	}
	tg := 0
	if transientGotDisabled {
		tg = 1
	}
	stride := e.predStride()
	n := nbBands
	fmt.Fprintf(f, "GOPUS_ETRACE silence=%d isTransient=%d tgd=%d consec=%d C=%d nbE=%d stride=%d delayedIntra=%.6f lastCodedBands=%d spread=%d tapset=%d tonalAvg=%d intensity=%d specAvg=%.6f pfPeriod=%d pfGain=%.6f rng=%08x\n",
		si, ti, tg, e.consecTransient, codedChannels, n, stride, float64(e.delayedIntra),
		e.lastCodedBands, e.spreadDecision, e.tapsetDecision, e.tonalAverage, e.intensity, float64(e.specAvg), e.prefilterPeriod, float64(e.prefilterGain), e.rng)
	dump := func(label string, src []celtGLog) {
		fmt.Fprintf(f, "  %s:", label)
		for c := 0; c < codedChannels; c++ {
			for b := 0; b < n; b++ {
				idx := c*stride + b
				if idx < len(src) {
					fmt.Fprintf(f, " %.6f", float64(src[idx]))
				}
			}
		}
		fmt.Fprintf(f, "\n")
	}
	dump("oldBandE", e.prevEnergy)
	dump("oldLogE2", e.prevEnergy2)
	dump("enErr   ", e.energyError)
}

// celtETracePacket dumps the emitted packet bytes (trace-only) to compare the
// silent-frame byte stream against the matching libopus compressed-buffer trace.
func (e *Encoder) celtETracePacket(pkt []byte) {
	path := os.Getenv("GOPUS_CELT_ETRACE")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "  PKT(%d):", len(pkt))
	for _, b := range pkt {
		fmt.Fprintf(f, " %02x", b)
	}
	fmt.Fprintf(f, "\n")
}
