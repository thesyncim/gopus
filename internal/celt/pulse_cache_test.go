package celt

import "testing"

var pulseCacheBenchSink int

func TestBitsToPulsesCachedFastMatchesBinarySearch(t *testing.T) {
	for lm := -1; lm <= 3; lm++ {
		for band := range MaxBands {
			start := int(cacheIndex50[(lm+1)*MaxBands+band])
			cache, ok := pulseCacheForBand(band, lm)
			if start < 0 {
				if ok {
					t.Fatalf("band=%d lm=%d has cache view for invalid cache index %d", band, lm, start)
				}
				if _, tableOK := pulseCacheForBandTables(band, lm, cacheIndex50[:], cacheBits50[:], MaxBands); tableOK {
					t.Fatalf("explicit standard tables have cache view for invalid band=%d lm=%d index=%d", band, lm, start)
				}
				continue
			}
			if !ok {
				t.Fatalf("missing standard pulse cache for band=%d lm=%d", band, lm)
			}
			if cache.staticOffset != start || len(cache.bits) != len(cacheBits50)-start {
				t.Fatalf("band=%d lm=%d cache view offset=%d len=%d, want offset=%d len=%d",
					band, lm, cache.staticOffset, len(cache.bits), start, len(cacheBits50)-start)
			}
			tableCache, ok := pulseCacheForBandTables(band, lm, cacheIndex50[:], cacheBits50[:], MaxBands)
			if !ok || tableCache.staticOffset != start {
				t.Fatalf("explicit standard tables band=%d lm=%d view=(%+v, %t), want offset=%d",
					band, lm, tableCache, ok, start)
			}
			maxBits := pulseCacheMaxBits(cache)
			for bitsQ3 := 1; bitsQ3 <= maxBits+32; bitsQ3++ {
				got := bitsToPulsesCachedFast(cache, bitsQ3)
				want := bitsToPulsesCachedBinarySearch(cache.bits, bitsQ3)
				if got != want {
					t.Fatalf("cache start=%d bitsQ3=%d got=%d want=%d", start, bitsQ3, got, want)
				}
				got = bitsToPulsesCachedFast(tableCache, bitsQ3)
				if got != want {
					t.Fatalf("explicit standard cache start=%d bitsQ3=%d got=%d want=%d", start, bitsQ3, got, want)
				}
			}
		}
	}
}

func TestBitsToPulsesCachedFastFallbackCustomSlice(t *testing.T) {
	cache, ok := pulseCacheForBandTables(0, -1, []int16{0}, []uint8{5, 12, 26, 41, 58, 77}, 1)
	if !ok || cache.staticOffset != noPulseCacheLookupOffset {
		t.Fatalf("custom cache view = (%+v, %t), want binary-search view", cache, ok)
	}
	for bitsQ3 := 1; bitsQ3 <= 96; bitsQ3++ {
		got := bitsToPulsesCachedFast(cache, bitsQ3)
		want := bitsToPulsesCachedBinarySearch(cache.bits, bitsQ3)
		if got != want {
			t.Fatalf("custom cache bitsQ3=%d got=%d want=%d", bitsQ3, got, want)
		}
	}
}

func TestBitsToPulsesCachedAllocs(t *testing.T) {
	static, ok := pulseCacheForBand(7, 2)
	if !ok {
		t.Fatal("missing standard pulse cache")
	}
	custom, ok := pulseCacheForBandTables(0, -1, []int16{0}, []uint8{5, 12, 26, 41, 58, 77}, 1)
	if !ok {
		t.Fatal("missing custom pulse cache")
	}
	explicitStatic, ok := pulseCacheForBandTables(7, 2, cacheIndex50[:], cacheBits50[:], MaxBands)
	if !ok {
		t.Fatal("missing explicit standard pulse cache")
	}
	for _, tc := range []struct {
		name  string
		cache pulseCacheView
	}{{"static", static}, {"explicit static", explicitStatic}, {"custom", custom}} {
		bitsToPulsesCached(tc.cache, 17)
		allocs := testing.AllocsPerRun(1000, func() {
			pulseCacheBenchSink = bitsToPulsesCached(tc.cache, 17)
		})
		if allocs != 0 {
			t.Errorf("%s cache lookup allocs/call = %g, want 0", tc.name, allocs)
		}
	}
}

func BenchmarkBitsToPulsesCachedFast(b *testing.B) {
	caches := make([]pulseCacheView, 0, len(cacheIndex50))
	var seen [len(cacheBits50)]bool
	for _, start16 := range cacheIndex50 {
		start := int(start16)
		if start < 0 || seen[start] {
			continue
		}
		seen[start] = true
		caches = append(caches, pulseCacheView{bits: cacheBits50[start:], staticOffset: start})
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
	caches := make([]pulseCacheView, 0, len(cacheIndex50))
	var seen [len(cacheBits50)]bool
	for _, start16 := range cacheIndex50 {
		start := int(start16)
		if start < 0 || seen[start] {
			continue
		}
		seen[start] = true
		caches = append(caches, pulseCacheView{bits: cacheBits50[start:], staticOffset: start})
	}

	b.ReportAllocs()
	b.ResetTimer()
	sum := 0
	for i := 0; i < b.N; i++ {
		cache := caches[i%len(caches)]
		sum += bitsToPulsesCachedBinarySearch(cache.bits, (i&255)+1)
	}
	pulseCacheBenchSink = sum
}
