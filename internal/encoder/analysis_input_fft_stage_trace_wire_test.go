//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

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
		phaseBins       = 239
		phaseWords      = 10
	)
	gastBytes := 4 + headerWords*4 + sourceHashBytes + metadataWords*4 + floatWords*4
	data := make([]byte, gastBytes+4+5*4+phaseBins*phaseWords*4)
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
	gaph := data[gastBytes:]
	copy(gaph, "GAPH")
	putPhaseWord := func(offset int, value uint32) {
		binary.LittleEndian.PutUint32(gaph[offset:], value)
	}
	putPhaseWord(4, 1)          // version
	putPhaseWord(8, 0)          // selected frame
	putPhaseWord(12, phaseBins) // actual calls
	putPhaseWord(16, phaseBins) // stored calls
	putPhaseWord(20, 0)         // overflow
	for i := 0; i < phaseBins; i++ {
		putPhaseWord(24+i*phaseWords*4, uint32(i+1)) // ordered bin
	}
	return data
}

func TestAnalysisInputFFTStageTraceParserRejectsMalformedWire(t *testing.T) {
	valid := validAnalysisStageTraceWireFixture()
	if _, phase, err := parseLibopusAnalysisStageTrace(valid); err != nil {
		t.Fatalf("valid GAST/GAPH fixture rejected: %v", err)
	} else if len(phase.records) != 239 {
		t.Fatalf("valid GAPH records=%d want 239", len(phase.records))
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
		{
			name: "wrong phase magic",
			mutate: func(data []byte) []byte {
				copy(data[analysisGASTPayloadBytes():], "GATE")
				return data
			},
		},
		{
			name: "unsupported phase version",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+4:], 2)
				return data
			},
		},
		{
			name: "phase call count mismatch",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+12:], 238)
				return data
			},
		},
		{
			name: "phase frame mismatch",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+8:], 1)
				return data
			},
		},
		{
			name: "phase stored count mismatch",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+16:], 238)
				return data
			},
		},
		{
			name: "phase overflow",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+20:], 1)
				return data
			},
		},
		{
			name: "phase bin order",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+24:], 2)
				return data
			},
		},
		{
			name: "non-finite phase value",
			mutate: func(data []byte) []byte {
				// Row word 5 is the first scaled-angle field after the bin and four inputs.
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+24+5*4:], 0x7fc00000)
				return data
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if _, _, err := parseLibopusAnalysisStageTrace(test.mutate(data)); err == nil {
				t.Fatal("malformed GAST payload was accepted")
			}
		})
	}
}

func analysisGASTPayloadBytes() int {
	const (
		fixedHeaderBytes = 7*4 + 64 + 21*4
		floatArrayWords  = 480 + 2*960 + 3 + 1
	)
	return 4 + fixedHeaderBytes + 4*floatArrayWords
}
