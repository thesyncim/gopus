package dred

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

const ActivityHistorySize = 4 * MaxFrames

const dredLaplaceFTBits = 15

// UpdateActivityHistory mirrors the libopus encoder-side DRED activity-memory
// rollover at 2.5 ms resolution.
func UpdateActivityHistory(dst *[ActivityHistorySize]byte, frameSize, sampleRate int, active bool) {
	if dst == nil || sampleRate <= 0 || frameSize <= 0 {
		return
	}
	n := frameSize * 400 / sampleRate
	if n <= 0 {
		return
	}
	fill := byte(0)
	if active {
		fill = 1
	}
	if n >= len(dst) {
		for i := range dst {
			dst[i] = fill
		}
		return
	}
	copy(dst[n:], dst[:len(dst)-n])
	for i := 0; i < n; i++ {
		dst[i] = fill
	}
}

func dredVoiceActive(activity []byte, offset int) bool {
	if offset < 0 {
		return false
	}
	start := 8 * offset
	if start >= len(activity) {
		return false
	}
	end := start + 16
	if end > len(activity) {
		end = len(activity)
	}
	for i := start; i < end; i++ {
		if activity[i] == 1 {
			return true
		}
	}
	return false
}

func encodeLaplaceP0(enc *rangecoding.Encoder, value int, p0, decay uint16) {
	signICDF := [3]uint16{32768 - p0, (32768 - p0) / 2, 0}
	symbol := 0
	if value > 0 {
		symbol = 1
	} else if value < 0 {
		symbol = 2
	}
	enc.EncodeICDF16(symbol, signICDF[:], dredLaplaceFTBits)

	if value < 0 {
		value = -value
	}
	if value == 0 {
		return
	}

	icdf := [8]uint16{}
	icdf[0] = uint16(maxInt(7, int(decay)))
	for i := 1; i < 7; i++ {
		icdf[i] = uint16(maxInt(7-i, int((uint32(icdf[i-1])*uint32(decay))>>15)))
	}

	value--
	for {
		symbol := value
		if symbol > 7 {
			symbol = 7
		}
		enc.EncodeICDF16(symbol, icdf[:], dredLaplaceFTBits)
		value -= 7
		if value < 0 {
			return
		}
	}
}

func tanhApprox(x float32) float32 {
	const (
		n0 = 952.52801514
		n1 = 96.39235687
		n2 = 0.60863042
		d0 = 952.72399902
		d1 = 413.36801147
		d2 = 11.88600922
	)
	x2 := x * x
	num := ((n2*x2 + n1) * x2) + n0
	den := ((d2*x2 + d1) * x2) + d0
	y := num * x / den
	if y < -1 {
		return -1
	}
	if y > 1 {
		return 1
	}
	return y
}

func encodeDREDLatents(enc *rangecoding.Encoder, x []float32, scale, dzone, rTable, p0Table []uint8) {
	for i := range x {
		if rTable[i] == 0 || p0Table[i] == 255 {
			continue
		}
		delta := float32(dzone[i]) * (1.0 / 256.0)
		xq := x[i] * float32(scale[i]) * (1.0 / 256.0)
		deadzone := tanhApprox(xq / (delta + 0.1))
		xq -= delta * deadzone
		q := int(math.Floor(0.5 + float64(xq)))
		encodeLaplaceP0(enc, q, uint16(p0Table[i])<<7, uint16(rTable[i])<<7)
	}
}

