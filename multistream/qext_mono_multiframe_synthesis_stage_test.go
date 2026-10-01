//go:build gopus_qext && !gopus_fixed_point

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTMonoMultiFrameSynthesisStagesMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	_, packets, frames := makeLibopusQEXTMultiFrameStreamPacketForTest(t, opusDemo, 1)
	stream := newStreamDecoder(48000, 1)
	stage := stream.celtDec.EnableSynthesisStageTrace()
	for frame, input := range frames {
		if _, err := stream.decodeFramePayload(input.rawFrame, 960, input.toc, input.qextPayload); err != nil {
			t.Fatalf("Go decode frame[%d]: %v", frame, err)
		}
		goEnergy := append([]float32(nil), stage.QEXTEnergy(0)...)
		goQEXTNorm := append([]float32(nil), stage.QEXTNorm(0)...)
		goBaseEnergy := append([]float32(nil), stage.BaseEnergy(0)...)
		goBaseNorm := append([]float32(nil), stage.BaseNorm(0)...)
		cTrace := traceQEXTCELTSynthesisSequence(t, packets, frame, 1, 960)
		qextStart := 100 * (cTrace.n / 120)
		if i, gotBits, wantBits := firstQEXTFloat32Difference(goEnergy, cTrace.qextEnergy[0]); i >= 0 {
			t.Errorf("frame[%d] QEXT energy first difference at band %d: Go=%08x C=%08x", frame, i, gotBits, wantBits)
		}
		if i, gotBits, wantBits := firstQEXTFloat32Difference(goQEXTNorm[qextStart:], cTrace.qextNorm[0][qextStart:]); i >= 0 {
			t.Errorf("frame[%d] QEXT normalized coefficient first difference at bin %d: Go=%08x C=%08x", frame, i+qextStart, gotBits, wantBits)
		}
		if i, gotBits, wantBits := firstQEXTFloat32Difference(goBaseEnergy, cTrace.baseEnergy[0]); i >= 0 {
			t.Errorf("frame[%d] base energy first difference at band %d: Go=%08x C=%08x", frame, i, gotBits, wantBits)
		}
		if i, gotBits, wantBits := firstQEXTFloat32Difference(goBaseNorm[:qextStart], cTrace.baseNorm[0][:qextStart]); i >= 0 {
			t.Errorf("frame[%d] base normalized coefficient first difference at bin %d: Go=%08x C=%08x", frame, i, gotBits, wantBits)
		}
	}
}
