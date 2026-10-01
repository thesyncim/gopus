package silk

import (
	"math"
	"math/rand"
	"testing"
)

// eagerOutputHistory is the float output ring written sample by sample at
// update time, the model the deferred int16 history must reproduce.
type eagerOutputHistory struct {
	hist []float32
	idx  int
}

func (h *eagerOutputHistory) writeInt16(samples []int16) {
	for _, s := range samples {
		h.hist[h.idx] = float32(s) * (1.0 / 32768.0)
		h.idx = (h.idx + 1) % len(h.hist)
	}
}

func (h *eagerOutputHistory) writeFloat(samples []float32) {
	for _, s := range samples {
		h.hist[h.idx] = s
		h.idx = (h.idx + 1) % len(h.hist)
	}
}

// TestOutputHistoryDeferredMatchesEager drives the decoder history through
// random interleavings of int16 updates, float updates, index changes, resets
// and reads, and checks every read against the eager float ring.
func TestOutputHistoryDeferredMatchesEager(t *testing.T) {
	rng := rand.New(rand.NewSource(0x415))
	d := NewDecoder()
	ref := eagerOutputHistory{hist: make([]float32, len(d.outputHistory))}
	for step := range 20000 {
		switch op := rng.Intn(10); {
		case op < 4:
			n := 1 + rng.Intn(700)
			if rng.Intn(3) == 0 {
				n = []int{80, 160, 320}[rng.Intn(3)]
			}
			s := make([]int16, n)
			for i := range s {
				s[i] = int16(rng.Intn(65536) - 32768)
			}
			d.updateHistoryInt16(s)
			ref.writeInt16(s)
		case op < 5:
			s := make([]float32, 1+rng.Intn(40))
			for i := range s {
				s[i] = rng.Float32()*2 - 1
			}
			d.updateHistory(s)
			ref.writeFloat(s)
		case op < 6:
			idx := rng.Intn(len(ref.hist))
			d.SetHistoryIndex(idx)
			ref.idx = idx
		case op < 7 && rng.Intn(20) == 0:
			d.Reset()
			clear(ref.hist)
			ref.idx = 0
		case op < 8:
			off := 1 + rng.Intn(len(ref.hist))
			want := ref.hist[((ref.idx-off)%len(ref.hist)+len(ref.hist))%len(ref.hist)]
			if got := d.getHistorySample(off); math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("step %d: getHistorySample(%d) = %v, want %v", step, off, got, want)
			}
		default:
			got := d.OutputHistory()
			if d.HistoryIndex() != ref.idx {
				t.Fatalf("step %d: history index %d, want %d", step, d.HistoryIndex(), ref.idx)
			}
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(ref.hist[i]) {
					t.Fatalf("step %d: history[%d] = %v, want %v", step, i, got[i], ref.hist[i])
				}
			}
		}
	}
}

func TestUpdateHistoryInt16ZeroAlloc(t *testing.T) {
	d := NewDecoder()
	frame := make([]int16, 320)
	for i := range frame {
		frame[i] = int16(i * 97)
	}
	d.updateHistoryInt16(frame)
	if allocs := testing.AllocsPerRun(100, func() {
		d.updateHistoryInt16(frame)
		_ = d.OutputHistory()
	}); allocs != 0 {
		t.Fatalf("history update allocates %.1f per frame", allocs)
	}
}
