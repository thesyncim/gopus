//go:build gopus_fixed_point

package multistream

func projectionShortOracleBits(enc *Encoder, stream, index int, _ float32) uint32 {
	return uint32(enc.projectionShortResScratch[stream][index])
}