// EncodePayload mirrors libopus dred_encode_silk_frame() for a caller-owned
// DRED history window. dst must not include the temporary experimental prefix.
// It returns the encoded payload length in bytes, or 0 when libopus would
// suppress DRED emission for the provided window.
func EncodePayload(dst []byte, maxChunks, q0, dQ, qmax int, stateBuffer, latentsBuffer []float32, latentsFill, dredOffset, latentOffset int, lastExtraDREDOffset *int, activity []byte) int {
	if len(dst) == 0 || latentsFill <= 0 {
		return 0
	}

	extraDREDOffset := 0
	delayedDRED := false
	if len(activity) > 0 && activity[0] != 0 && lastExtraDREDOffset != nil && *lastExtraDREDOffset > 0 {
		latentOffset = *lastExtraDREDOffset
		delayedDRED = true
		*lastExtraDREDOffset = 0
	}
	for latentOffset < latentsFill && !dredVoiceActive(activity, latentOffset) {
		latentOffset++
		extraDREDOffset++
	}
	if !delayedDRED && lastExtraDREDOffset != nil {
		*lastExtraDREDOffset = extraDREDOffset
	}

	stateIndex := latentOffset * StateDim
	if stateIndex < 0 || stateIndex+StateDim > len(stateBuffer) {
		return 0
	}

	var enc rangecoding.Encoder
	enc.Init(dst)
	enc.EncodeUniform(uint32(q0), 16)
	enc.EncodeUniform(uint32(dQ), 8)

	totalOffset := 16 - (dredOffset - extraDREDOffset*8)
	if totalOffset < 0 {
		return 0
	}
	if totalOffset > 31 {
		enc.EncodeUniform(1, 2)
		enc.EncodeUniform(uint32(totalOffset>>5), 256)
		enc.EncodeUniform(uint32(totalOffset&31), 32)
	} else {
		enc.EncodeUniform(0, 2)
		enc.EncodeUniform(uint32(totalOffset), 32)
	}

	if q0 < 14 && dQ > 0 {
		nvals := 15 - (q0 + 1)
		if qmax >= 15 {
			enc.Encode(0, uint32(nvals), uint32(2*nvals))
		} else {
			fl := nvals + qmax - (q0 + 1)
			fh := nvals + qmax - q0
			enc.Encode(uint32(fl), uint32(fh), uint32(2*nvals))
		}
	}

	stateOffset := q0 * StateDim
	encodeDREDLatents(
		&enc,
		stateBuffer[stateIndex:stateIndex+StateDim],
		dredStateQuantScalesQ8[stateOffset:stateOffset+StateDim],
		dredStateDeadZoneQ8[stateOffset:stateOffset+StateDim],
		dredStateRQ8[stateOffset:stateOffset+StateDim],
		dredStateP0Q8[stateOffset:stateOffset+StateDim],
	)
	if enc.Tell() > 8*len(dst) {
		return 0
	}

	backup := enc
	dredEncoded := 0
	prevActive := false
	limit := minInt(2*maxChunks, latentsFill-latentOffset-1)
	header := Header{Q0: q0, DQ: dQ, QMax: qmax}
	for i := 0; i < limit; i += 2 {
		quant := header.QuantizerLevel(i / 2)
		latentIndex := (i + latentOffset) * LatentDim
		if latentIndex < 0 || latentIndex+LatentDim > len(latentsBuffer) {
			return 0
		}
		offset := quant * LatentDim
		encodeDREDLatents(
			&enc,
			latentsBuffer[latentIndex:latentIndex+LatentDim],
			dredLatentQuantScalesQ8[offset:offset+LatentDim],
			dredLatentDeadZoneQ8[offset:offset+LatentDim],
			dredLatentRQ8[offset:offset+LatentDim],
			dredLatentP0Q8[offset:offset+LatentDim],
		)
		if enc.Tell() > 8*len(dst) {
			if i == 0 {
				return 0
			}
			break
		}
		active := dredVoiceActive(activity, i+latentOffset)
		if active || prevActive {
			backup = enc
			dredEncoded = i + 2
		}
		prevActive = active
	}

	if dredEncoded == 0 || (dredEncoded <= 2 && extraDREDOffset > 0) {
		return 0
	}

	enc = backup
	used := (enc.Tell() + 7) / 8
	enc.Shrink(uint32(used))
	return len(enc.Done())
}

// EncodeExperimentalPayload mirrors the current libopus temporary DRED payload
// framing by prepending the experimental header in front of EncodePayload().
func EncodeExperimentalPayload(dst []byte, maxChunks, q0, dQ, qmax int, stateBuffer, latentsBuffer []float32, latentsFill, dredOffset, latentOffset int, lastExtraDREDOffset *int, activity []byte) int {
	if len(dst) <= ExperimentalHeaderBytes {
		return 0
	}
	dst[0] = 'D'
	dst[1] = ExperimentalVersion
	n := EncodePayload(dst[ExperimentalHeaderBytes:], maxChunks, q0, dQ, qmax, stateBuffer, latentsBuffer, latentsFill, dredOffset, latentOffset, lastExtraDREDOffset, activity)
	if n == 0 {
		return 0
	}
	return ExperimentalHeaderBytes + n
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
