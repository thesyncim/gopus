package celt

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// setDecodeHistory loads channel-major decoded history (decodeMemHistoryLen()
// samples per channel) into decode_mem.
func (d *Decoder) setDecodeHistory(hist []celtSig) {
	d.ensureDecodeMem()
	n := d.decodeMemHistoryLen()
	for c := range int(d.channels) {
		copy(d.decodeMemChannel(c)[:n], hist[c*n:(c+1)*n])
	}
}

// decodeMemOverlapLen returns the MDCT overlap length decode_mem carries per
// channel.
func (d *Decoder) decodeMemOverlapLen() int {
	return len(d.DecodeMem(0)) - d.decodeMemHistoryLen()
}

// synthesizeTest runs celt_synthesis for one frame of coeffs into every
// channel's out_syn and returns channel 0's out_syn.
func (d *Decoder) synthesizeTest(coeffs []float32, transient bool, shortBlocks int) []float32 {
	d.synthesizeToDecodeMem(coeffs, coeffs, len(coeffs), shortBlocks, transient)
	return append([]float32(nil), d.outSyn(0, len(coeffs))[:len(coeffs)]...)
}

// synthesizeStereoTest runs celt_synthesis for one stereo frame and returns
// the interleaved out_syn of both channels.
func (d *Decoder) synthesizeStereoTest(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32 {
	n := len(coeffsL)
	d.synthesizeToDecodeMem(coeffsL, coeffsR, n, shortBlocks, transient)
	out := make([]float32, 2*n)
	for i := range n {
		out[2*i] = d.outSyn(0, n)[i]
		out[2*i+1] = d.outSyn(1, n)[i]
	}
	return out
}

// postfilterTest moves decode_mem by frameSize, stores the interleaved samples
// as out_syn, runs the decoder postfilter on them and writes the filtered
// out_syn back to samples.
func (d *Decoder) postfilterTest(samples []float32, frameSize, lm, period int, gain float32, tapset int) {
	d.shiftDecodeMem(frameSize)
	channels := int(d.channels)
	for c := range channels {
		out := d.outSyn(c, frameSize)
		for i := range frameSize {
			out[i] = samples[i*channels+c]
		}
	}
	d.postfilterDecodeMem(frameSize, lm, period, gain, tapset)
	for c := range channels {
		out := d.outSyn(c, frameSize)
		for i := range frameSize {
			samples[i*channels+c] = out[i]
		}
	}
}

// TestShiftDecodeMemMatchesOpusMove drives decode_mem through random frame
// sizes and checks every channel's line against a plain buffer moved with
// libopus OPUS_MOVE semantics each frame, across window compactions. The
// 2048-sample custom geometry also checks the comb-filter headroom before
// decode_mem.
func TestShiftDecodeMemMatchesOpusMove(t *testing.T) {
	for _, tc := range []struct {
		name            string
		customFrameSize int
		frameSizes      []int
		wantHeadroom    int
	}{
		{"standard", 0, []int{120, 240, 480, 960}, 0},
		{"custom2048", 2048, []int{256, 512, 1024, 2048}, combFilterHistory},
	} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/ch%d", tc.name, channels), func(t *testing.T) {
				shiftDecodeMemAgainstOpusMove(t, channels, tc.customFrameSize, tc.frameSizes, tc.wantHeadroom)
			})
		}
	}
}

// shiftDecodeMemAgainstOpusMove runs one TestShiftDecodeMemMatchesOpusMove
// geometry.
func shiftDecodeMemAgainstOpusMove(t *testing.T, channels, customFrameSize int, frameSizes []int, wantHeadroom int) {
	rng := rand.New(rand.NewSource(0xdec0de))
	d := NewDecoder(channels)
	d.customFrameSize = customFrameSize
	d.ensureDecodeMem()
	if got := d.decodeMemCombHeadroom(); got != wantHeadroom {
		t.Fatalf("headroom=%d want %d", got, wantHeadroom)
	}
	line := d.decodeMemLineLen()
	end := d.decodeMemCombHeadroom() + d.decodeMemHistoryLen()
	model := make([][]celtSig, channels)
	for c := range model {
		model[c] = make([]celtSig, line)
	}
	next := float32(1)
	for frame := range 2000 {
		n := frameSizes[rng.Intn(len(frameSizes))]
		d.shiftDecodeMem(n)
		for c := range channels {
			m := model[c]
			copy(m, m[n:])
			// The caller writes out_syn and the overlap after the move.
			out := d.outSyn(c, n)
			for i := range out {
				out[i] = next
				m[end-n+i] = next
				next++
			}
		}
		for c := range channels {
			got := d.decodeMemLine(c)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(model[c][i]) {
					t.Fatalf("frame %d n=%d: line[%d][%d]=%v want %v", frame, n, c, i, got[i], model[c][i])
				}
			}
		}
	}
}

