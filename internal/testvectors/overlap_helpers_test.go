package testvectors

// overlapWrite replaces the previous frame's tail with the current frame's
// overlap region and writes the non-overlapped samples contiguously.
func overlapWrite(output, frame []float64, frameIndex, frameSize, overlap int) {
	start := frameIndex * frameSize
	if frameIndex == 0 {
		if len(output) >= frameSize && len(frame) >= frameSize {
			copy(output[:frameSize], frame[:frameSize])
		}
		return
	}

	overlapStart := start - overlap
	if overlapStart < 0 {
		overlapStart = 0
	}
	if overlap > 0 && overlapStart+overlap <= len(output) && len(frame) >= overlap {
		copy(output[overlapStart:overlapStart+overlap], frame[:overlap])
	}
	if start < len(output) && len(frame) >= frameSize {
		copy(output[start:], frame[overlap:frameSize])
	}
}
