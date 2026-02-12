//go:build !gopus_tmp_env

package celt

// Production/default build: disable all temporary env tuning and debug toggles.
// This keeps hot paths free of repeated env lookups.
func tmpGetenv(_ string) string {
	return ""
}

func tmpLookupEnv(_ string) (string, bool) {
	return "", false
}
