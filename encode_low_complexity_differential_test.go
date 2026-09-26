package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestEncodeDifferentialLowComplexity checks low-complexity CELT and Hybrid
// encodes against libopus, including the complexity-zero transient gate in
// Hybrid SWB.
// Below complexity 4 the coarse-energy intra decision reads delayedIntra, so
// the silent frames the corpus signals start with must advance the encoder
// state exactly as celt_encode_with_ec's full silent-frame pass does.
func TestEncodeDifferentialLowComplexity(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}

	const sampleRate = 48000
	const framesPerSpec = 8
	for _, spec := range buildEncDiffSweep() {
		if spec.fec || spec.dtx {
			continue
		}
		complexities := []int(nil)
		if spec.gmode == EncoderModeCELT && (spec.frameMs == ExpertFrameDuration2_5Ms || spec.frameMs == ExpertFrameDuration20Ms) {
			complexities = []int{0, 5}
		}
		if spec.gmode == EncoderModeHybrid && spec.bitrate == 48000 && spec.vbr == BitrateModeVBR &&
			((spec.channels == 1 && spec.frameMs == ExpertFrameDuration10Ms) || (spec.channels == 2 && spec.frameMs == ExpertFrameDuration20Ms)) {
			complexities = []int{0, 1}
		}
		if len(complexities) == 0 {
			continue
		}
		for _, complexity := range complexities {
			t.Run(fmt.Sprintf("%s/cx%d", spec.name, complexity), func(t *testing.T) {
				fs := encFrameSamples48k(spec.frameMs)
				pcm, err := testsignal.GenerateCorpusSignal(spec.sigClass, sampleRate, fs*framesPerSpec*spec.channels, spec.channels)
				if err != nil {
					t.Fatalf("GenerateCorpusSignal(%s): %v", spec.sigClass, err)
				}
				vbr, constraint := vbrFlags(spec.vbr)
				want, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
					SampleRate:    sampleRate,
					Channels:      spec.channels,
					Application:   libopustest.EncodeDiffApplicationAudio,
					ForceMode:     spec.forceMode,
					Bandwidth:     spec.bwCode,
					MaxBandwidth:  spec.bwCode,
					Bitrate:       spec.bitrate,
					Complexity:    complexity,
					Signal:        spec.signal,
					VBR:           vbr,
					VBRConstraint: constraint,
					ForceChannels: spec.channels,
					FrameSize:     fs,
					FrameCount:    framesPerSpec,
					PCM:           pcm,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "encode diff oracle", err)
					return
				}
				enc, ok := configureEncDiff(t, spec)
				if !ok {
					t.Fatalf("gopus rejected config %s", spec.name)
				}
				if err := enc.SetComplexity(complexity); err != nil {
					t.Fatalf("SetComplexity(%d): %v", complexity, err)
				}
				for f := range framesPerSpec {
					pkt, err := encDiffEncodeOneFrame(enc, pcm[f*fs*spec.channels:(f+1)*fs*spec.channels])
					if err != nil {
						t.Fatalf("frame %d: gopus encode: %v", f, err)
					}
					o := want[f]
					if !bytes.Equal(pkt, o.Packet) || enc.FinalRange() != o.FinalRange {
						firstDiff := firstByteDiff(pkt, o.Packet)
						var gotWindow, wantWindow []byte
						if firstDiff >= 0 {
							start := max(firstDiff-2, 0)
							gotWindow = pkt[start:min(firstDiff+3, len(pkt))]
							wantWindow = o.Packet[start:min(firstDiff+3, len(o.Packet))]
						}
						t.Fatalf("frame %d: gopus len=%d rng=%08x, libopus len=%d rng=%08x, firstByteDiff=%d go[%x] C[%x]", f, len(pkt), enc.FinalRange(), len(o.Packet), o.FinalRange, firstDiff, gotWindow, wantWindow)
					}
				}
			})
		}
	}
}
