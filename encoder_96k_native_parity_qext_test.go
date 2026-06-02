//go:build gopus_qext

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// hd96kParitySine builds a native 96 kHz interleaved signal with content both
// below and above 20 kHz so the extension bands are populated.
func hd96kParitySine(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := 0.4*math.Sin(2*math.Pi*6000*float64(i)/96000) +
			0.2*math.Sin(2*math.Pi*30000*float64(i)/96000)
		pcm[i*channels] = float32(s)
		if channels == 2 {
			pcm[i*channels+1] = float32(0.3 * s)
		}
	}
	return pcm
}

// refMainCELTPayload extracts the main CELT payload and the QEXT extension
// payload from a native 96 kHz code-3 reference Opus packet carrying a single
// CBR frame. Returns (mainPayload, qextPayload).
func refMainCELTPayload(t *testing.T, pkt []byte) (main, qext []byte) {
	t.Helper()
	if len(pkt) < 2 {
		t.Fatalf("ref packet too short: %d", len(pkt))
	}
	if pkt[0]&0x03 != 3 {
		t.Fatalf("ref packet not code 3: toc=0x%02x", pkt[0])
	}
	fc := pkt[1]
	vbr := fc&0x80 != 0
	hasPad := fc&0x40 != 0
	m := int(fc & 0x3f)
	if m != 1 {
		t.Fatalf("expected single frame, got m=%d", m)
	}
	if vbr {
		t.Fatalf("expected CBR ref packet")
	}
	offset := 2
	padding := 0
	if hasPad {
		for {
			if offset >= len(pkt) {
				t.Fatalf("padding overran packet")
			}
			b := int(pkt[offset])
			offset++
			if b == 255 {
				padding += 254
			} else {
				padding += b
				break
			}
		}
	}
	// Single CBR frame: the frame data runs from offset to len-padding.
	end := len(pkt) - padding
	if end < offset {
		t.Fatalf("bad framing: offset=%d end=%d", offset, end)
	}
	main = pkt[offset:end]

	if hasPad && padding > 0 {
		var payloads [maxRepacketizerFrames][]byte
		collectQEXTPacketExtensions(pkt[len(pkt)-padding:], m, qextPacketExtensionID, &payloads)
		qext = payloads[0]
	}
	return main, qext
}

