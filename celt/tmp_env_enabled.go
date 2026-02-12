//go:build gopus_tmp_env

package celt

import "os"

// Debug/tuning build: opt in to env-driven temporary toggles.
// Use only for local investigation (build with -tags gopus_tmp_env).
func tmpGetenv(name string) string {
	return os.Getenv(name)
}

func tmpLookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}
