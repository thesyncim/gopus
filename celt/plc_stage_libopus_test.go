package celt

import (
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// seedCELTMonoPacketHex / seedCELTStereoPacketHex are the exact Opus packets
// produced by the root helper encodeAPIRateCELTPacketFrameSize(t, ch, 960): a
// single 20 ms fullband CELT-only frame at 128 kbit/s (1200 Hz / 1900 Hz sines).
// They are the seed of the overlong int16 PLC parity failure
// (TestDecodeInt16OverlongPLCRequestAPIRatePCMMatchesLibopus): a 200 ms nil PLC
// request follows, whose concealment first diverges in the first noise-PLC chunk
// (the 6th internal celt_decode_lost call, prefilter_and_fold index 0).
const seedCELTMonoPacketHex = "f89ecc3c1abc39107aac7c6f0f14812ad56396763f3811fbbcdeb4484cc2341f57f4397aebf1ae9cd2861f0b00c65fd6d1bc1ade33026844ea9e43cb87cd45b73eedcafdd10e92bae665c74952f8d9e7b0d52290a322b6e7f9f08c2015ec7760e37b614827315ae6237d4ac567edabc589879eb69ae19d328617ed4e3a42650257780886508000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000021762d14e1499436820b31dda2decbc315253e77eecbd3bacb74598abdab638c171dc7d35e8a43faa87b0fd117028815c48573e2ee0ec143f26fc4896a9696ed5817e22fc65ce0ff2b1d5295f810d341739eb5a139c9"

var libopusCELTPLCStageTraceHelper libopustest.HelperCache

func buildLibopusCELTPLCStageTraceHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT PLC stage trace",
		OutputBase:  "gopus_libopus_celt_plc_stage_trace",
		SourceFile:  "libopus_celt_plc_stage_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type libopusCELTPLCStageTrace struct {
	n        int
	channels int
	overlap  int
	preSpec  [][]float32
	spec     [][]float32
	combIn   [][]float32
	combOut  [][]float32
	fold     [][]float32
	presyn   [][]float32
	final    []float32
}

