package celt

import (
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
// sizes and checks every channel's window against a plain buffer moved with
// libopus OPUS_MOVE semantics each frame, across window compactions.
func TestShiftDecodeMemMatchesOpusMove(t *testing.T) {
	rng := rand.New(rand.NewSource(0xdec0de))
	for _, channels := range []int{1, 2} {
		d := NewDecoder(channels)
		size := d.decodeMemLen()
		hist := d.decodeMemHistoryLen()
		model := make([][]celtSig, channels)
		for c := range model {
			model[c] = make([]celtSig, size)
		}
		next := float32(1)
		for frame := range 2000 {
			n := []int{120, 240, 480, 960}[rng.Intn(4)]
			d.shiftDecodeMem(n)
			for c := range channels {
				m := model[c]
				copy(m, m[n:])
				// The caller writes out_syn and the overlap after the move.
				out := d.outSyn(c, n)
				for i := range out {
					out[i] = next
					m[hist-n+i] = next
					next++
				}
			}
			for c := range channels {
				got := d.decodeMemChannel(c)
				for i := range got {
					if math.Float32bits(got[i]) != math.Float32bits(model[c][i]) {
						t.Fatalf("channels=%d frame %d n=%d: decode_mem[%d][%d]=%v want %v", channels, frame, n, c, i, got[i], model[c][i])
					}
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
