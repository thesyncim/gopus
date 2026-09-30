//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"encoding/binary"
	"testing"
)

func validAnalysisStageTraceWireFixture() []byte {
	const (
		headerWords     = 7
		sourceHashBytes = 64
		metadataWords   = 21
		floatWords      = 480 + 2*960 + 3 + 1
	)
	data := make([]byte, 4+headerWords*4+sourceHashBytes+metadataWords*4+floatWords*4)
	copy(data, "GAST")
	putWord := func(offset int, value uint32) {
		binary.LittleEndian.PutUint32(data[offset:], value)
	}
	putWord(4, 1)   // version
	putWord(8, 0)   // selected frame
	putWord(12, 50) // run_analysis calls
	putWord(16, 1)  // selected tonality calls
	putWord(20, 0)  // overflow
	putWord(24, 15) // captured stages
	putWord(28, sourceHashBytes)
	for i := 0; i < sourceHashBytes; i++ {
		data[32+i] = 'a'
	}
	return data
}

func TestAnalysisInputFFTStageTraceParserRejectsMalformedWire(t *testing.T) {
	valid := validAnalysisStageTraceWireFixture()
	if _, err := parseLibopusAnalysisStageTrace(valid); err != nil {
		t.Fatalf("valid GAST fixture rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "truncated payload",
			mutate: func(data []byte) []byte {
				return data[:len(data)-1]
			},
		},
		{
			name: "unsupported version",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[4:], 2)
				return data
			},
		},
		{
			name: "wrong source hash length",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[28:], 63)
				return data
			},
		},
		{
			name: "malformed source hash",
			mutate: func(data []byte) []byte {
				data[32] = 'z'
				return data
			},
		},
		{
			name: "trailing bytes",
			mutate: func(data []byte) []byte {
				return append(data, 0)
			},
		},
		{
			name: "wrong magic",
			mutate: func(data []byte) []byte {
				copy(data, "GANO")
				return data
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if _, err := parseLibopusAnalysisStageTrace(test.mutate(data)); err == nil {
				t.Fatal("malformed GAST payload was accepted")
			}
		})
	}
}
