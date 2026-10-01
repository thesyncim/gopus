package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// stdBandDecodeParams holds the quant_all_bands() decode parameters of one
// standard-mode frame.
type stdBandDecodeParams struct {
	channels, frameSize, lm, start, end        int
	pulses, tfRes                              []int32
	shortBlocks, spread, dualStereo, intensity int
	totalBitsQ3, balance, codedBands           int
	disableInv                                 bool
	seed                                       uint32
}

// randomStdBandDecodeParams returns decode parameters for random band data.
func randomStdBandDecodeParams(rng *rand.Rand, bufLen int) stdBandDecodeParams {
	p := stdBandDecodeParams{channels: 1 + rng.Intn(2), lm: rng.Intn(4)}
	p.frameSize = 120 << p.lm
	if rng.Intn(4) == 0 {
		p.start = 17
	}
	p.end = p.start + 1 + rng.Intn(MaxBands-p.start)
	transient := 0
	if p.lm > 0 && rng.Intn(3) == 0 {
		p.shortBlocks = 1 << p.lm
		transient = 1
	}
	// tf_res takes the tf_decode() values: a tf_select_table entry for the
	// frame's transient flag and tf_select.
	row := &tfSelectTable[p.lm]
	base := 4*transient + 2*rng.Intn(2)
	p.pulses = make([]int32, MaxBands)
	p.tfRes = make([]int32, MaxBands)
	for i := range p.pulses {
		p.pulses[i] = int32(rng.Intn(400))
		p.tfRes[i] = int32(row[base+rng.Intn(2)])
	}
	p.spread = rng.Intn(4)
	if p.channels == 2 {
		p.dualStereo = rng.Intn(2)
		p.intensity = p.start + rng.Intn(p.end-p.start+1)
	}
	p.totalBitsQ3 = rng.Intn(bufLen*64) + 64
	p.balance = rng.Intn(200) - 100
	p.codedBands = p.start + rng.Intn(p.end-p.start+1)
	p.disableInv = rng.Intn(2) == 0
	p.seed = rng.Uint32()
	return p
}

// TestQuantAllBandsDecodeStdMatchesWithMode decodes random mono and stereo band
// data through the standard-mode setup and through the generic mode setup, with
// scratch reused across frames of different sizes, and requires the same
// coefficients, collapse masks, seed and range decoder state.
func TestQuantAllBandsDecodeStdMatchesWithMode(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57d))
	buf := make([]byte, 400)
	var scA, scB bandDecodeScratch
	for iter := range 2000 {
		p := randomStdBandDecodeParams(rng, len(buf))
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		var rdA, rdB rangecoding.Decoder
		rdA.Init(buf)
		rdB.Init(buf)
		seedA, seedB := p.seed, p.seed
		leftA, rightA, colA := quantAllBandsDecodeStd(&rdA, p.channels, p.frameSize, p.lm, p.start, p.end, p.pulses,
			p.shortBlocks, p.spread, p.dualStereo, p.intensity, p.tfRes, p.totalBitsQ3, p.balance, p.codedBands,
			p.disableInv, &seedA, &scA)
		leftB, rightB, colB := quantAllBandsDecodeWithScratchWithMode(&rdB, p.channels, p.frameSize, p.lm, p.start, p.end, p.pulses,
			p.shortBlocks, p.spread, p.dualStereo, p.intensity, p.tfRes, p.totalBitsQ3, p.balance, p.codedBands,
			p.disableInv, &seedB, &scB, nil, nil, 0, nil, nil, nil, nil)
		if seedA != seedB || rdA.Tell() != rdB.Tell() || rdA.Range() != rdB.Range() || rdA.Val() != rdB.Val() {
			t.Fatalf("iter %d: seed/range state diverged", iter)
		}
		if len(leftA) != len(leftB) || len(rightA) != len(rightB) || len(colA) != len(colB) {
			t.Fatalf("iter %d: lengths left %d/%d right %d/%d collapse %d/%d", iter,
				len(leftA), len(leftB), len(rightA), len(rightB), len(colA), len(colB))
		}
		for _, pair := range [][2][]celtNorm{{leftA, leftB}, {rightA, rightB}} {
			for i := range pair[1] {
				if math.Float32bits(float32(pair[0][i])) != math.Float32bits(float32(pair[1][i])) {
					t.Fatalf("iter %d: coefficient %d = %v want %v", iter, i, pair[0][i], pair[1][i])
				}
			}
		}
		for i := range colB {
			if colA[i] != colB[i] {
				t.Fatalf("iter %d: collapse[%d]=%d want %d", iter, i, colA[i], colB[i])
			}
		}
	}
}

func TestQuantAllBandsDecodeStdNoAllocs(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57e))
	buf := make([]byte, 400)
	for i := range buf {
		buf[i] = byte(rng.Intn(256))
	}
	for _, channels := range []int{1, 2} {
		var p stdBandDecodeParams
		for p.channels != channels || p.lm != 3 {
			p = randomStdBandDecodeParams(rng, len(buf))
		}
		var sc bandDecodeScratch
		var rd rangecoding.Decoder
		seed := p.seed
		run := func() {
			rd.Init(buf)
			quantAllBandsDecodeStd(&rd, p.channels, p.frameSize, p.lm, p.start, p.end, p.pulses,
				p.shortBlocks, p.spread, p.dualStereo, p.intensity, p.tfRes, p.totalBitsQ3, p.balance, p.codedBands,
				p.disableInv, &seed, &sc)
		}
		run()
		if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
			t.Fatalf("channels=%d: %v allocs per run, want 0", channels, allocs)
		}
	}
}

// referenceCollapseMask is the libopus quant_band() collapse mask: bit i is
// set when block i of the interleaved pulse vector holds a nonzero pulse.
func referenceCollapseMask(pulses []int32, n, b int) int {
	if b <= 1 {
		return 1
	}
	n0 := n / b
	mask := 0
	for i := range b {
		tmp := int32(0)
		for j := range n0 {
			tmp |= pulses[i*n0+j]
		}
		if tmp != 0 {
			mask |= 1 << i
		}
	}
	return mask
}

func TestExtractCollapseMaskMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc011))
	pulses := make([]int32, 176)
	for iter := range 20000 {
		b := 1 << rng.Intn(4)
		n := b * (1 + rng.Intn(len(pulses)/b))
		density := rng.Intn(4)
		for i := range pulses {
			pulses[i] = 0
			if rng.Intn(8) < density {
				pulses[i] = int32(rng.Intn(65) - 32)
			}
		}
		if rng.Intn(8) == 0 {
			pulses[rng.Intn(n)] = math.MinInt32
		}
		if got, want := extractCollapseMask(pulses, n, b), referenceCollapseMask(pulses, n, b); got != want {
			t.Fatalf("iter %d n=%d b=%d: mask %b want %b", iter, n, b, got, want)
		}
	}
}
