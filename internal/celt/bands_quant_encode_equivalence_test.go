package celt

import (
	"bytes"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

type quantAllBandsEncodeCase struct {
	channels, lm, end, shortBlocks, spread, dualStereo, intensity int
	totalBitsQ3, balance, codedBands, complexity                  int
	disableInv                                                    bool
	seed                                                          uint32
	x, y                                                          []celtNorm
	pulses, tfRes                                                 []int32
	bandE                                                         []celtEner
	prefix                                                        int
	storage                                                       int
}

func randomQuantAllBandsEncodeCase(rng *rand.Rand) quantAllBandsEncodeCase {
	c := quantAllBandsEncodeCase{
		channels:   1 + rng.Intn(2),
		lm:         rng.Intn(4),
		end:        []int{13, 17, 19, 21}[rng.Intn(4)],
		spread:     rng.Intn(4),
		complexity: []int{5, 8, 10}[rng.Intn(3)],
		disableInv: rng.Intn(4) == 0,
		seed:       rng.Uint32(),
		prefix:     rng.Intn(40),
	}
	M := 1 << c.lm
	frameSize := M * Overlap
	isTransient := c.lm > 0 && rng.Intn(3) == 0
	if isTransient {
		c.shortBlocks = M
	}
	if c.channels == 2 {
		c.intensity = rng.Intn(c.end + 1)
		if rng.Intn(4) == 0 {
			c.dualStereo = 1
		}
	}
	c.storage = 20 + rng.Intn(300)
	c.totalBitsQ3 = (c.storage*8 - 40) << bitRes
	c.codedBands = 1 + rng.Intn(c.end)
	c.balance = rng.Intn(64) - 32

	c.x = make([]celtNorm, frameSize)
	if c.channels == 2 {
		c.y = make([]celtNorm, frameSize)
	}
	fillBand := func(v []celtNorm) {
		kind := rng.Intn(6)
		var e float64
		for i := range v {
			switch kind {
			case 0:
				v[i] = 0
			case 1:
				if i == rng.Intn(len(v)) {
					v[i] = 1
				}
			default:
				v[i] = celtNorm(rng.NormFloat64())
			}
			e += float64(v[i]) * float64(v[i])
		}
		if e > 0 {
			g := float32(1 / math.Sqrt(e))
			for i := range v {
				v[i] = celtNorm(float32(v[i]) * g)
			}
		}
	}
	for i := 0; i < c.end; i++ {
		lo, hi := M*EBands[i], M*EBands[i+1]
		fillBand(c.x[lo:hi])
		if c.y != nil {
			if rng.Intn(5) == 0 {
				copy(c.y[lo:hi], c.x[lo:hi])
			} else {
				fillBand(c.y[lo:hi])
			}
		}
	}

	c.pulses = make([]int32, MaxBands)
	for i := range c.pulses[:c.end] {
		n := M * (EBands[i+1] - EBands[i])
		c.pulses[i] = int32(rng.Intn(c.channels * n * 48))
		if rng.Intn(6) == 0 {
			c.pulses[i] = 0
		}
	}
	c.tfRes = make([]int32, MaxBands)
	tfSelect := rng.Intn(2)
	for i := range c.tfRes {
		idx := 2*tfSelect + rng.Intn(2)
		if isTransient {
			idx += 4
		}
		c.tfRes[i] = int32(tfSelectTable[c.lm][idx])
	}
	c.bandE = make([]celtEner, c.channels*MaxBands)
	for i := range c.bandE {
		c.bandE[i] = celtEner(rng.Float32() * 4)
		if rng.Intn(12) == 0 {
			c.bandE[i] = 1e-12
		}
	}
	return c
}

// run codes the case with the given pulse cache tables: nil tables take the
// standard-mode functions of bands_quant_encode.go, and the static tables
// passed explicitly take the general functions.
func (c quantAllBandsEncodeCase) run(cacheIndex []int16, cacheBits []uint8) ([]byte, []byte, []celtNorm, []celtNorm, uint32) {
	x := append([]celtNorm(nil), c.x...)
	var y []celtNorm
	if c.y != nil {
		y = append([]celtNorm(nil), c.y...)
	}
	buf := make([]byte, c.storage)
	var re rangecoding.Encoder
	re.Init(buf)
	for i := 0; i < c.prefix; i++ {
		re.EncodeBit(i&1, 2)
	}
	seed := c.seed
	var scratch bandEncodeScratch
	collapse := quantAllBandsEncodeScratchWithMode(&re, c.channels, len(c.x), c.lm, 0, c.end,
		x, y, c.pulses, c.shortBlocks, c.spread, 0, c.dualStereo, c.intensity,
		c.tfRes, c.totalBitsQ3, c.balance, c.codedBands, c.disableInv, &seed, c.complexity,
		c.bandE, nil, nil, &scratch, nil, nil, cacheIndex, cacheBits)
	collapse = append([]byte(nil), collapse...)
	return re.Done(), collapse, x, y, seed
}

// TestQuantAllBandsEncodeStandardPathMatchesGeneral checks the standard-mode
// encoder band functions (quantBandStereoEnc, quantBandEnc, quantPartitionEnc,
// computeThetaEnc, algQuantEnc and the theta RDO trials) bit for bit against
// the general *WithExtBudget path on random bands, allocations and coder
// states.
func TestQuantAllBandsEncodeStandardPathMatchesGeneral(t *testing.T) {
	rng := rand.New(rand.NewSource(0x6a1d))
	for iter := range 3000 {
		c := randomQuantAllBandsEncodeCase(rng)
		gotBytes, gotCollapse, gotX, gotY, gotSeed := c.run(nil, nil)
		wantBytes, wantCollapse, wantX, wantY, wantSeed := c.run(cacheIndex50[:], cacheBits50[:])
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Fatalf("iter %d: packet differs\n got %x\nwant %x", iter, gotBytes, wantBytes)
		}
		if !bytes.Equal(gotCollapse, wantCollapse) {
			t.Fatalf("iter %d: collapse masks differ: got %v want %v", iter, gotCollapse, wantCollapse)
		}
		if gotSeed != wantSeed {
			t.Fatalf("iter %d: seed %d want %d", iter, gotSeed, wantSeed)
		}
		for i := range gotX {
			if math.Float32bits(float32(gotX[i])) != math.Float32bits(float32(wantX[i])) {
				t.Fatalf("iter %d: x[%d] = %v want %v", iter, i, gotX[i], wantX[i])
			}
		}
		for i := range gotY {
			if math.Float32bits(float32(gotY[i])) != math.Float32bits(float32(wantY[i])) {
				t.Fatalf("iter %d: y[%d] = %v want %v", iter, i, gotY[i], wantY[i])
			}
		}
	}
}

func TestQuantAllBandsEncodeStandardPathZeroAlloc(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var c quantAllBandsEncodeCase
	for c.channels != 2 || c.complexity < 8 || c.dualStereo != 0 || c.lm != 3 {
		c = randomQuantAllBandsEncodeCase(rng)
	}
	x := append([]celtNorm(nil), c.x...)
	y := append([]celtNorm(nil), c.y...)
	buf := make([]byte, c.storage)
	var re rangecoding.Encoder
	var scratch bandEncodeScratch
	run := func() {
		copy(x, c.x)
		copy(y, c.y)
		re.Init(buf)
		seed := c.seed
		quantAllBandsEncodeScratchWithMode(&re, c.channels, len(c.x), c.lm, 0, c.end,
			x, y, c.pulses, c.shortBlocks, c.spread, 0, c.dualStereo, c.intensity,
			c.tfRes, c.totalBitsQ3, c.balance, c.codedBands, c.disableInv, &seed, c.complexity,
			c.bandE, nil, nil, &scratch, nil, nil, nil, nil)
	}
	run()
	if allocs := testing.AllocsPerRun(50, run); allocs != 0 {
		t.Fatalf("quantAllBandsEncodeScratchWithMode allocates %.1f per call", allocs)
	}
}