// TestHD96kNativeEncodeMainPayloadParity compares the gopus native 96 kHz CELT
// main payload (and QEXT extension payload) against the QEXT libopus reference.
//
// Status: the threaded overlap=240 analysis MDCT, the 2-tap HD pre-emphasis and
// the Fs=96000 bitrate/QEXT-reservation budget reproduce the reference's early
// frame structure. The analysis-side comb prefilter now runs at the HD scale
// (run_prefilter max_period = QEXT_SCALE(COMBFILTER_MAXPERIOD) = 2048,
// min_period = 2*COMBFILTER_MINPERIOD, pitch_index /= qext_scale; see
// celt/prefilter.go), so the encoded postfilter octave/pitch/qg/tapset are now
// bit-exact vs the reference (mono: silence/postfilter flags + pitch params all
// match through ec_tell=12). The extra-band quant_all_bands now also receives
// the signed ext_balance (no clamp), mirroring the decode side.
//
// The native HD96k analysis MDCT and band-energy bin scaling are now wired into
// the encode path: EncodeFrame drives the overlap=240 long/short forward MDCT at
// the native 3840/480 transform lengths (computeMDCTWithHistory* now honour the
// passed overlap instead of the 48 kHz package constant), and band energies use
// the libopus bin multiplier M=1<<LM (eBands[i]*M) instead of frameSize/120,
// which mis-scaled the HD bin edges by 2x. With those in place:
//   - the QEXT packet-space reservation now reserves qext_bytes=21 (payload 20)
//     for both mono and stereo CBR @256k (mono main payload is 616 like stereo),
//     because the corrected analysis feeds the right tell/tot_boost into the CBR
//     compute_vbr() pivot, and
//   - the coarse-energy intra decision now matches (stereo intra=1), and stereo
//     coarse band energies decode bit-identically to the reference.
//
// The band-data analysis front-end normalises with the libopus bin multiplier
// M=1<<LM (band edges eBands[i]*M), not frameSize/120, which doubled the per-band
// bin reach at the HD scale and corrupted the normalised spectrum feeding
// tf_analysis/spreading_decision/alloc_trim/quant_all_bands. With that fixed the
// TF resolution, spreading, alloc-trim, intensity, dual-stereo and coded-band
// allocation now match the reference, and the stereo PVQ band data is bit-exact
// through band 15.
//
// Native-96k encode is byte-exact for both mono and stereo. The HD-scale comb
// prefilter (comb_filter_qext, x!=y) filters the even/odd phases with the input
// delay line (mem_buf) and the output buffer kept SEPARATE, so an already-written
// output sample is never read back as comb input. The forward MDCT folds the
// 1/nfft FFT scale into the post-rotation twiddles under the ENABLE_QEXT scale
// placement (mdctQEXTScalePlacement), matching the QEXT clt_mdct_forward(); the
// pre-rotation placement of the default build rounds the >20 kHz extension bins
// by tens of ULP, which had flipped the band-16 stereo PVQ fold leaf.
//
// The test logs the first divergence and fails on any byte mismatch.
func TestHD96kNativeEncodeMainPayloadParity(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       bitrate,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 8000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			refMain, refQext := refMainCELTPayload(t, res.Packets[0])

			e := celt.NewEncoder(ch)
			// QEXT path: libopus copies raw PCM into the CELT buffer (no dc_reject,
			// no delay compensation). RESTRICTED_LOWDELAY only forces CELT-only; it
			// does NOT disable the prefilter, so leave CELT prediction at default.
			e.SetDCRejectEnabled(false)
			e.SetLSBQuantizationEnabled(false)
			e.SetDelayCompensationEnabled(false)
			e.SetVBR(false)
			e.SetComplexity(10)
			e.SetBitrate(bitrate)
			e.SetLSBDepth(24)
			e.SetQEXTEnabled(true)
			e.EnableHD96kMode()

			gotMain, err := e.EncodeFrame(pcm, frameSize)
			if err != nil {
				t.Fatalf("ch=%d: EncodeFrame: %v", ch, err)
			}
			gotQext := e.LastQEXTPayload()

			if len(gotMain) != len(refMain) {
				t.Logf("ch=%d main byte budget got=%d ref=%d", ch, len(gotMain), len(refMain))
			}
			diagFlags(t, "got", gotMain)
			diagFlags(t, "ref", refMain)

			firstMainDiff := firstByteDiff(gotMain, refMain)
			firstQextDiff := firstByteDiff(gotQext, refQext)
			// Both mono and stereo native-96k encode are byte-exact (main + QEXT);
			// any mismatch is a real correctness regression.
			if firstMainDiff >= 0 {
				dumpAround(t, "main", gotMain, refMain, firstMainDiff)
				t.Fatalf("ch=%d main payload diverges at byte %d (got len=%d ref len=%d)",
					ch, firstMainDiff, len(gotMain), len(refMain))
			}
			if firstQextDiff >= 0 {
				dumpAround(t, "qext", gotQext, refQext, firstQextDiff)
				t.Fatalf("ch=%d qext payload diverges at byte %d (got len=%d ref len=%d)",
					ch, firstQextDiff, len(gotQext), len(refQext))
			}
		})
	}
}

func diagFlags(t *testing.T, label string, payload []byte) {
	t.Helper()
	var d rangecoding.Decoder
	d.Init(payload)
	silence := d.DecodeBit(15)
	pf := d.DecodeBit(1)
	transient := d.DecodeBit(3)
	intra := d.DecodeBit(3)
	t.Logf("%s flags: silence=%d postfilter=%d transient=%d intra=%d tell=%d",
		label, silence, pf, transient, intra, d.Tell())
}

func dumpAround(t *testing.T, label string, got, ref []byte, at int) {
	t.Helper()
	lo := at - 4
	if lo < 0 {
		lo = 0
	}
	hiG := at + 8
	if hiG > len(got) {
		hiG = len(got)
	}
	hiR := at + 8
	if hiR > len(ref) {
		hiR = len(ref)
	}
	t.Logf("%s got[%d:%d]=% x", label, lo, hiG, got[lo:hiG])
	t.Logf("%s ref[%d:%d]=% x", label, lo, hiR, ref[lo:hiR])
}
