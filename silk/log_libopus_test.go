package silk

import (
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKLogInputMagic  = "GSLI"
	libopusSILKLogOutputMagic = "GSLO"

	libopusSILKLogModeLin2Log = uint32(0)
	libopusSILKLogModeLog2Lin = uint32(1)
)

var (
	libopusSILKLogHelperOnce sync.Once
	libopusSILKLogHelperPath string
	libopusSILKLogHelperErr  error
)

func buildLibopusSILKLogHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "silk log",
		OutputBase:  "gopus_libopus_silk_log",
		SourceFile:  "libopus_silk_log_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		RefIncludes: []string{"celt", "silk"},
		RefSources:  []string{"silk/lin2log.c", "silk/log2lin.c"},
	})
}

func getLibopusSILKLogHelperPath() (string, error) {
	libopusSILKLogHelperOnce.Do(func() {
		libopusSILKLogHelperPath, libopusSILKLogHelperErr = buildLibopusSILKLogHelper()
	})
	if libopusSILKLogHelperErr != nil {
		return "", libopusSILKLogHelperErr
	}
	return libopusSILKLogHelperPath, nil
}

func probeLibopusSILKLog(mode uint32, samples []int32) ([]int32, error) {
	binPath, err := getLibopusSILKLogHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLogInputMagic, mode, uint32(len(samples)))
	for _, sample := range samples {
		payload.I32(sample)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk log helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk log", libopusSILKLogOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
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

func TestSILKLin2LogMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []int32{1, 2, 3, 4, 5, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255, 256, 1024, 65535, 65536, 0x7fffffff}
	for shift := 1; shift < 31; shift++ {
		base := int32(1) << shift
		for delta := int32(-2); delta <= 2; delta++ {
			v := base + delta
			if v > 0 {
				samples = append(samples, v)
			}
		}
	}
	want, err := probeLibopusSILKLog(libopusSILKLogModeLin2Log, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk log", err)
	}
	for i, sample := range samples {
		if got := silkLin2Log(sample); got != want[i] {
			t.Fatalf("silkLin2Log(%d)=%d want %d", sample, got, want[i])
		}
	}
}

func TestSILKLog2LinMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []int32{-16, -1, 0, 1, 2, 63, 64, 65, 127, 128, 129, 2047, 2048, 2049, 3966, 3967, 4096}
	for x := int32(0); x <= 3967; x++ {
		samples = append(samples, x)
	}
	want, err := probeLibopusSILKLog(libopusSILKLogModeLog2Lin, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk log", err)
	}
	for i, sample := range samples {
		if got := silkLog2Lin(sample); got != want[i] {
			t.Fatalf("silkLog2Lin(%d)=%d want %d", sample, got, want[i])
		}
	}
}
