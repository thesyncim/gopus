package multistream

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var transitionSequenceRefHelper libopustest.HelperCache

type transitionDecodeStep struct {
	packet    []byte
	frameSize int
}

type transitionDecodeResult struct {
	pcm        []float32
	samples    int
	finalRange uint32
}

// decodeTransitionSequenceWithLibopus uses the matched C decoder's v8 transport:
// each step has its own frame capacity, returned sample count, final range, and
// PCM span. The packet sequence preserves decoder history across transitions.
func decodeTransitionSequenceWithLibopus(t *testing.T, sampleRate, channels, gainQ8, maxFrameSize int, steps []transitionDecodeStep) []transitionDecodeResult {
	t.Helper()
	path, err := transitionSequenceRefHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "single-stream transition sequence reference",
		OutputBase: "gopus_transition_sequence_ref",
		SourceFile: "libopus_refdecode_single.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "single-stream transition sequence reference", err)
	}
	payload := libopustest.NewOraclePayloadVersion(
		"GOSI", 8, 0, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(maxFrameSize), uint32(len(steps)),
	)
	for i, step := range steps {
		if step.frameSize <= 0 || step.frameSize > maxFrameSize {
			t.Fatalf("step %d invalid frame capacity %d", i, step.frameSize)
		}
		payload.U32(0) // decode_fec
		payload.U32(uint32(step.frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracleVersion(path, payload.Bytes(), "single-stream transition sequence reference", "GOSO", 3)
	if err != nil {
		libopustest.HelperUnavailable(t, "single-stream transition sequence reference", err)
	}
	pcmCount := reader.Count(-1)
	if pcmCount < 0 || pcmCount > len(steps)*maxFrameSize*channels {
		t.Fatalf("C PCM count %d exceeds step capacity %d", pcmCount, len(steps)*maxFrameSize*channels)
	}
	if reader.Remaining() != pcmCount*4+4+len(steps)*16 {
		t.Fatalf("C v8 output size=%d want %d", reader.Remaining(), pcmCount*4+4+len(steps)*16)
	}
	pcm := make([]float32, pcmCount)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if count := reader.Count(len(steps)); count != len(steps) {
		t.Fatalf("C step count=%d want %d", count, len(steps))
	}
	results := make([]transitionDecodeResult, len(steps))
	nextOffset := 0
	for i := range steps {
		status := reader.I32()
		samples := reader.Count(-1)
		finalRange := reader.U32()
		offset := reader.Count(-1)
		if status != 0 || samples < 0 || samples > steps[i].frameSize || offset != nextOffset || samples > (pcmCount-offset)/channels {
			t.Fatalf("C step %d record=(status=%d samples=%d range=%08x offset=%d), next offset=%d PCM count=%d",
				i, status, samples, finalRange, offset, nextOffset, pcmCount)
		}
		end := offset + samples*channels
		results[i] = transitionDecodeResult{pcm: pcm[offset:end], samples: samples, finalRange: finalRange}
		nextOffset = end
	}
	if nextOffset != pcmCount {
		t.Fatalf("C step PCM spans end at %d, total PCM=%d", nextOffset, pcmCount)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return results
}

// TestTransitionFullSequenceMatchesLibopus checks complete transition and
// recovery frames in both CELT-boundary directions at every supported API rate.
// It compares each returned length, final range, and float bit against the
// matched libopus build, with no window-specific tolerance.
func TestTransitionFullSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize48 = 480
	modes := []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid}
	for _, channels := range []int{1, 2} {
		packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize48, modes)
		for i, packet := range packets {
			wantMode := streamModeHybrid
			if modes[i] == encoder.ModeCELT {
				wantMode = streamModeCELT
			}
			if gotMode := streamModeOfPacket(packet); gotMode != wantMode {
				t.Fatalf("packet %d mode=%d want %d", i, gotMode, wantMode)
			}
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			frameSize := frameSize48 * sampleRate / 48000
			steps := make([]transitionDecodeStep, len(packets))
			for i, packet := range packets {
				steps[i] = transitionDecodeStep{packet: packet, frameSize: frameSize}
			}
			for _, gainQ8 := range []int{0, 768, -768} {
				t.Run(fmt.Sprintf("ch%d/fs%d/g%d", channels, sampleRate, gainQ8), func(t *testing.T) {
					want := decodeTransitionSequenceWithLibopus(t, sampleRate, channels, gainQ8, frameSize, steps)
					dec := newStreamDecoder(sampleRate, channels)
					if err := dec.SetGain(gainQ8); err != nil {
						t.Fatalf("SetGain(%d): %v", gainQ8, err)
					}
					for i, step := range steps {
						if want[i].samples != frameSize {
							t.Fatalf("C frame %d returned %d samples/channel, want packet duration %d", i, want[i].samples, frameSize)
						}
						got, err := dec.Decode(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("Go frame %d: %v", i, err)
						}
						if len(got) != want[i].samples*channels {
							t.Fatalf("Go frame %d returned %d PCM samples, C %d", i, len(got), want[i].samples*channels)
						}
						if gotRange := dec.FinalRange(); gotRange != want[i].finalRange {
							t.Fatalf("frame %d final range Go=%08x C=%08x", i, gotRange, want[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("frame %d %v→%v", i, modes[max(0, i-1)], modes[i]))
					}
				})
			}
		}
	}
}

// TestTransitionPreviousCELTPLCStageMatchesLibopus isolates the CELT PLC audio
// entering CELT→Hybrid transitions. A public C NULL decode after the identical
// packet history requests exactly F5 samples/channel, matching the recursive
// opus_decode_frame(NULL) CELT branch before its outer crossfade.
func TestTransitionPreviousCELTPLCStageMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize48 = 480
	modes := []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid}
	for _, channels := range []int{1, 2} {
		packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize48, modes)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			frameSize := frameSize48 * sampleRate / 48000
			f5 := sampleRate / 200
			for _, gainQ8 := range []int{0, 768, -768} {
				for _, target := range []int{2, 4} {
					t.Run(fmt.Sprintf("ch%d/fs%d/g%d/target%d", channels, sampleRate, gainQ8, target), func(t *testing.T) {
						steps := make([]transitionDecodeStep, target+1)
						for i := range target {
							steps[i] = transitionDecodeStep{packet: packets[i], frameSize: frameSize}
						}
						steps[target] = transitionDecodeStep{frameSize: f5}
						want := decodeTransitionSequenceWithLibopus(t, sampleRate, channels, gainQ8, frameSize, steps)
						dec := newStreamDecoder(sampleRate, channels)
						if err := dec.SetGain(gainQ8); err != nil {
							t.Fatal(err)
						}
						for i := range target {
							got, err := dec.Decode(packets[i], frameSize)
							if err != nil {
								t.Fatalf("history frame %d: %v", i, err)
							}
							if want[i].samples != frameSize || len(got) != frameSize*channels || dec.FinalRange() != want[i].finalRange {
								t.Fatalf("history frame %d length/range Go=(%d,%08x) C=(%d,%08x)",
									i, len(got), dec.FinalRange(), want[i].samples*channels, want[i].finalRange)
							}
							assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("history frame %d", i))
						}
						if dec.lastMode != streamModeCELT {
							t.Fatalf("previous mode=%d, want CELT", dec.lastMode)
						}
						got, err := dec.transitionPLCToFloat32(f5, int(dec.lastMode), int(dec.lastBandwidth), dec.lastPacketStereo)
						if err != nil {
							t.Fatal(err)
						}
						if want[target].samples != f5 || len(got) != f5*channels {
							t.Fatalf("F5 PLC length Go=%d C=%d want %d", len(got), want[target].samples*channels, f5*channels)
						}
						assertTransitionStagePCMExact(t, got, want[target].pcm, "full CELT PLC stage")
					})
				}
			}
		}
	}
}
