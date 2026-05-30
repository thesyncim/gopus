//go:build gopus_fixedpoint

package gopus

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/rangecoding"
)

// celtDecodeFixedAPIRate runs the FIXED_POINT integer CELT decoder
// (internal/fixedpoint.CELTDecoder) for a CELT-only frame and accumulates its
// libopus-exact int16 (Res2Int16) and int24 (opus_res; RES2INT24(a)==a) output
// for the in-flight DecodeInt16 / DecodeInt24 wrapper. It runs IN ADDITION to
// the float CELT decoder (which has already filled out and advanced the float
// cross-frame state), purely to capture integer-exact PCM.
//
// It only does work when an integer-output packet is active (beginFixedPacket
// armed the accumulation). The float Decode path never arms one, so this is a
// no-op there. A degenerate (len(data) <= 1, DTX/PLC) frame is declined so the
// float conversion is used for that packet.
//
// It returns handled=true when it accumulated a frame.
func (d *Decoder) celtDecodeFixedAPIRate(data []byte, apiFrameSize int, packetStereo bool, celtBW celt.CELTBandwidth, out []float32) (bool, error) {
	if !d.fixedPacketActive {
		return false, nil
	}
	if len(data) <= 1 {
		return false, nil
	}
	channels := int(d.channels)
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(channels, int(d.sampleRate))
	}

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := apiFrameSize * downsample

	// CELT-only frames cover bands [0, EffectiveBands(bw)).
	d.fixedCELT.SetBandRange(0, celtBW.EffectiveBands())

	needed := apiFrameSize * channels

	int16Out := d.fixedCELTScratch(needed)
	d.fixedCELT.DecodeWithEC(data, coreFrameSize, int16Out)
	res := d.fixedCELT.LastRes()

	// Stash the integer-exact output for the int16/int24 wrappers.
	d.appendFixedOutput(int16Out[:needed], res[:needed])
	return true, nil
}

// prepareFixedHybrid arms the integer hybrid highband hook for the in-flight
// frame and records the CELT end band and reset policy, mirroring the libopus
// opus_decode_frame hybrid CELT path. It is called from the Hybrid dispatch
// before the float hybrid decode runs (which drives the SILK lowband and then
// invokes the hook). It returns false when the integer path is declined (no
// active integer packet, or a degenerate/PLC frame), in which case the caller
// marks the packet unhandled and uses the float conversion.
func (d *Decoder) prepareFixedHybrid(data []byte, celtBW celt.CELTBandwidth, needCeltReset bool) bool {
	if !d.fixedPacketActive || len(data) <= 1 {
		return false
	}
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	d.fixedHybridEnd = celtBW.EffectiveBands()
	d.fixedHybridReset = needCeltReset
	d.fixedHybridErr = nil
	d.hybridDecoder.SetFixedHighband(d)
	return true
}

// finishFixedHybrid disarms the hook and reports whether the integer hybrid
// highband decode produced a frame the int16/int24 wrappers can read directly.
func (d *Decoder) finishFixedHybrid() error {
	d.hybridDecoder.SetFixedHighband(nil)
	return d.fixedHybridErr
}

