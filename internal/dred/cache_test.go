package dred

import "testing"

func TestCacheStoreResultAndClear(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 8, -4)
	buf := make([]byte, MaxDataSize)
	var cache Cache

	if err := cache.Store(buf, payload, 8); err != nil {
		t.Fatalf("Cache.Store error: %v", err)
	}
	if cache.Len != len(payload) {
		t.Fatalf("Cache.Len=%d want %d", cache.Len, len(payload))
	}
	if cache.Parsed.Header.DredOffset != -4 || cache.Parsed.Header.DredFrameOffset != 8 {
		t.Fatalf("Cache.Parsed.Header offsets=(%d,%d) want (-4,8)", cache.Parsed.Header.DredOffset, cache.Parsed.Header.DredFrameOffset)
	}

	result := cache.Result(Request{MaxDREDSamples: 960, SampleRate: 48000})
	if result.Availability.AvailableSamples != 480 {
		t.Fatalf("Result.Availability.AvailableSamples=%d want 480", result.Availability.AvailableSamples)
	}

	cache.Clear()
	if cache != (Cache{}) {
		t.Fatalf("Cache after Clear=%+v want zero state", cache)
	}
}

func TestCacheStoreRejectsSmallBuffer(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 0, 4)
	var cache Cache

	if err := cache.Store(make([]byte, len(payload)-1), payload, 0); err == nil {
		t.Fatal("Cache.Store error=nil want non-nil")
	}
}
