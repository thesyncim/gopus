package rangecoding

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestSaveStateShallowMatchesFullForShallowRestore checks that a shallow save
// restores the same encoder as a full save when both go through
// RestoreStateShallow, and that neither restore touches the packet bytes.
func TestSaveStateShallowMatchesFullForShallowRestore(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5a11))
	for iter := 0; iter < 200; iter++ {
		size := 16 + rng.Intn(200)
		bufA := make([]byte, size)
		bufB := make([]byte, size)
		var a, b Encoder
		a.Init(bufA)
		b.Init(bufB)
		step := func(e *Encoder, r *rand.Rand) {
			for range 1 + r.Intn(12) {
				switch r.Intn(3) {
				case 0:
					e.EncodeBit(r.Intn(2), uint(1+r.Intn(8)))
				case 1:
					ft := uint32(2 + r.Intn(200))
					fl := uint32(r.Intn(int(ft)))
					e.Encode(fl, fl+1, ft)
				default:
					e.EncodeRawBits(uint32(r.Intn(16)), 4)
				}
			}
		}
		seed := rng.Int63()
		step(&a, rand.New(rand.NewSource(seed)))
		step(&b, rand.New(rand.NewSource(seed)))

		var full, shallow EncoderState
		a.SaveStateInto(&full)
		b.SaveStateShallowInto(&shallow)
		trial := rng.Int63()
		step(&a, rand.New(rand.NewSource(trial)))
		step(&b, rand.New(rand.NewSource(trial)))
		a.RestoreStateShallow(&full)
		b.RestoreStateShallow(&shallow)
		if !bytes.Equal(bufA, bufB) {
			t.Fatalf("iter %d: packet bytes differ after shallow restore", iter)
		}
		tail := rng.Int63()
		step(&a, rand.New(rand.NewSource(tail)))
		step(&b, rand.New(rand.NewSource(tail)))
		if !bytes.Equal(a.Done(), b.Done()) || a.Range() != b.Range() {
			t.Fatalf("iter %d: encoders diverge after shallow restore", iter)
		}
	}
}

// TestSaveStateSinceRestoresFirstTrial runs the theta RDO pattern: a shallow
// save, a first trial kept with SaveStateSinceInto, a shallow restore and a
// second trial, then RestoreState of the first trial. The finished packet and
// range must equal coding the first trial alone.
func TestSaveStateSinceRestoresFirstTrial(t *testing.T) {
	rng := rand.New(rand.NewSource(0x51ce))
	step := func(e *Encoder, r *rand.Rand) {
		for range 1 + r.Intn(12) {
			switch r.Intn(3) {
			case 0:
				e.EncodeBit(r.Intn(2), uint(1+r.Intn(8)))
			case 1:
				ft := uint32(2 + r.Intn(200))
				fl := uint32(r.Intn(int(ft)))
				e.Encode(fl, fl+1, ft)
			default:
				e.EncodeRawBits(uint32(r.Intn(16)), 4)
			}
		}
	}
	for iter := 0; iter < 500; iter++ {
		size := 16 + rng.Intn(200)
		bufA := make([]byte, size)
		bufB := make([]byte, size)
		var a, b Encoder
		a.Init(bufA)
		b.Init(bufB)
		head, trial1, trial2, tail := rng.Int63(), rng.Int63(), rng.Int63(), rng.Int63()
		step(&a, rand.New(rand.NewSource(head)))
		step(&b, rand.New(rand.NewSource(head)))

		var start, kept EncoderState
		b.SaveStateShallowInto(&start)
		step(&b, rand.New(rand.NewSource(trial1)))
		b.SaveStateSinceInto(&kept, &start)
		b.RestoreStateShallow(&start)
		step(&b, rand.New(rand.NewSource(trial2)))
		b.RestoreState(&kept)

		step(&a, rand.New(rand.NewSource(trial1)))
		step(&a, rand.New(rand.NewSource(tail)))
		step(&b, rand.New(rand.NewSource(tail)))
		if !bytes.Equal(a.Done(), b.Done()) || a.Range() != b.Range() || a.Error() != b.Error() {
			t.Fatalf("iter %d: restored first trial differs from coding it alone", iter)
		}
	}
}
