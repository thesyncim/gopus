package encoder

import "testing"

// opus_encode_native returns a low-space packet before it calls the frame
// encoder's dc_reject or hp_cutoff input filter.
func TestLowSpacePacketPreservesInputHighPassState(t *testing.T) {
	const frameSize = 480
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = 0.375
	}
	for _, voip := range []bool{false, true} {
		for _, shortInput := range []bool{false, true} {
			name := "audio_float"
			switch {
			case voip && shortInput:
				name = "voip_short"
			case voip:
				name = "voip_float"
			case shortInput:
				name = "audio_short"
			}
			t.Run(name, func(t *testing.T) {
				e := NewEncoder(48000, 1)
				e.SetVoIPApplication(voip)
				encode := func(maxBytes int) ([]byte, error) {
					if shortInput {
						return e.EncodeShortMixedWithAnalysisMaxBytes(pcm, frameSize, pcm, maxBytes)
					}
					return e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, maxBytes)
				}
				packet, err := encode(1)
				if err != nil || len(packet) != 1 {
					t.Fatalf("low-space packet len=%d err=%v", len(packet), err)
				}
				if e.hpMem != ([4]float32{}) || e.variableHPSmth2Inited {
					t.Fatalf("low-space packet advanced high-pass state: mem=%v smoother=%d initialized=%t",
						e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
				}
				packet, err = encode(400)
				if err != nil || len(packet) < 2 {
					t.Fatalf("following packet len=%d err=%v", len(packet), err)
				}
				if e.hpMem == ([4]float32{}) {
					t.Fatal("following real frame did not advance high-pass memory")
				}
				if voip && !e.variableHPSmth2Inited {
					t.Fatal("following VoIP frame did not initialize cutoff smoother")
				}
				memAfterReal := e.hpMem
				smootherAfterReal := e.variableHPSmth2Q15
				packet, err = encode(1)
				if err != nil || len(packet) != 1 {
					t.Fatalf("second low-space packet len=%d err=%v", len(packet), err)
				}
				if e.hpMem != memAfterReal || e.variableHPSmth2Q15 != smootherAfterReal {
					t.Fatalf("second low-space packet advanced high-pass state: mem=%v/%v smoother=%d/%d",
						e.hpMem, memAfterReal, e.variableHPSmth2Q15, smootherAfterReal)
				}
			})
		}
	}
}
