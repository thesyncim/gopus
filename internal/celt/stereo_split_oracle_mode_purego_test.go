//go:build purego

package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func requireCELTStereoSplitOracleMode(t *testing.T) libopustooling.LibopusReferenceVariant {
	t.Helper()
	return libopustooling.LibopusReferenceScalar
}
