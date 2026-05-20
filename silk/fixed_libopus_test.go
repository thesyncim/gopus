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
)

type libopusSILKFixedRecord struct {
	value int32
	shift uint32
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
