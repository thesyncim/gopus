package libopustest

import "testing"

func TestOracleEnabledEnvironmentMatrix(t *testing.T) {
	tests := []struct {
		name   string
		oracle string
		tier   string
		strict string
		want   bool
	}{
		{name: "default_on", want: true},
		{name: "parity_on", tier: "parity", want: true},
		{name: "fast_off_without_tag", tier: "fast", want: oracleBuildTagEnabled},
		{name: "smoke_off_without_tag", tier: "smoke", want: oracleBuildTagEnabled},
		{name: "explicit_zero_off", oracle: "0", want: false},
		{name: "explicit_false_off", oracle: " false ", want: false},
		{name: "strict_overrides_fast", tier: "fast", strict: "true", want: true},
		{name: "strict_overrides_explicit_off", oracle: "off", strict: "yes", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOPUS_LIBOPUS_ORACLE", tc.oracle)
			t.Setenv("GOPUS_TEST_TIER", tc.tier)
			t.Setenv("GOPUS_STRICT_LIBOPUS_REF", tc.strict)
			if got := OracleEnabled(); got != tc.want {
				t.Fatalf("OracleEnabled()=%v want %v", got, tc.want)
			}
		})
	}
}
