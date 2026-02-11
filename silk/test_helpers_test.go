package silk

import (
	"sync"
	"testing"
)

const int16Scale = 1.0 / 32768.0

var silkQualityOnce sync.Once

func logSilkQualityStatus(t *testing.T) {
	t.Helper()
	silkQualityOnce.Do(func() {
		t.Log("SILK QUALITY STATUS: expected failures while encoder parity is in progress.")
		t.Log("ATTEMPTED: frame type coding tables (VAD active/inactive), inputBuffer-based pitch residual + noise-shape window alignment, int16 quantization at entry.")
		t.Log("KNOWN: SILK roundtrip amplitude is still unstable (too low in some paths, overdrives in others).")
		t.Log("NEXT: verify SILK past-only buffer vs libopus, LTP residual alignment, and gain/LSF parity before FEC checks.")
	})
}
