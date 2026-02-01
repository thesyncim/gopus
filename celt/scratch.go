package celt

func ensureFloat64Slice(buf *[]float64, n int) []float64 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]float64, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureIntSlice(buf *[]int, n int) []int {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]int, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureByteSlice(buf *[]byte, n int) []byte {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]byte, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureComplexSlice(buf *[]complex128, n int) []complex128 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]complex128, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureComplex64Slice(buf *[]complex64, n int) []complex64 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]complex64, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureFloat32Slice(buf *[]float32, n int) []float32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]float32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureUint32Slice(buf *[]uint32, n int) []uint32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]uint32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureKissCpxSlice(buf *[]kissCpx, n int) []kissCpx {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]kissCpx, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

type bandDecodeScratch struct {
	left     []float64
	right    []float64
	collapse []byte
	norm     []float64
	lowband  []float64

	// Pre-allocated buffers for DecodeBands hot path (eliminates per-frame allocations)
	coeffs       []float64   // MDCT coefficients output buffer (size: frameSize or 2*frameSize for stereo)
	bandVectors  [][]float64 // Per-band decoded vectors for folding (size: MaxBands)
	bandVectorsL [][]float64 // Left channel band vectors for stereo (size: MaxBands)
	bandVectorsR [][]float64 // Right channel band vectors for stereo (size: MaxBands)

	// Individual band vector storage - flat storage to avoid slice-of-slice allocations
	// Each band can have up to maxBandWidth bins (scaled band 20 at 20ms = 176 bins)
	bandStorage  [MaxBands][]float64 // Pre-allocated storage for each band vector
	bandStorageL [MaxBands][]float64 // Left channel band storage
	bandStorageR [MaxBands][]float64 // Right channel band storage

	// Scratch buffers for PVQ/folding operations
	pvqPulses  []int     // Pulse vector from CWRS decode
	pvqFloat   []float64 // Float conversion of pulses
	pvqNorm    []float64 // Normalized PVQ vector
	foldResult []float64 // Folded band result
	cwrsU      []uint32  // CWRS u-row scratch buffer

	// Scratch buffers for Hadamard interleave/deinterleave (eliminates per-call allocations)
	hadamardTmp []float64 // Temporary buffer for Hadamard transforms

	// Scratch buffer for FFT fstride computation (eliminates per-call allocations)
	fftFstride []int // fstride array for FFT butterfly stages
}

// maxBandWidth is the maximum width of any single band (band 20 at LM=3 = 176 bins).
const maxBandWidth = 176

// maxPVQPulses is the maximum number of pulses that can be decoded.
// Conservative estimate based on maximum bit allocation.
const maxPVQPulses = 256

// ensureCoeffs returns a pre-allocated coefficients buffer of the requested size.
func (s *bandDecodeScratch) ensureCoeffs(n int) []float64 {
	return ensureFloat64Slice(&s.coeffs, n)
}

// ensureBandVectors returns a pre-allocated slice of band vector pointers.
// The returned slice has length nbBands, with each element pointing to
// pre-allocated storage in bandStorage.
func (s *bandDecodeScratch) ensureBandVectors(nbBands int) [][]float64 {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	// Ensure the slice of slices has enough capacity
	if cap(s.bandVectors) < nbBands {
		s.bandVectors = make([][]float64, nbBands)
	} else {
		s.bandVectors = s.bandVectors[:nbBands]
	}
	// Clear the slice references (they will be set per-band)
	for i := 0; i < nbBands; i++ {
		s.bandVectors[i] = nil
	}
	return s.bandVectors
}

// ensureBandVectorsStereo returns pre-allocated slices for left and right channel band vectors.
func (s *bandDecodeScratch) ensureBandVectorsStereo(nbBands int) (left, right [][]float64) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	// Ensure left channel slice
	if cap(s.bandVectorsL) < nbBands {
		s.bandVectorsL = make([][]float64, nbBands)
	} else {
		s.bandVectorsL = s.bandVectorsL[:nbBands]
	}
	// Ensure right channel slice
	if cap(s.bandVectorsR) < nbBands {
		s.bandVectorsR = make([][]float64, nbBands)
	} else {
		s.bandVectorsR = s.bandVectorsR[:nbBands]
	}
	// Clear the slice references
	for i := 0; i < nbBands; i++ {
		s.bandVectorsL[i] = nil
		s.bandVectorsR[i] = nil
	}
	return s.bandVectorsL, s.bandVectorsR
}

// getBandStorage returns a pre-allocated buffer for storing a band vector.
// The buffer is sized to fit n elements.
func (s *bandDecodeScratch) getBandStorage(band, n int) []float64 {
	if band < 0 || band >= MaxBands || n <= 0 {
		return nil
	}
	return ensureFloat64Slice(&s.bandStorage[band], n)
}

// getBandStorageL returns a pre-allocated buffer for left channel band vector.
func (s *bandDecodeScratch) getBandStorageL(band, n int) []float64 {
	if band < 0 || band >= MaxBands || n <= 0 {
		return nil
	}
	return ensureFloat64Slice(&s.bandStorageL[band], n)
}

// getBandStorageR returns a pre-allocated buffer for right channel band vector.
func (s *bandDecodeScratch) getBandStorageR(band, n int) []float64 {
	if band < 0 || band >= MaxBands || n <= 0 {
		return nil
	}
	return ensureFloat64Slice(&s.bandStorageR[band], n)
}

// ensurePVQPulses returns a pre-allocated buffer for PVQ pulse vector.
func (s *bandDecodeScratch) ensurePVQPulses(n int) []int {
	return ensureIntSlice(&s.pvqPulses, n)
}

// ensurePVQFloat returns a pre-allocated buffer for float conversion.
func (s *bandDecodeScratch) ensurePVQFloat(n int) []float64 {
	return ensureFloat64Slice(&s.pvqFloat, n)
}

// ensurePVQNorm returns a pre-allocated buffer for normalized vector.
func (s *bandDecodeScratch) ensurePVQNorm(n int) []float64 {
	return ensureFloat64Slice(&s.pvqNorm, n)
}

// ensureFoldResult returns a pre-allocated buffer for fold result.
func (s *bandDecodeScratch) ensureFoldResult(n int) []float64 {
	return ensureFloat64Slice(&s.foldResult, n)
}

// ensureCWRSU returns a pre-allocated buffer for CWRS u-row.
func (s *bandDecodeScratch) ensureCWRSU(n int) []uint32 {
	return ensureUint32Slice(&s.cwrsU, n)
}

// ensureHadamardTmp returns a pre-allocated buffer for Hadamard transforms.
func (s *bandDecodeScratch) ensureHadamardTmp(n int) []float64 {
	return ensureFloat64Slice(&s.hadamardTmp, n)
}

// ensureFFTFstride returns a pre-allocated buffer for FFT fstride computation.
func (s *bandDecodeScratch) ensureFFTFstride(n int) []int {
	return ensureIntSlice(&s.fftFstride, n)
}

type imdctScratch struct {
	fftIn  []complex128
	fftOut []complex128
	buf    []float64
}

// imdctScratchF32 holds scratch buffers for float32 IMDCT to avoid per-call allocations.
type imdctScratchF32 struct {
	fftIn  []complex64
	fftOut []complex64
	fftTmp []kissCpx
	buf    []float32
	out    []float32
}
