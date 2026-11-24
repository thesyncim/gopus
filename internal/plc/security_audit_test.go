package plc

import (
	"math"
	"testing"
)

func TestPLCDefensiveGuards(t *testing.T) {
	_ = inverse32VarQ(math.MinInt32, 46)
	if got := ConcealCELT(nil, nil, -1, 1.0); got != nil {
		t.Fatalf("ConcealCELT(-1) = %v, want nil", got)
	}
	if got := ConcealCELTHybrid(nil, nil, -1, 1.0); got != nil {
		t.Fatalf("ConcealCELTHybrid(-1) = %v, want nil", got)
	}
}
