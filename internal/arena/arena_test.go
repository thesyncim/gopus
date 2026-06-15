package arena

import "testing"

func TestBumpAllocLenCap(t *testing.T) {
	var b Bump[float32]
	b.Ensure(10)
	a := b.Alloc(3)
	c := b.Alloc(7)
	if len(a) != 0 || cap(a) != 3 {
		t.Fatalf("a: got len=%d cap=%d, want 0/3", len(a), cap(a))
	}
	if len(c) != 0 || cap(c) != 7 {
		t.Fatalf("c: got len=%d cap=%d, want 0/7", len(c), cap(c))
	}
	if b.Used() != 10 {
		t.Fatalf("used: got %d, want 10", b.Used())
	}
}

func TestBumpNonOverlap(t *testing.T) {
	var b Bump[float32]
	b.Ensure(10)
	a := b.Alloc(3)[:3]
	c := b.Alloc(7)[:7]
	for i := range a {
		a[i] = 1
	}
	for i := range c {
		c[i] = 2
	}
	// If the slots overlapped, writing c would have clobbered a.
	for i := range a {
		if a[i] != 1 {
			t.Fatalf("a[%d]=%v: slot overlap detected", i, a[i])
		}
	}
	for i := range c {
		if c[i] != 2 {
			t.Fatalf("c[%d]=%v: slot overlap detected", i, c[i])
		}
	}
}

func TestBumpClearConfinement(t *testing.T) {
	var b Bump[float32]
	b.Ensure(10)
	a := b.Alloc(3)
	m := b.Alloc(4)
	z := b.Alloc(3)
	// Fill every slot to capacity with a sentinel.
	for _, s := range [][]float32{a[:cap(a)], m[:cap(m)], z[:cap(z)]} {
		for i := range s {
			s[i] = 7
		}
	}
	// Clearing the middle slot to its capacity must not touch neighbours.
	clear(m[:cap(m)])
	for i, v := range a[:cap(a)] {
		if v != 7 {
			t.Fatalf("a[%d]=%v: clear bled left", i, v)
		}
	}
	for i, v := range z[:cap(z)] {
		if v != 7 {
			t.Fatalf("z[%d]=%v: clear bled right", i, v)
		}
	}
	for i, v := range m[:cap(m)] {
		if v != 0 {
			t.Fatalf("m[%d]=%v: clear incomplete", i, v)
		}
	}
}

func TestBumpGrowResetsOffsetAndReplacesBacking(t *testing.T) {
	var b Bump[float32]
	b.Ensure(4)
	_ = b.Alloc(4)
	if b.Used() != 4 {
		t.Fatalf("used after first carve: got %d, want 4", b.Used())
	}
	b.Ensure(100) // forces a larger backing
	if b.Used() != 0 {
		t.Fatalf("Ensure did not rewind offset: got %d", b.Used())
	}
	if b.Cap() < 100 {
		t.Fatalf("backing did not grow: cap=%d", b.Cap())
	}
	got := b.Alloc(100)
	if cap(got) != 100 {
		t.Fatalf("post-grow Alloc cap: got %d, want 100", cap(got))
	}
}

func TestBumpReuseDoesNotAllocate(t *testing.T) {
	var b Bump[float32]
	b.Ensure(10) // prime the backing once
	allocs := testing.AllocsPerRun(200, func() {
		b.Ensure(10)
		_ = b.Alloc(3)
		_ = b.Alloc(7)
	})
	if allocs != 0 {
		t.Fatalf("steady-state reuse allocated %v objects/op, want 0", allocs)
	}
}

func TestBumpOverrunPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Alloc beyond Ensure'd total did not panic")
		}
	}()
	var b Bump[float32]
	b.Ensure(5)
	_ = b.Alloc(3)
	_ = b.Alloc(4) // 3+4 > 5 -> must panic
}

func TestBumpAllocN(t *testing.T) {
	var b Bump[int16]
	b.Ensure(8)
	a := b.AllocN(3)
	c := b.AllocN(5)
	if len(a) != 3 || cap(a) != 3 {
		t.Fatalf("a: got len=%d cap=%d, want 3/3", len(a), cap(a))
	}
	if len(c) != 5 || cap(c) != 5 {
		t.Fatalf("c: got len=%d cap=%d, want 5/5", len(c), cap(c))
	}
	for i := range a {
		a[i] = 1
	}
	for i := range c {
		c[i] = 2
	}
	for i := range a {
		if a[i] != 1 {
			t.Fatalf("a[%d]=%d: AllocN slot overlap", i, a[i])
		}
	}
}

// Generic over a non-float element type.
func TestBumpInt32(t *testing.T) {
	var b Bump[int32]
	b.Ensure(6)
	a := b.Alloc(2)[:2]
	c := b.Alloc(4)[:4]
	a[0], a[1] = 10, 11
	for i := range c {
		c[i] = 99
	}
	if a[0] != 10 || a[1] != 11 {
		t.Fatalf("int32 slot overlap: a=%v", a)
	}
}
