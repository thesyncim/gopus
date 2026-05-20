package silk

import (
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	fixedTestMinInt32 = int32(-1 << 31)
	fixedTestMaxInt32 = int32(1<<31 - 1)
)

const (
	libopusSILKFixedInputMagic  = "GSFI"
	libopusSILKFixedOutputMagic = "GSFO"

	libopusSILKFixedModeRShiftRound        = uint32(0)
	libopusSILKFixedModeSAT16              = uint32(1)
	libopusSILKFixedModeSAT16RShiftRound10 = uint32(2)
	libopusSILKFixedModeSAT16RShiftRound15 = uint32(3)
	libopusSILKFixedModeLShiftSAT32        = uint32(4)
	libopusSILKFixedModeSMULWB             = uint32(5)
	libopusSILKFixedModeSMLAWB             = uint32(6)
	libopusSILKFixedModeSMULWW             = uint32(7)
	libopusSILKFixedModeSMMUL              = uint32(8)
	libopusSILKFixedModeAddSat32           = uint32(9)
	libopusSILKFixedModeSubSat32           = uint32(10)
	libopusSILKFixedModeDiv32_16           = uint32(11)
	libopusSILKFixedModeDiv32VarQ          = uint32(12)
	libopusSILKFixedModeInverse32VarQ      = uint32(13)
	libopusSILKFixedModeCLZ32              = uint32(14)
	libopusSILKFixedModeRShiftRound64To32  = uint32(15)
)

type libopusSILKFixedRecord struct {
	value int32
	shift uint32
}

type libopusSILKFixedOpRecord struct {
	a int32
	b int32
	c int32
	q uint32
}

var (
	libopusSILKFixedHelperOnce sync.Once
	libopusSILKFixedHelperPath string
	libopusSILKFixedHelperErr  error
)

func buildLibopusSILKFixedHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "silk fixed",
		OutputBase:  "gopus_libopus_silk_fixed",
		SourceFile:  "libopus_silk_fixed_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		RefIncludes: []string{"celt", "silk"},
	})
}

func getLibopusSILKFixedHelperPath() (string, error) {
	libopusSILKFixedHelperOnce.Do(func() {
		libopusSILKFixedHelperPath, libopusSILKFixedHelperErr = buildLibopusSILKFixedHelper()
	})
	if libopusSILKFixedHelperErr != nil {
		return "", libopusSILKFixedHelperErr
	}
	return libopusSILKFixedHelperPath, nil
}

func probeLibopusSILKFixed(mode uint32, records []libopusSILKFixedRecord) ([]int32, error) {
	binPath, err := getLibopusSILKFixedHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		payload.I32(record.value)
		payload.U32(record.shift)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed", libopusSILKFixedOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(4 * count)
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKFixedOps(mode uint32, records []libopusSILKFixedOpRecord) ([]int32, error) {
	binPath, err := getLibopusSILKFixedHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		payload.I32(record.a)
		payload.I32(record.b)
		payload.I32(record.c)
		payload.U32(record.q)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed", libopusSILKFixedOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(4 * count)
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFixedRShiftRoundMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	records := make([]libopusSILKFixedRecord, 0, 512)
	edgeValues := []int32{
		fixedTestMinInt32, fixedTestMinInt32 + 1, -65537, -65536, -65535,
		-32769, -32768, -32767, -17, -16, -15, -3, -2, -1,
		0, 1, 2, 3, 15, 16, 17, 32766, 32767, 32768, 32769,
		65535, 65536, 65537, fixedTestMaxInt32 - 1, fixedTestMaxInt32,
	}
	for shift := uint32(1); shift <= 31; shift++ {
		for _, value := range edgeValues {
			records = append(records, libopusSILKFixedRecord{value: value, shift: shift})
		}
		half := int32(1) << (shift - 1)
		for _, value := range []int32{-half - 1, -half, -half + 1, half - 1, half, half + 1} {
			records = append(records, libopusSILKFixedRecord{value: value, shift: shift})
		}
	}
	want, err := probeLibopusSILKFixed(libopusSILKFixedModeRShiftRound, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed", err)
	}
	for i, record := range records {
		if got := silkRSHIFT_ROUND(record.value, int(record.shift)); got != want[i] {
			t.Fatalf("silkRSHIFT_ROUND(%d,%d)=%d want %d", record.value, record.shift, got, want[i])
		}
	}
}

func TestSILKFixedSaturatingHelpersMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKFixedRecord{
		{value: fixedTestMinInt32}, {value: -1073741825}, {value: -1073741824},
		{value: -33554433}, {value: -33554432}, {value: -33554431},
		{value: -32769}, {value: -32768}, {value: -32767},
		{value: -1}, {value: 0}, {value: 1},
		{value: 32766}, {value: 32767}, {value: 32768},
		{value: 33554431}, {value: 33554432}, {value: 33554433},
		{value: 1073741823}, {value: 1073741824}, {value: 1073741825},
		{value: fixedTestMaxInt32},
	}
	tests := []struct {
		name string
		mode uint32
		got  func(int32) int32
	}{
		{name: "sat16", mode: libopusSILKFixedModeSAT16, got: func(x int32) int32 { return int32(silkSAT16(x)) }},
		{name: "sat16_rshift_round10", mode: libopusSILKFixedModeSAT16RShiftRound10, got: func(x int32) int32 { return int32(sat16RShiftRound10(x)) }},
		{name: "sat16_rshift_round15", mode: libopusSILKFixedModeSAT16RShiftRound15, got: func(x int32) int32 { return int32(sat16RShiftRound15(x)) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusSILKFixed(tc.mode, records)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk fixed", err)
			}
			for i, record := range records {
				if got := tc.got(record.value); got != want[i] {
					t.Fatalf("%s(%d)=%d want %d", tc.name, record.value, got, want[i])
				}
			}
		})
	}
}