// TestPostfilterDecodeMemFullHistoryFrame runs the received-frame comb filter
// on 2048-sample frames of the 96 kHz QEXT custom geometry, where out_syn
// starts at decode_mem[c]. With constant postfilter parameters it checks each
// frame against the in-place comb filter on a contiguous buffer of the previous
// output followed by the frame, across decode_mem window compactions.
func TestPostfilterDecodeMemFullHistoryFrame(t *testing.T) {
	const (
		n      = 2048
		lm     = 3
		short  = n >> lm
		gain   = float32(0.5)
		tapset = 1
	)
	for _, period := range []int{combFilterMinPeriod, 700, combFilterMaxPeriod - 2} {
		for _, channels := range []int{1, 2} {
			d := NewDecoder(channels)
			d.customFrameSize = n
			d.Reset()
			rng := rand.New(rand.NewSource(int64(period*channels + channels)))
			model := make([][]float32, channels)
			for c := range model {
				model[c] = make([]float32, combFilterHistory+n)
			}
			overlap := d.synthOverlapLen()
			windowSq := d.postfilterWindowSquareF32(overlap)
			samples := make([]float32, n*channels)
			for frame := range 8 {
				for i := range samples {
					samples[i] = rng.Float32()*2 - 1
				}
				for c := range channels {
					m := model[c]
					copy(m, m[n:])
					for i := range n {
						m[combFilterHistory+i] = samples[i*channels+c]
					}
				}
				d.postfilterTest(samples, n, lm, period, gain, tapset)
				for c := range channels {
					m := model[c]
					if frame > 0 {
						// The previous frame committed (period, gain, tapset) as
						// both the old and the current parameters.
						combFilterInPlace(m, combFilterHistory, period, period, short, gain, gain, tapset, tapset, windowSq, overlap)
						combFilterInPlace(m, combFilterHistory+short, period, period, n-short, gain, gain, tapset, tapset, windowSq, overlap)
					}
					for i := range n {
						got := samples[i*channels+c]
						if frame == 0 {
							// The first frame fades in from the reset state;
							// keep the decoder output as the next frame's history.
							m[combFilterHistory+i] = got
							continue
						}
						if math.Float32bits(got) != math.Float32bits(m[combFilterHistory+i]) {
							t.Fatalf("period=%d channels=%d frame %d: out_syn[%d][%d]=%v want %v", period, channels, frame, c, i, got, m[combFilterHistory+i])
						}
					}
				}
			}
		}
	}
}

// TestSynthesizeToDecodeMemMonoSpectrumOnStereo synthesizes a mono spectrum
// on a stereo decoder, as celt_synthesis does for C=1, CC=2, and checks each
// channel's out_syn and next overlap against a mono decoder carrying that
// channel's previous overlap.
func TestSynthesizeToDecodeMemMonoSpectrumOnStereo(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5ae0))
	for _, frameSize := range []int{120, 240, 480, 960} {
		for _, shortBlocks := range []int{1, frameSize / 120} {
			transient := shortBlocks > 1
			coeffs := randSpectrum(rng, frameSize)
			stereo := NewDecoder(2)
			hist := stereo.decodeMemHistoryLen()
			overlap := stereo.synthOverlapLen()
			for c := range 2 {
				prev := stereo.DecodeMem(c)[hist : hist+overlap]
				for i := range prev {
					prev[i] = celtSig(float64((i*(c+3))%23-11) * 0.03125)
				}
			}
			stereo.synthesizeToDecodeMem(coeffs, coeffs, frameSize, shortBlocks, transient)
			for c := range 2 {
				mono := NewDecoder(1)
				prev := mono.DecodeMem(0)[hist : hist+overlap]
				for i := range prev {
					prev[i] = celtSig(float64((i*(c+3))%23-11) * 0.03125)
				}
				mono.synthesizeToDecodeMem(coeffs, nil, frameSize, shortBlocks, transient)
				want := mono.outSyn(0, frameSize)
				got := stereo.outSyn(c, frameSize)
				if i := equalFloat32Bits(got, want); i >= 0 {
					t.Fatalf("frame=%d B=%d channel %d: out_syn[%d]=%v want %v", frameSize, shortBlocks, c, i, got[i], want[i])
				}
			}
		}
	}
}

// TestSynthesizeFrameZeroAllocs locks the decode_mem synthesis tail at zero
// allocations across window compactions.
func TestSynthesizeFrameZeroAllocs(t *testing.T) {
	for _, channels := range []int{1, 2} {
		d := NewDecoder(channels)
		const n = 120
		spec := make([]float32, n)
		for i := range spec {
			spec[i] = float32((i*37)%101-50) / 64
		}
		pcm := make([]float32, n*channels)
		d.directOutPCM = pcm
		run := func() {
			d.synthesizeFrame(spec, spec, n, 0, 1, false, 100, 0.5, 1)
		}
		for range 40 {
			run()
		}
		if allocs := testing.AllocsPerRun(200, run); allocs != 0 {
			t.Fatalf("channels=%d: synthesizeFrame allocated %g times/run", channels, allocs)
		}
	}
}