// traceLibopusCELTPLCStage drives opus_decode_float() over the seed packet then
// a single overlong nil PLC request (requestedFrameSize samples), capturing the
// concealment stage buffers of the noise-PLC chunk at prefilter_and_fold index
// targetFold.
func traceLibopusCELTPLCStage(t *testing.T, sampleRate, channels, frameSize, requestedFrameSize, targetFold, targetChunk int, seed []byte) *libopusCELTPLCStageTrace {
	t.Helper()
	binPath, err := libopusCELTPLCStageTraceHelper.Path(buildLibopusCELTPLCStageTraceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT PLC stage trace", err)
	}
	packets := [][]byte{seed, nil}
	payload := libopustest.NewOraclePayload("GCLI",
		uint32(sampleRate), uint32(channels), uint32(frameSize), uint32(requestedFrameSize),
		uint32(targetFold), uint32(targetChunk), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec = 0
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT PLC stage trace", "GCLO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT PLC stage trace", err)
	}
	n := int(reader.U32())
	cc := int(reader.U32())
	ov := int(reader.U32())
	cinlen := int(reader.U32())
	presynIdx := int(reader.U32())
	foldIdx := int(reader.U32())
	if presynIdx < cc || foldIdx < cc {
		t.Fatalf("libopus PLC stage trace captured presyn=%d fold=%d /%d channels", presynIdx, foldIdx, cc)
	}
	trace := &libopusCELTPLCStageTrace{n: n, channels: cc, overlap: ov}
	trace.preSpec = make([][]float32, cc)
	trace.spec = make([][]float32, cc)
	trace.combIn = make([][]float32, cc)
	trace.combOut = make([][]float32, cc)
	trace.fold = make([][]float32, cc)
	trace.presyn = make([][]float32, cc)
	trace.final = make([]float32, n*cc)
	reader.ExpectRemaining((cc*n + cc*n + cc*cinlen + cc*ov + cc*ov + cc*n + cc*n) * 4)
	for ch := 0; ch < cc; ch++ {
		trace.preSpec[ch] = make([]float32, n)
		for i := range trace.preSpec[ch] {
			trace.preSpec[ch][i] = reader.Float32()
		}
	}
	for ch := 0; ch < cc; ch++ {
		trace.spec[ch] = make([]float32, n)
		for i := range trace.spec[ch] {
			trace.spec[ch][i] = reader.Float32()
		}
	}
	for ch := 0; ch < cc; ch++ {
		trace.combIn[ch] = make([]float32, cinlen)
		for i := range trace.combIn[ch] {
			trace.combIn[ch][i] = reader.Float32()
		}
	}
	for ch := 0; ch < cc; ch++ {
		trace.combOut[ch] = make([]float32, ov)
		for i := range trace.combOut[ch] {
			trace.combOut[ch][i] = reader.Float32()
		}
	}
	for ch := 0; ch < cc; ch++ {
		trace.fold[ch] = make([]float32, ov)
		for i := range trace.fold[ch] {
			trace.fold[ch][i] = reader.Float32()
		}
	}
	for ch := 0; ch < cc; ch++ {
		trace.presyn[ch] = make([]float32, n)
		for i := range trace.presyn[ch] {
			trace.presyn[ch][i] = reader.Float32()
		}
	}
	for i := range trace.final {
		trace.final[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}

// TestCELTPLCStagesMatchLibopusC localises the residual overlong int16 PLC
// parity drift. It replays the seed CELT frame plus PLC chunks through the celt
// decoder, capturing the concealment stages (post prefilter_and_fold overlap;
// post-synthesis pre-postfilter time buffer) of the first noise-PLC chunk and
// comparing each bit-exactly against the libopus C reference.
func TestCELTPLCStagesMatchLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate         = 48000
		frameSize          = 960
		requestedFrameSize = 9600 // 200 ms, the overlong PLC request.
		targetFold         = 0    // first noise-PLC chunk consumes pending fold.
		targetChunk        = 5    // chunks 0..4 periodic, chunk 5 first noise.
		plcChunks          = 6    // decode through the first noise chunk.
	)

	cases := []struct {
		channels int
		seedHex  string
	}{
		{1, seedCELTMonoPacketHex},
		{2, seedCELTStereoPacketHex},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("ch_"+itoaChN(tc.channels), func(t *testing.T) {
			seed, err := hex.DecodeString(tc.seedHex)
			if err != nil {
				t.Fatalf("decode seed hex: %v", err)
			}
			celtPayload := seed[1:]

			trace := traceLibopusCELTPLCStage(t, sampleRate, tc.channels, frameSize, requestedFrameSize, targetFold, targetChunk, seed)
			if trace.channels != tc.channels || trace.n != frameSize {
				t.Fatalf("trace channels=%d n=%d want %d/%d", trace.channels, trace.n, tc.channels, frameSize)
			}

			dec := NewDecoder(tc.channels)
			if err := dec.SetAPISampleRate(sampleRate); err != nil {
				t.Fatalf("SetAPISampleRate: %v", err)
			}
			dec.SetBandwidth(CELTFullband)
			stage := dec.EnablePLCStageTrace(targetFold)

			out := make([]float32, frameSize*tc.channels)
			if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(celtPayload, frameSize, tc.channels == 2, out); err != nil {
				t.Fatalf("decode seed frame: %v", err)
			}
			for c := 0; c < plcChunks; c++ {
				if _, err := dec.DecodeFrame(nil, frameSize); err != nil {
					t.Fatalf("PLC chunk %d: %v", c, err)
				}
			}
			if !stage.Captured() {
				t.Fatal("gopus PLC stage trace did not capture (path mismatch)")
			}

			// prespec (renormalised noise) is only written for the coded bands
			// [start:effEnd]; beyond the band bound gopus clears the tail while
			// libopus leaves the (unused) X scratch untouched, so compare only the
			// active region, taken as the leading non-zero extent of the
			// (denormalised) spectrum.
			for ch := 0; ch < tc.channels; ch++ {
				active := specActiveLen(trace.spec[ch])
				gp := stage.PreSpec(ch)
				wp := trace.preSpec[ch]
				if active > len(gp) {
					active = len(gp)
				}
				if active > len(wp) {
					active = len(wp)
				}
				assertFloat32BitExact(t, "prespec/ch"+itoaChN(ch), gp[:active], wp[:active])
			}
			for ch := 0; ch < tc.channels; ch++ {
				assertFloat32BitExact(t, "spec/ch"+itoaChN(ch), stage.Spec(ch), trace.spec[ch])
			}
			for ch := 0; ch < tc.channels; ch++ {
				assertFloat32BitExact(t, "combin/ch"+itoaChN(ch), stage.CombIn(ch), trace.combIn[ch])
			}
			for ch := 0; ch < tc.channels; ch++ {
				assertFloat32BitExact(t, "combout/ch"+itoaChN(ch), stage.CombOut(ch), trace.combOut[ch])
			}
			for ch := 0; ch < tc.channels; ch++ {
				assertFloat32BitExact(t, "fold/ch"+itoaChN(ch), stage.Fold(ch), trace.fold[ch])
			}
			for ch := 0; ch < tc.channels; ch++ {
				assertFloat32BitExact(t, "presyn/ch"+itoaChN(ch), stage.PreSyn(ch), trace.presyn[ch])
			}
			assertFloat32BitExact(t, "final", stage.Final(), trace.final)
		})
	}
}

// specActiveLen returns the leading contiguous count of non-zero spectrum
// samples (the coded-band region; the denormalised tail is cleared).
func specActiveLen(spec []float32) int {
	n := 0
	for i, v := range spec {
		if v != 0 {
			n = i + 1
		}
	}
	return n
}

func itoaChN(n int) string {
	if n == 0 {
		return "0"
	}
	if n == 1 {
		return "1"
	}
	if n == 2 {
		return "2"
	}
	return "?"
}
