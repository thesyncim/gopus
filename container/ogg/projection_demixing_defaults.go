package ogg

import "encoding/binary"

type projectionDemixingDefault struct {
	streams uint8
	coupled uint8
	gain    int16
	matrix  []byte
}

// defaultProjectionDemixingMatrix returns libopus 1.6.1 projection defaults
// for mapping family 3 (RFC 8486) when the (channels,streams,coupled) tuple
// matches a valid ambisonics order+optional non-diegetic stereo layout.
func defaultProjectionDemixingMatrix(channels, streams, coupled uint8) ([]byte, int16, bool) {
	def, ok := projectionDemixingDefaults[channels]
	if !ok {
		return nil, 0, false
	}
	if def.streams != streams || def.coupled != coupled {
		return nil, 0, false
	}
	matrix := make([]byte, len(def.matrix))
	copy(matrix, def.matrix)
	return matrix, def.gain, true
}

func identityDemixingMatrix(channels, streams, coupled uint8) []byte {
	cols := int(streams + coupled)
	rows := int(channels)
	matrix := make([]byte, 2*rows*cols)

	for col := 0; col < cols; col++ {
		for row := 0; row < rows; row++ {
			var v uint16
			if row == col {
				v = 32767 // Q15 identity coefficient
			}
			offset := 2 * (col*rows + row)
			binary.LittleEndian.PutUint16(matrix[offset:offset+2], v)
		}
	}

	return matrix
}
