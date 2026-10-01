package libopustest

import "testing"

func TestNativeX86PitchXCorrMetadataValidatesRTCDAndPresumedPaths(t *testing.T) {
	const (
		cpuAVX2   = uint32(2)
		cpuFMA    = uint32(4)
		dispatch  = uint32(3)
		features  = cpuAVX2 | cpuFMA
		presumed  = dispatch
		avx2Only  = uint32(1)
		noPresume = uint32(0)
	)
	tests := []struct {
		name                         string
		arch, cpu, effective, assume uint32
		want                         bool
	}{
		{name: "RTCD arch 4", arch: 4, cpu: features, effective: dispatch, assume: noPresume, want: true},
		{name: "both presumed with arch zero", arch: 0, cpu: features, effective: dispatch, assume: presumed, want: true},
		{name: "both presumed with unrelated RTCD arch", arch: 7, cpu: features, effective: dispatch, assume: presumed, want: true},
		{name: "AVX2 presumed and SSE RTCD", arch: 4, cpu: features, effective: dispatch, assume: avx2Only, want: true},
		{name: "RTCD arch too low", arch: 0, cpu: features, effective: dispatch, assume: noPresume},
		{name: "partial presumed but RTCD arch too low", arch: 0, cpu: features, effective: dispatch, assume: avx2Only},
		{name: "CPU lacks FMA", arch: 4, cpu: cpuAVX2, effective: dispatch, assume: noPresume},
		{name: "effective AVX2 dispatch missing", arch: 4, cpu: features, effective: 2, assume: noPresume},
		{name: "presumed dispatch not effective", arch: 4, cpu: features, effective: 1, assume: 2},
		{name: "unknown presumed bit", arch: 4, cpu: features, effective: dispatch, assume: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NativeX86PitchXCorrMetadataValid(tc.arch, tc.cpu, tc.effective, tc.assume); got != tc.want {
				t.Fatalf("metadata valid=%t want %t: arch=%d cpu=%03b dispatch=%02b presumed=%02b", got, tc.want, tc.arch, tc.cpu, tc.effective, tc.assume)
			}
		})
	}
}

func TestNativeX86SILKInnerProductMetadataValidatesRTCDAndPresumedPaths(t *testing.T) {
	tests := []struct {
		name                   string
		arch, implementation   uint32
		presumed               uint32
		wantAVX2, wantMetadata bool
	}{
		{name: "scalar", arch: 0, implementation: 0, wantAVX2: false, wantMetadata: true},
		{name: "RTCD AVX2", arch: 4, implementation: 1, wantAVX2: true, wantMetadata: true},
		{name: "presumed AVX2 at arch zero", arch: 0, implementation: 1, presumed: 1, wantAVX2: true, wantMetadata: true},
		{name: "presumed AVX2 with other arch", arch: 7, implementation: 1, presumed: 1, wantAVX2: true, wantMetadata: true},
		{name: "RTCD AVX2 arch too low", arch: 0, implementation: 1, wantAVX2: true},
		{name: "unexpected AVX2", arch: 4, implementation: 1, wantAVX2: false},
		{name: "unknown implementation", arch: 4, implementation: 2, wantAVX2: true},
		{name: "unknown presumed bit", arch: 4, implementation: 1, presumed: 2, wantAVX2: true},
		{name: "presumed scalar", arch: 0, implementation: 0, presumed: 1, wantAVX2: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NativeX86SILKInnerProductMetadataValid(tc.arch, tc.implementation, tc.presumed, tc.wantAVX2)
			if got != tc.wantMetadata {
				t.Fatalf("metadata valid=%t want %t: arch=%d implementation=%d presumed=%d wantAVX2=%t",
					got, tc.wantMetadata, tc.arch, tc.implementation, tc.presumed, tc.wantAVX2)
			}
		})
	}
}
