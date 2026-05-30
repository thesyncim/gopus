package celt

// perModeTables carries the per-mode CELT tables for a non-standard Opus Custom
// mode whose band layout differs from the static 21-band 48 kHz tables. It is a
// plain data carrier (no neural / fixed-point code), so it is safe to define in
// the default build; it is only ever populated by EnablePerModeTables, which is
// called exclusively from the gopus_custom celt<->custom plumbing for a
// non-standard, non-family custom mode (e.g. 48000/640, nbEBands=19).
//
// Every field mirrors the equivalent libopus CELTMode member that the decode
// data plane indexes by band:
//
//	eBands      -> mode->eBands       (NbEBands+1 band edges, MDCT bins at base)
//	eBandWidths -> eBands[i+1]-eBands[i]
//	logN        -> mode->logN
//	bandAlloc   -> mode->allocVectors (11 x NbEBands)
//	cacheCaps   -> mode->cache.caps   ((maxLM+1)*2*NbEBands)
//	cacheIndex  -> mode->cache.index
//	cacheBits   -> mode->cache.bits
//	scaleBase   -> mode->shortMdctSize (band-bin scale base; M = frameSize/scaleBase)
//
// Reference: libopus celt/modes.h CELTMode, celt/celt_decoder.c
// celt_decode_with_ec() with a custom CELTMode.
type perModeTables struct {
	nbEBands    int
	scaleBase   int
	eBands      []int
	eBandWidths []int
	logN        []int
	bandAlloc   [][]int
	cacheCaps   []uint8
	cacheIndex  []int16
	cacheBits   []uint8
}

// EnablePerModeTables installs the per-mode CELT tables for a non-standard Opus
// Custom mode whose band layout differs from the static 21-band 48 kHz tables.
// It must be called only for a mode that is neither standard nor in the
// Fs==400*shortMdctSize family; the standard, family, hybrid and QEXT paths
// leave perMode nil and stay byte-identical.
//
// eBands has length nbEBands+1; bandAlloc is the 11 x nbEBands allocation matrix
// (row-major in allocVectors); cacheCaps is (maxLM+1)*2*nbEBands; cacheIndex /
// cacheBits are the pulse cache. scaleBase is the mode short-MDCT size.
func (d *Decoder) EnablePerModeTables(nbEBands, scaleBase int, eBands []int16, logN []int16, allocVectors []uint8, cacheIndex []int16, cacheBits, cacheCaps []uint8) {
	if nbEBands <= 0 || len(eBands) < nbEBands+1 {
		return
	}
	edges := make([]int, nbEBands+1)
	for i := range edges {
		edges[i] = int(eBands[i])
	}
	widths := make([]int, nbEBands)
	for i := 0; i < nbEBands; i++ {
		widths[i] = edges[i+1] - edges[i]
	}
	logNi := make([]int, nbEBands)
	for i := 0; i < nbEBands && i < len(logN); i++ {
		logNi[i] = int(logN[i])
	}
	alloc := make([][]int, 11)
	for r := 0; r < 11; r++ {
		row := make([]int, nbEBands)
		for j := 0; j < nbEBands; j++ {
			idx := r*nbEBands + j
			if idx < len(allocVectors) {
				row[j] = int(allocVectors[idx])
			}
		}
		alloc[r] = row
	}
	d.perMode = &perModeTables{
		nbEBands:    nbEBands,
		scaleBase:   scaleBase,
		eBands:      edges,
		eBandWidths: widths,
		logN:        logNi,
		bandAlloc:   alloc,
		cacheCaps:   cacheCaps,
		cacheIndex:  cacheIndex,
		cacheBits:   cacheBits,
	}
}

// modeNbEBands returns the active band count: the per-mode table's nbEBands when
// a per-mode custom layout is installed, otherwise the static MaxBands.
func (d *Decoder) modeNbEBands() int {
	if d.perMode != nil {
		return d.perMode.nbEBands
	}
	return MaxBands
}

// modeEdges returns the active band-edge slice (length modeNbEBands()+1).
func (d *Decoder) modeEdges() []int {
	if d.perMode != nil {
		return d.perMode.eBands
	}
	return EBands[:]
}