func TestSILKFixedLShiftSAT32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	records := make([]libopusSILKFixedRecord, 0, 256)
	edgeValues := []int32{
		fixedTestMinInt32, fixedTestMinInt32 + 1, -1073741825, -1073741824, -1073741823,
		-65537, -65536, -65535, -2, -1, 0, 1, 2,
		65535, 65536, 65537, 1073741823, 1073741824, 1073741825,
		fixedTestMaxInt32 - 1, fixedTestMaxInt32,
	}
	for shift := uint32(0); shift <= 31; shift++ {
		for _, value := range edgeValues {
			records = append(records, libopusSILKFixedRecord{value: value, shift: shift})
		}
	}
	want, err := probeLibopusSILKFixed(libopusSILKFixedModeLShiftSAT32, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed", err)
	}
	for i, record := range records {
		if got := silkLShiftSAT32(record.value, int(record.shift)); got != want[i] {
			t.Fatalf("silkLShiftSAT32(%d,%d)=%d want %d", record.value, record.shift, got, want[i])
		}
	}
}

func TestSILKFixedMultiplyAndSaturatingOpsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	mulValues := []int32{
		-1073741824, -8388608, -65537, -65536, -32769, -32768, -32767,
		-2, -1, 0, 1, 2, 32766, 32767, 32768, 32769, 65535, 65536,
		8388607, 1073741823,
	}
	mulRecords := make([]libopusSILKFixedOpRecord, 0, len(mulValues)*len(mulValues))
	for _, a := range mulValues {
		for _, b := range mulValues {
			mulRecords = append(mulRecords, libopusSILKFixedOpRecord{a: a, b: b})
		}
	}

	smlawbRecords := make([]libopusSILKFixedOpRecord, 0, 512)
	accValues := []int32{-268435456, -65536, -1, 0, 1, 65536, 268435456}
	cValues := []int32{-32768, -32767, -1, 0, 1, 32766, 32767, 32768, 65535}
	for _, a := range accValues {
		for _, b := range mulValues {
			for _, c := range cValues {
				smlawbRecords = append(smlawbRecords, libopusSILKFixedOpRecord{a: a, b: b, c: c})
			}
		}
	}

	satRecords := []libopusSILKFixedOpRecord{
		{a: fixedTestMinInt32, b: -1},
		{a: fixedTestMinInt32, b: 0},
		{a: fixedTestMinInt32, b: 1},
		{a: fixedTestMinInt32 + 1, b: -2},
		{a: -1073741824, b: -1073741824},
		{a: -65536, b: 65535},
		{a: -1, b: -1},
		{a: -1, b: 1},
		{a: 0, b: fixedTestMinInt32},
		{a: 0, b: fixedTestMaxInt32},
		{a: 1, b: -1},
		{a: 1, b: 1},
		{a: 65535, b: -65536},
		{a: 1073741823, b: 1073741824},
		{a: fixedTestMaxInt32 - 1, b: 2},
		{a: fixedTestMaxInt32, b: -1},
		{a: fixedTestMaxInt32, b: 0},
		{a: fixedTestMaxInt32, b: 1},
	}

	tests := []struct {
		name    string
		mode    uint32
		records []libopusSILKFixedOpRecord
		got     func(libopusSILKFixedOpRecord) int32
	}{
		{name: "smulwb", mode: libopusSILKFixedModeSMULWB, records: mulRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkSMULWB(r.a, r.b)
		}},
		{name: "smlawb", mode: libopusSILKFixedModeSMLAWB, records: smlawbRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkSMLAWB(r.a, r.b, r.c)
		}},
		{name: "smulww", mode: libopusSILKFixedModeSMULWW, records: mulRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkSMULWW(r.a, r.b)
		}},
		{name: "smmul", mode: libopusSILKFixedModeSMMUL, records: mulRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkSMMUL(r.a, r.b)
		}},
		{name: "add_sat32", mode: libopusSILKFixedModeAddSat32, records: satRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkAddSat32(r.a, r.b)
		}},
		{name: "sub_sat32", mode: libopusSILKFixedModeSubSat32, records: satRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkSubSat32(r.a, r.b)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusSILKFixedOps(tc.mode, tc.records)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk fixed", err)
			}
			for i, record := range tc.records {
				if got := tc.got(record); got != want[i] {
					t.Fatalf("%s(%d,%d,%d,q=%d)=%d want %d", tc.name, record.a, record.b, record.c, record.q, got, want[i])
				}
			}
		})
	}
}

func TestSILKFixedDivisionAndCLZOpsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	divValues := []int32{
		-2147483647, -1073741824, -65536, -32769, -32768, -32767,
		-2, -1, 0, 1, 2, 32766, 32767, 32768, 65535, 65536,
		1073741823, fixedTestMaxInt32,
	}
	divisors16 := []int32{-32768, -30000, -2, -1, 1, 2, 30000, 32767}
	div32_16Records := make([]libopusSILKFixedOpRecord, 0, len(divValues)*len(divisors16))
	for _, a := range divValues {
		for _, b := range divisors16 {
			if a == fixedTestMinInt32 && b == -1 {
				continue
			}
			div32_16Records = append(div32_16Records, libopusSILKFixedOpRecord{a: a, b: b})
		}
	}

	varQRecords := make([]libopusSILKFixedOpRecord, 0, 512)
	varQValues := []int32{-1073741824, -16777216, -65536, -1, 1, 65536, 16777216, 1073741823}
	varQShifts := []uint32{0, 8, 13, 16, 24, 30}
	for _, a := range varQValues {
		for _, b := range varQValues {
			for _, q := range varQShifts {
				varQRecords = append(varQRecords, libopusSILKFixedOpRecord{a: a, b: b, q: q})
			}
		}
	}

	inverseRecords := make([]libopusSILKFixedOpRecord, 0, 256)
	inverseValues := []int32{-1073741824, -16777216, -65536, -32768, -1, 1, 32767, 65536, 16777216, 1073741823}
	inverseShifts := []uint32{1, 8, 16, 30, 47}
	for _, a := range inverseValues {
		for _, q := range inverseShifts {
			inverseRecords = append(inverseRecords, libopusSILKFixedOpRecord{a: a, q: q})
		}
	}

	clzRecords := make([]libopusSILKFixedOpRecord, 0, 128)
	for _, a := range []int32{
		fixedTestMinInt32, fixedTestMinInt32 + 1, -1073741824, -1,
		0, 1, 2, 3, 15, 16, 17, 255, 256, 257,
		65535, 65536, 65537, 1073741823, fixedTestMaxInt32,
	} {
		clzRecords = append(clzRecords, libopusSILKFixedOpRecord{a: a})
	}

	tests := []struct {
		name    string
		mode    uint32
		records []libopusSILKFixedOpRecord
		got     func(libopusSILKFixedOpRecord) int32
	}{
		{name: "div32_16", mode: libopusSILKFixedModeDiv32_16, records: div32_16Records, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkDiv32_16(r.a, r.b)
		}},
		{name: "div32_var_q", mode: libopusSILKFixedModeDiv32VarQ, records: varQRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkDiv32VarQ(r.a, r.b, int(r.q))
		}},
		{name: "inverse32_var_q", mode: libopusSILKFixedModeInverse32VarQ, records: inverseRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkInverse32VarQ(r.a, int(r.q))
		}},
		{name: "clz32", mode: libopusSILKFixedModeCLZ32, records: clzRecords, got: func(r libopusSILKFixedOpRecord) int32 {
			return silkCLZ32(r.a)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusSILKFixedOps(tc.mode, tc.records)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk fixed", err)
			}
			for i, record := range tc.records {
				if got := tc.got(record); got != want[i] {
					t.Fatalf("%s(%d,%d,%d,q=%d)=%d want %d", tc.name, record.a, record.b, record.c, record.q, got, want[i])
				}
			}
		})
	}
}

func TestSILKFixedRShiftRound64MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	values := []int64{
		-1 << 50, -1 << 40, -1 << 33, -1 << 32, -1<<31 - 1,
		-65537, -65536, -65535, -3, -2, -1,
		0, 1, 2, 3, 65535, 65536, 65537,
		1<<31 - 1, 1 << 32, 1 << 33, 1 << 40, 1 << 50,
	}
	records := make([]libopusSILKFixedOpRecord, 0, len(values)*48)
	for _, value := range values {
		for shift := uint32(1); shift <= 48; shift++ {
			y := silkRSHIFT_ROUND64(value, int(shift))
			if y < int64(fixedTestMinInt32) || y > int64(fixedTestMaxInt32) {
				continue
			}
			bits := uint64(value)
			records = append(records, libopusSILKFixedOpRecord{
				a: int32(uint32(bits >> 32)),
				b: int32(uint32(bits)),
				q: shift,
			})
		}
	}
	want, err := probeLibopusSILKFixedOps(libopusSILKFixedModeRShiftRound64To32, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed", err)
	}
	for i, record := range records {
		value := int64(uint64(uint32(record.a))<<32 | uint64(uint32(record.b)))
		if got := int32(silkRSHIFT_ROUND64(value, int(record.q))); got != want[i] {
			t.Fatalf("silkRSHIFT_ROUND64(%d,%d)=%d want %d", value, record.q, got, want[i])
		}
	}
}
