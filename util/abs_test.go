package util

import "testing"

func TestAbs(t *testing.T) {
	// int
	if Abs(-5) != 5 {
		t.Error("Abs(-5) should be 5")
	}
	if Abs(5) != 5 {
		t.Error("Abs(5) should be 5")
	}

	// int32
	if Abs(int32(-100)) != 100 {
		t.Error("Abs(int32(-100)) should be 100")
	}

	// int16
	if Abs(int16(-32)) != 32 {
		t.Error("Abs(int16(-32)) should be 32")
	}

	// float32
	if Abs(float32(-3.14)) != float32(3.14) {
		t.Error("Abs(float32(-3.14)) should be 3.14")
	}
}
