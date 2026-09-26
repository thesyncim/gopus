//go:build gopus_qext

package encoder

import "testing"

func TestQEXTInputHighPassStateFollowsApplication(t *testing.T) {
	e := NewEncoder(48000, 1)
	input := make([]opusRes, 120)
	for i := range input {
		input[i] = 0.25
	}

	e.SetQEXT(false)
	filtered := e.preprocessInputHP(input, 120)
	if &filtered[0] == &input[0] || e.hpMem == ([4]float32{}) {
		t.Fatal("non-VoIP QEXT-off input did not run dc_reject")
	}
	beforeBypass := e.hpMem
	e.SetQEXT(true)
	bypassed := e.preprocessInputHP(input, 120)
	if &bypassed[0] != &input[0] || e.hpMem != beforeBypass {
		t.Fatal("non-VoIP QEXT-on input changed samples or high-pass memory")
	}
	if allocs := testing.AllocsPerRun(1000, func() { e.preprocessInputHP(input, 120) }); allocs != 0 {
		t.Fatalf("warmed QEXT input bypass allocated %.1f times", allocs)
	}
	if e.hpMem != beforeBypass {
		t.Fatal("repeated QEXT input bypass advanced high-pass memory")
	}

	e.SetQEXT(false)
	filtered = e.preprocessInputHP(input, 120)
	if &filtered[0] == &input[0] || e.hpMem == beforeBypass {
		t.Fatal("non-VoIP QEXT-off input did not resume dc_reject state")
	}

	e.SetVoIPApplication(true)
	e.SetQEXT(true)
	beforeVoIP := e.hpMem
	filtered = e.preprocessInputHP(input, 120)
	if &filtered[0] == &input[0] || e.hpMem == beforeVoIP {
		t.Fatal("VoIP QEXT-on input did not use its high-pass filter")
	}
}
