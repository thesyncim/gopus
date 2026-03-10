package celt

import "testing"

var pulseCacheBenchSink int

func TestBitsToPulsesCachedFastMatchesBinarySearch(t *testing.T) {
	var seen [len(cacheBits50)]bool
	for _, start16 := range cacheIndex50 {
		start := int(start16)
		if start < 0 || seen[start] {
			continue
		}
		seen[start] = true
		cache := cacheBits50[start:]
		maxBits := pulseCacheMaxBits(cache)
		for bitsQ3 := 1; bitsQ3 <= maxBits+32; bitsQ3++ {
			got := bitsToPulsesCachedFast(cache, bitsQ3)
			want := bitsToPulsesCachedBinarySearch(cache, bitsQ3)
			if got != want {
				t.Fatalf("cache start=%d bitsQ3=%d got=%d want=%d", start, bitsQ3, got, want)
			}
		}
	}
}

func TestBitsToPulsesCachedFastFallbackCustomSlice(t *testing.T) {
	cache := []uint8{5, 12, 26, 41, 58, 77}
	for bitsQ3 := 1; bitsQ3 <= 96; bitsQ3++ {
		got := bitsToPulsesCachedFast(cache, bitsQ3)
		want := bitsToPulsesCachedBinarySearch(cache, bitsQ3)
		if got != want {
			t.Fatalf("custom cache bitsQ3=%d got=%d want=%d", bitsQ3, got, want)
		}
	}
}

func BenchmarkBitsToPulsesCachedFast(b *testing.B) {
	caches := make([][]uint8, 0, len(cacheIndex50))
	var seen [len(cacheBits50)]bool
	for _, start16 := range cacheIndex50 {
		start := int(start16)
		if start < 0 || seen[start] {
			continue
		}
		seen[start] = true
		caches = append(caches, cacheBits50[start:])
	}

	b.ReportAllocs()
	b.ResetTimer()
	sum := 0
	for i := 0; i < b.N; i++ {
		cache := caches[i%len(caches)]
		sum += bitsToPulsesCachedFast(cache, (i&255)+1)
	}
	pulseCacheBenchSink = sum
}

func BenchmarkBitsToPulsesCachedBinarySearch(b *testing.B) {
	caches := make([][]uint8, 0, len(cacheIndex50))
	var seen [len(cacheBits50)]bool
	for _, start16 := range cacheIndex50 {
		start := int(start16)
		if start < 0 || seen[start] {
			continue
		}
		seen[start] = true
		caches = append(caches, cacheBits50[start:])
	}

	b.ReportAllocs()
	b.ResetTimer()
	sum := 0
	for i := 0; i < b.N; i++ {
		cache := caches[i%len(caches)]
		sum += bitsToPulsesCachedBinarySearch(cache, (i&255)+1)
	}
	pulseCacheBenchSink = sum
}