// DecodeHybridHighband implements hybrid.FixedHybridHighband. It builds the
// opus_res SILK lowband (INT16TORES: int16 << RES_SHIFT) from the resampled int16
// SILK output, then accumulates the integer CELT highband (start band 17) onto it
// from the cloned shared range decoder, matching libopus celt_decode_with_ec_dred
// with celt_accum=1. The combined opus_res / int16 output is stashed for the
// DecodeInt16 / DecodeInt24 wrappers.
func (d *Decoder) DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, frameSizeAPI, frameSize48 int, packetStereo bool) {
	channels := int(d.channels)
	needed := frameSizeAPI * channels

	if cap(d.fixedHybridRes) < needed {
		d.fixedHybridRes = make([]int32, needed)
	}
	res := d.fixedHybridRes[:needed]
	// INT16TORES(a) = SHL32(EXTEND32(a), RES_SHIFT); RES_SHIFT == 8.
	for i := 0; i < needed; i++ {
		var s int16
		if i < filled && i < len(silkInt16) {
			s = silkInt16[i]
		}
		res[i] = int32(s) << 8
	}

	if d.fixedHybridReset {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSizeAPI * downsample

	d.fixedCELT.DecodeHybridAccum(rd, coreFrameSize, res)

	if cap(d.fixedHybridInt16) < needed {
		d.fixedHybridInt16 = make([]int16, needed)
	}
	int16Out := d.fixedHybridInt16[:needed]
	for i := 0; i < needed; i++ {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}

	d.appendFixedOutput(int16Out, res)
}

// beginFixedPacket arms the per-packet integer-output accumulation used by the
// DecodeInt16 / DecodeInt24 wrappers. It must be paired with a read of
// fixedAllHandled / fixedInt16 / fixedRes after the float decode completes.
func (d *Decoder) beginFixedPacket() {
	// A non-zero decode gain is applied to the float output only; the integer
	// path does not reimplement the FIXED_POINT gain stage, so decline the
	// direct accumulation and let the float conversion handle these packets.
	if d.decodeGainQ8 != 0 {
		d.fixedPacketActive = false
		d.fixedAllHandled = false
		return
	}
	d.fixedPacketActive = true
	d.fixedAllHandled = true
	d.fixedCursor = 0
	d.fixedInt16 = d.fixedInt16[:0]
	d.fixedRes = d.fixedRes[:0]
}

// endFixedPacket disarms the accumulation. fixedAllHandled stays valid for the
// caller to inspect immediately after.
func (d *Decoder) endFixedPacket() {
	d.fixedPacketActive = false
}

// appendFixedOutput records one frame's int16 and opus_res output at the
// running packet cursor.
func (d *Decoder) appendFixedOutput(int16Out []int16, res []int32) {
	d.fixedInt16 = append(d.fixedInt16, int16Out...)
	d.fixedRes = append(d.fixedRes, res...)
	d.fixedCursor += len(int16Out)
}

// markFixedUnhandled records that the in-flight packet contains a frame the
// integer CELT path did not produce (a SILK/Hybrid frame, a float CELT
// fallback, or a concealment frame), so the int16/int24 wrappers must use the
// float conversion for this packet.
func (d *Decoder) markFixedUnhandled() {
	d.fixedAllHandled = false
}

// fixedInt16Ready reports whether the just-decoded packet was produced entirely
// by the integer CELT path and the accumulated int16 output covers exactly the
// expected interleaved length.
func (d *Decoder) fixedInt16Ready(needed int) bool {
	return d.fixedPacketActive && d.fixedAllHandled && d.fixedCursor == needed && len(d.fixedInt16) == needed
}

// finishInt16Output writes n*channels int16 PCM samples into pcm. When the
// just-decoded packet was produced entirely by the integer CELT path, the
// libopus-exact int16 output (Res2Int16 of each opus_res sample) is copied
// directly, avoiding the lossy float32->int16 conversion. Otherwise it falls
// back to the shared float path (soft clip + float32ToInt16), exactly as the
// default build does. It returns true when the integer path was used.
func (d *Decoder) finishInt16Output(pcm []int16, scratch []float32, n, channels int) bool {
	needed := n * channels
	if d.fixedInt16Ready(needed) {
		copy(pcm[:needed], d.fixedInt16[:needed])
		return true
	}
	softClipAndFloat32ToInt16(pcm, scratch, n, channels, d.softClipMem[:])
	return false
}

// finishInt24Output writes n*channels int24 PCM samples (right-justified in
// int32) into pcm. When the just-decoded packet was produced entirely by the
// integer CELT path, the opus_res output is copied directly: for the
// FIXED_POINT ENABLE_RES24 build RES2INT24(a) == a, so the opus_res value is
// the int24 sample. Otherwise it falls back to the shared float path
// (float32ToInt24), exactly as the default build does. Returns true when the
// integer path was used.
func (d *Decoder) finishInt24Output(pcm []int32, scratch []float32, n, channels int) bool {
	needed := n * channels
	if d.fixedInt16Ready(needed) && len(d.fixedRes) >= needed {
		copy(pcm[:needed], d.fixedRes[:needed])
		return true
	}
	float32ToInt24Slice(pcm, scratch, n, channels)
	return false
}

// resetFixedCELT clears the integer CELT decoder cross-frame state on a CELT
// mode change, mirroring the float path's d.celtDecoder.Reset().
func (d *Decoder) resetFixedCELT() {
	if d.fixedCELT != nil {
		d.fixedCELT.Reset()
	}
}

func (d *Decoder) fixedCELTScratch(n int) []int16 {
	if cap(d.fixedCELTPCM) < n {
		d.fixedCELTPCM = make([]int16, n)
		return d.fixedCELTPCM
	}
	d.fixedCELTPCM = d.fixedCELTPCM[:n]
	return d.fixedCELTPCM
}
