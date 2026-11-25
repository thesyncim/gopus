package celt

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	spreadNone       = 0
	spreadLight      = 1
	spreadNormal     = 2
	spreadAggressive = 3
)

var orderyTable = []int{
	1, 0,
	3, 0, 2, 1,
	7, 0, 4, 3, 6, 1, 5, 2,
	15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
}

var bitInterleaveTable = []int{
	0, 1, 1, 1,
	2, 3, 3, 3,
	2, 3, 3, 3,
	2, 3, 3, 3,
}

var bitDeinterleaveTable = []int{
	0x00, 0x03, 0x0C, 0x0F,
	0x30, 0x33, 0x3C, 0x3F,
	0xC0, 0xC3, 0xCC, 0xCF,
	0xF0, 0xF3, 0xFC, 0xFF,
}

type bandCtx struct {
	rd              *rangecoding.Decoder
	re              *rangecoding.Encoder
	encode          bool
	extEnc          *rangecoding.Encoder
	extDec          *rangecoding.Decoder
	extBudget       int
	extTotalBits    int
	extraBands      bool
	bandEdges       []int
	bandLogN        []int
	cacheIndex      []int16
	cacheBits       []uint8
	bandCaps        []int32
	bandE           []celtEner
	nbBands         int
	channels        int
	spread          int
	tfChange        int
	remainingBits   int
	intensity       int
	band            int
	seed            uint32
	seedActive      bool
	resynth         bool
	disableInv      bool
	avoidSplitNoise bool
	// thetaRound controls theta quantization biasing for stereo RDO:
	//   0: normal rounding (default)
	//  -1: bias toward rounding down (toward 0 or 16384)
	//   1: bias toward rounding up (toward 8192/equal split)
	// This is used by theta RDO optimization in quant_all_bands.
	thetaRound int
	// tapset controls the window taper selection for prefilter/postfilter comb filter.
	// Values: 0 (narrow), 1 (medium), 2 (wide).
	// This is set during spreading_decision and used for comb filter taper gains.
	// While not directly used in PVQ quantization, it's tracked here for
	// encoder state consistency and future prefilter integration.
	// Reference: libopus celt/celt.c comb_filter() gains table
	tapset int
	// scratch holds pre-allocated buffers for the decode hot path.
	// This eliminates per-call allocations in algUnquant, Hadamard transforms, etc.
	scratch *bandDecodeScratch
	// encScratch holds pre-allocated buffers for the encode hot path.
	// This eliminates per-call allocations in algQuant, PVQ search, etc.
	encScratch *bandEncodeScratch
	// stdEnc selects the bands_quant_encode.go functions: an encoder with
	// the standard mode tables, a bandEncodeScratch and no extension coder.
	stdEnc bool
	// rdoItheta is the stereo_itheta() of the band a theta_rdo pair codes,
	// kept from the first trial for the second while rdoIthetaSet.
	rdoItheta    int
	rdoIthetaSet bool
	// The decoder's quant_all_bands() loop sets these for its quant_band()
	// calls: the frame LM and block count, the lowband_scratch of the
	// current band, and stdCache when the band tables are the standard
	// mode's, whose pulse caches have precomputed lookups.
	lm             int
	blocks         int
	lowbandScratch []celtNorm
	stdCache       bool
}

type splitCtx struct {
	inv       int
	imid      int
	iside     int
	delta     int
	itheta    int
	ithetaQ30 int // Extended precision theta in Q30 format (when QEXT enabled)
	qalloc    int
}

func (ctx *bandCtx) bandEnergy(channel int) celtEner {
	if ctx.bandE == nil || ctx.nbBands <= 0 || channel < 0 || channel >= ctx.channels {
		return 0
	}
	idx := channel*ctx.nbBands + ctx.band
	if idx < 0 || idx >= len(ctx.bandE) {
		return 0
	}
	return ctx.bandE[idx]
}

func (ctx *bandCtx) modeBandEdges() []int {
	if len(ctx.bandEdges) >= 2 {
		return ctx.bandEdges
	}
	return EBands[:]
}

func (ctx *bandCtx) modeBandCount() int {
	edges := ctx.modeBandEdges()
	if len(edges) < 2 {
		return 0
	}
	return len(edges) - 1
}

func (ctx *bandCtx) modeLogN(band int) int {
	if band >= 0 && band < len(ctx.bandLogN) {
		return ctx.bandLogN[band]
	}
	if band >= 0 && band < len(LogN) {
		return LogN[band]
	}
	return 0
}

func pulseCacheForBandTables(band, lm int, cacheIndex []int16, cacheBits []uint8, bands int) (pulseCacheView, bool) {
	if band < 0 || band >= bands {
		return pulseCacheView{}, false
	}
	if lm < -1 {
		return pulseCacheView{}, false
	}
	idx := (lm + 1) * bands
	if idx < 0 || idx+band >= len(cacheIndex) {
		return pulseCacheView{}, false
	}
	start := int(cacheIndex[idx+band])
	if start < 0 || start >= len(cacheBits) {
		return pulseCacheView{}, false
	}
	cache := cacheBits[start:]
	if len(cache) == 0 {
		return pulseCacheView{}, false
	}
	maxPseudo := int(cache[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache) {
		return pulseCacheView{}, false
	}
	return pulseCacheView{bits: cache, staticOffset: pulseCacheTableOffset(cacheBits, start)}, true
}

func (ctx *bandCtx) pulseCacheForBand(lm int) (pulseCacheView, bool) {
	if len(ctx.cacheIndex) != 0 && len(ctx.cacheBits) != 0 {
		return pulseCacheForBandTables(ctx.band, lm, ctx.cacheIndex, ctx.cacheBits, ctx.modeBandCount())
	}
	return pulseCacheForBand(ctx.band, lm)
}

func (ctx *bandCtx) bitsToPulses(lm, bitsQ3 int) int {
	if bitsQ3 <= 0 {
		return 0
	}
	if cache, ok := ctx.pulseCacheForBand(lm); ok {
		return bitsToPulsesCached(cache, bitsQ3)
	}
	return bitsToPulses(ctx.band, lm, bitsQ3)
}

func (ctx *bandCtx) pulsesToBits(lm, pulses int) int {
	if pulses <= 0 {
		return 0
	}
	if cache, ok := ctx.pulseCacheForBand(lm); ok {
		return pulsesToBitsCached(cache, pulses)
	}
	return pulsesToBits(ctx.band, lm, pulses)
}

// computeTheta computes and encodes/decodes the stereo theta angle.
// This is the standard version without extended precision support.
func computeTheta(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	computeThetaWithExtBudget(ctx, sctx, x, y, n, b, nil, B, B0, lm, stereo, fill)
}

func computeThetaWithExtBudget(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, extB *int, B, B0, lm int, stereo bool, fill *int) {
	if !ctx.encode && (ctx.extDec == nil || extB == nil || *extB <= 0) {
		computeThetaDecode(ctx, sctx, n, b, B, B0, lm, stereo, fill)
		return
	}
	computeThetaExt(ctx, sctx, x, y, n, b, extB, B, B0, lm, stereo, fill)
}

func computeThetaDecode(ctx *bandCtx, sctx *splitCtx, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	pulseCap := ctx.modeLogN(ctx.band) + lm*(1<<bitRes)
	offset := (pulseCap >> 1) - qthetaOffset
	if stereo && n == 2 {
		offset = (pulseCap >> 1) - qthetaOffsetTwoPhase
	}
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && ctx.band >= ctx.intensity {
		qn = 1
	}

	itheta := 0
	ithetaQ30 := 0
	inv := 0
	tell := 0
	measureQalloc := false
	if qn != 1 {
		tell = ctx.rd.TellFrac()
		measureQalloc = true
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			fm := int(ctx.rd.Decode(uint32(ft)))
			if fm < (x0+1)*p0 {
				itheta = fm / p0
			} else {
				itheta = x0 + 1 + (fm - (x0+1)*p0)
			}
			var fl int
			if itheta <= x0 {
				fl = p0 * itheta
				ctx.rd.Update(uint32(fl), uint32(fl+p0), uint32(ft))
			} else {
				fl = (itheta - 1 - x0) + (x0+1)*p0
				ctx.rd.Update(uint32(fl), uint32(fl+1), uint32(ft))
			}
		} else if B0 > 1 || stereo {
			ft := uint32(qn + 1)
			if ft <= 1<<8 {
				itheta = int(ctx.rd.DecodeUniformSmall(ft))
			} else {
				itheta = int(ctx.rd.DecodeUniform(ft))
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			fm := int(ctx.rd.Decode(uint32(ft)))
			if fm < ((qn >> 1) * ((qn >> 1) + 1) >> 1) {
				itheta = int((isqrt32(uint32(8*fm+1)) - 1) >> 1)
				fs := itheta + 1
				fl := itheta * (itheta + 1) >> 1
				ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
			} else {
				itheta = int((2*(qn+1) - int(isqrt32(uint32(8*(ft-fm-1)+1)))) >> 1)
				fs := qn + 1 - itheta
				fl := ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
				ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
			}
		}
		itheta = celtUdiv(itheta*16384, qn)
		ithetaQ30 = itheta << 16
	} else if stereo {
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			tell = ctx.rd.TellFrac()
			measureQalloc = true
			inv = ctx.rd.DecodeBit(2)
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
		ithetaQ30 = 0
	}

	if measureQalloc {
		qalloc := ctx.rd.TellFrac() - tell
		if qalloc != 0 {
			*b -= qalloc
			sctx.qalloc = qalloc
		}
	}

	imid := 0
	iside := 0
	delta := 0
	switch itheta {
	case 0:
		imid = 32767
		iside = 0
		*fill &= (1 << B) - 1
		delta = -16384
	case 16384:
		imid = 0
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	default:
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.ithetaQ30 = ithetaQ30
}

// computeThetaExt computes and encodes/decodes the stereo theta angle with optional extended precision.
// When extended precision is available (ctx.extEnc != nil and extB != nil),
// it also encodes additional Q30 precision bits to the extension bitstream.
// Reference: libopus bands.c compute_theta() with ENABLE_QEXT path (lines 863-885)
func computeThetaExt(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, extB *int, B, B0, lm int, stereo bool, fill *int) {
	var traceState quantBandTraceState
	if celtQuantBandTraceEnabled {
		traceState = beginQuantThetaTrace(ctx, x, y, n, *b, B, B0, lm, stereo, *fill)
	}
	bIn := *b
	pulseCap := ctx.modeLogN(ctx.band) + lm*(1<<bitRes)
	offset := (pulseCap >> 1) - qthetaOffset
	if stereo && n == 2 {
		offset = (pulseCap >> 1) - qthetaOffsetTwoPhase
	}
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && ctx.band >= ctx.intensity {
		qn = 1
	}

	tell := 0
	if ctx.encode {
		if ctx.re != nil {
			tell = ctx.re.TellFrac()
		}
	} else if ctx.rd != nil {
		tell = ctx.rd.TellFrac()
	}
	itheta := 0
	ithetaQ30 := 0
	rawItheta := 0
	rawIthetaQ30 := 0
	inv := 0
	if ctx.encode {
		// Match libopus: derive raw theta before qn decisions so qn==1
		// can still drive phase inversion signaling.
		ithetaQ30 = stereoIthetaQ30Norm(x, y, stereo)
		if celtQuantBandTraceEnabled {
			rawIthetaQ30 = ithetaQ30
		}
		itheta = ithetaQ30 >> 16
		rawItheta = itheta
	}
	if qn != 1 {
		if ctx.encode {
			// Apply theta biasing for stereo RDO.
			// When thetaRound == 0: normal rounding (default)
			// When thetaRound != 0: bias toward 0/16384 (away from equal split)
			// Reference: libopus bands.c compute_theta(), lines 787-796
			if !stereo || ctx.thetaRound == 0 {
				// Standard rounding
				itheta = (itheta*qn + 8192) >> 14
				if !stereo && ctx.avoidSplitNoise && itheta > 0 && itheta < qn {
					unquantized := celtUdiv(itheta*16384, qn)
					delta := fracMul16((n-1)<<7, bitexactLog2tanTheta(unquantized))
					if delta > *b {
						itheta = qn
					} else if delta < -*b {
						itheta = 0
					}
				}
			} else {
				// Stereo theta RDO biasing: bias toward 0 or 16384
				// (away from equal split at itheta=8192).
				// bias is positive when itheta > 8192, negative otherwise
				bias := 0
				if itheta > 8192 {
					bias = 32767 / qn
				} else {
					bias = -32767 / qn
				}
				down := min(max((itheta*qn+bias)>>14, 0), qn-1)
				if ctx.thetaRound < 0 {
					itheta = down
				} else {
					itheta = down + 1
				}
			}
		}
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			if ctx.encode {
				if ctx.re != nil {
					var fl, fs int
					if itheta <= x0 {
						fl = p0 * itheta
						fs = p0
					} else {
						fl = (itheta - 1 - x0) + (x0+1)*p0
						fs = 1
					}
					ctx.re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			} else if ctx.rd != nil {
				fm := int(ctx.rd.Decode(uint32(ft)))
				if fm < (x0+1)*p0 {
					itheta = fm / p0
				} else {
					itheta = x0 + 1 + (fm - (x0+1)*p0)
				}
				var fl int
				if itheta <= x0 {
					fl = p0 * itheta
					ctx.rd.Update(uint32(fl), uint32(fl+p0), uint32(ft))
				} else {
					fl = (itheta - 1 - x0) + (x0+1)*p0
					ctx.rd.Update(uint32(fl), uint32(fl+1), uint32(ft))
				}
			}
		} else if B0 > 1 || stereo {
			if ctx.encode {
				if ctx.re != nil {
					ctx.re.EncodeUniform(uint32(itheta), uint32(qn+1))
				}
			} else if ctx.rd != nil {
				ft := uint32(qn + 1)
				if ft <= 1<<8 {
					itheta = int(ctx.rd.DecodeUniformSmall(ft))
				} else {
					itheta = int(ctx.rd.DecodeUniform(ft))
				}
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			if ctx.encode {
				if ctx.re != nil {
					fs := 1
					fl := 0
					if itheta <= (qn >> 1) {
						fs = itheta + 1
						fl = itheta * (itheta + 1) >> 1
					} else {
						fs = qn + 1 - itheta
						fl = ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
					}
					ctx.re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			} else if ctx.rd != nil {
				fm := int(ctx.rd.Decode(uint32(ft)))
				if fm < ((qn >> 1) * ((qn >> 1) + 1) >> 1) {
					itheta = int((isqrt32(uint32(8*fm+1)) - 1) >> 1)
					fs := itheta + 1
					fl := itheta * (itheta + 1) >> 1
					ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
				} else {
					itheta = int((2*(qn+1) - int(isqrt32(uint32(8*(ft-fm-1)+1)))) >> 1)
					fs := qn + 1 - itheta
					fl := ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
					ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			}
		}
		// Unquantize itheta to 14-bit range [0, 16384]
		itheta = celtUdiv(itheta*16384, qn)

		// Extended precision theta coding (ENABLE_QEXT path in libopus).
		// This applies to regular band splits as well as stereo splits.
		codedExtendedTheta := false
		if extB != nil && ((ctx.encode && ctx.extEnc != nil) || (!ctx.encode && ctx.extDec != nil)) {
			extTellFrac := 0
			if ctx.encode {
				extTellFrac = ctx.extEnc.TellFrac()
			} else {
				extTellFrac = ctx.extDec.TellFrac()
			}
			extRemainingBits := max(ctx.extTotalBits-extTellFrac, 0)
			if *extB > extRemainingBits {
				*extB = extRemainingBits
			}
			if *extB >= 2*n<<bitRes && extRemainingBits-1 > 2<<bitRes {
				extTellBefore := extTellFrac
				extraBits := min(max(celtSudiv(*extB, (2*n-1)<<bitRes), 2), 12)

				encodedVal := 0
				if ctx.encode {
					residual := ithetaQ30 - (itheta << 16)
					scaleFactor := int64(qn) * int64((1<<extraBits)-1)
					encodedVal = int((int64(residual)*scaleFactor + (1 << 29)) >> 30)
					encodedVal += (1 << (extraBits - 1)) - 1
					if encodedVal < 0 {
						encodedVal = 0
					}
					if encodedVal > (1<<extraBits)-2 {
						encodedVal = (1 << extraBits) - 2
					}
					ctx.extEnc.EncodeUniform(uint32(encodedVal), uint32((1<<extraBits)-1))
					extTellFrac = ctx.extEnc.TellFrac()
				} else {
					encodedVal = int(ctx.extDec.DecodeUniform(uint32((1 << extraBits) - 1)))
					extTellFrac = ctx.extDec.TellFrac()
				}

				encodedVal -= (1 << (extraBits - 1)) - 1
				ithetaQ30 = min(max((itheta<<16)+int((int64(encodedVal)*(1<<30))/int64(qn*((1<<extraBits)-1))), 0), 1<<30)
				*extB -= extTellFrac - extTellBefore
				codedExtendedTheta = true
			}
		}

		if !codedExtendedTheta {
			ithetaQ30 = itheta << 16
		}

		if ctx.encode && stereo {
			if itheta == 0 {
				intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
			} else {
				stereoSplit(x, y)
			}
		}
	} else if stereo {
		if ctx.encode {
			// Match libopus: for intensity stereo bands, inversion is selected
			// from the raw theta and then entropy-coded as a single flag.
			if itheta > 8192 && !ctx.disableInv {
				inv = 1
			} else {
				inv = 0
			}
			if inv != 0 && y != nil {
				for i := range y {
					y[i] = -y[i]
				}
			}
			intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
		}
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			if ctx.encode {
				if ctx.re != nil {
					if inv != 0 {
						ctx.re.EncodeBit(1, 2)
					} else {
						ctx.re.EncodeBit(0, 2)
					}
				}
			} else if ctx.rd != nil {
				inv = ctx.rd.DecodeBit(2)
			}
		} else {
			inv = 0
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
		ithetaQ30 = 0
	}

	qalloc := 0
	if ctx.encode {
		if ctx.re != nil {
			qalloc = ctx.re.TellFrac() - tell
		}
	} else if ctx.rd != nil {
		qalloc = ctx.rd.TellFrac() - tell
	}
	if qalloc != 0 {
		*b -= qalloc
		sctx.qalloc = qalloc
	}

	imid := 0
	iside := 0
	delta := 0
	switch itheta {
	case 0:
		imid = 32767
		iside = 0
		*fill &= (1 << B) - 1
		delta = -16384
	case 16384:
		imid = 0
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	default:
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.ithetaQ30 = ithetaQ30
	if celtQuantBandTraceEnabled {
		finishQuantThetaTrace(&traceState, ctx, sctx, x, y, n, *b, *fill, qn, pulseCap, offset, rawIthetaQ30)
	}
	_, _ = bIn, rawItheta
}

// computeQEXTExtraBits reads the active coder before dividing. On POWER,
// Go 1.27.3 can clobber the encode/decode condition across a division
// (golang/go#81842). The limits in celt/bands.c:quant_partition are 12 for
// PVQ refinement and 14 for cubic quantization.
func computeQEXTExtraBits(ctx *bandCtx, extBudget, n, maxExtraBits int) int {
	if ctx == nil || n <= 1 || extBudget <= 0 {
		return 0
	}
	var extTellFrac int
	if ctx.encode {
		if ctx.extEnc == nil {
			return 0
		}
		extTellFrac = ctx.extEnc.TellFrac()
	} else {
		if ctx.extDec == nil {
			return 0
		}
		extTellFrac = ctx.extDec.TellFrac()
	}
	extRemainingBits := ctx.extTotalBits - extTellFrac
	if extRemainingBits <= n<<bitRes {
		return 0
	}
	extraBits := (extBudget / (n - 1)) >> bitRes
	if extRemainingBits < ((extraBits+1)*(n-1)+n)<<bitRes {
		extraBits = ((extRemainingBits - (n << bitRes)) / (n - 1)) >> bitRes
		extraBits = max(extraBits-1, 0)
	}
	return min(maxExtraBits, extraBits)
}

func quantPartitionEncodeWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int, extBudget int) (int, []celtNorm) {
	if n == 1 {
		return 1, x
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	cache, hasCache := ctx.pulseCacheForBand(lm)
	maxBits := 0
	if hasCache {
		if lm != -1 {
			maxBits = pulseCacheMaxBits(cache)
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		var extB *int
		if extBudget != 0 && ctx.extEnc != nil {
			extB = &extBudget
		}
		computeThetaWithExtBudget(ctx, &sctx, x[:nHalf], y, nHalf, &b, extB, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1 []celtNorm
		var lowband2 []celtNorm
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			midGain := celtMul32(gain, opusVal16(mid))
			{
				var traceContext quantBandTraceRestorePoint
				if celtQuantBandTraceEnabled {
					traceContext = saveQuantBandTraceContext()
				}
				cm, _ = quantPartitionEncodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, midGain, fill, extBudget/2)
				if celtQuantBandTraceEnabled {
					restoreQuantBandTraceContext(traceContext)
				}
			}
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			sideGain := celtMul32(gain, opusVal16(side))
			var scm int
			{
				var traceContext quantBandTraceRestorePoint
				if celtQuantBandTraceEnabled {
					traceContext = saveQuantBandTraceContext()
				}
				scm, _ = quantPartitionEncodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, sideGain, fill>>B, extBudget/2)
				if celtQuantBandTraceEnabled {
					restoreQuantBandTraceContext(traceContext)
				}
			}
			cm |= scm << (B0 >> 1)
		} else {
			sideGain := celtMul32(gain, opusVal16(side))
			{
				var traceContext quantBandTraceRestorePoint
				if celtQuantBandTraceEnabled {
					traceContext = saveQuantBandTraceContext()
				}
				cm, _ = quantPartitionEncodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, sideGain, fill>>B, extBudget/2)
				if celtQuantBandTraceEnabled {
					restoreQuantBandTraceContext(traceContext)
				}
			}
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			midGain := celtMul32(gain, opusVal16(mid))
			var scm int
			{
				var traceContext quantBandTraceRestorePoint
				if celtQuantBandTraceEnabled {
					traceContext = saveQuantBandTraceContext()
				}
				scm, _ = quantPartitionEncodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, midGain, fill, extBudget/2)
				if celtQuantBandTraceEnabled {
					restoreQuantBandTraceContext(traceContext)
				}
			}
			cm |= scm
		}
		return cm, x
	}

	q := 0
	currBits := 0
	remBefore := ctx.remainingBits
	qInit := 0
	if hasCache {
		if b > 0 {
			q = bitsToPulsesCached(cache, b)
			qInit = q
			currBits = pulsesToBitsCached(cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = ctx.bitsToPulses(lm, b)
		qInit = q
		currBits = ctx.pulsesToBits(lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
		}
	}
	_, _, _ = remBefore, qInit, currBits
	if q != 0 {
		k := getPulses(q)
		if ctx.encode {
			pvqExtraBits := 0
			if extBudget > 0 && ctx.extEnc != nil {
				pvqExtraBits = computeQEXTExtraBits(ctx, extBudget, n, 12)
			}
			var traceState quantBandTraceState
			if celtQuantBandTraceEnabled {
				traceState = beginQuantPVQTrace(ctx, x, n, k, ctx.spread, B, lm, gain, ctx.resynth)
			}
			cm := algQuantScratch(ctx.re, ctx.band, x, n, k, ctx.spread, B, gain, ctx.resynth, ctx.extEnc, pvqExtraBits, ctx.encScratch)
			if celtQuantBandTraceEnabled {
				finishQuantPVQTrace(&traceState, ctx, x, n, cm)
			}
			return cm, x
		}
		// Use scratch-aware version to avoid allocations in decode hot path
		cm := algUnquantInto(x, ctx.rd, ctx.band, n, k, ctx.spread, B, gain, ctx.extDec, computeQEXTExtraBits(ctx, extBudget, n, 12), ctx.scratch)
		return cm, x
	}
	if cubicBits := computeQEXTCubicBits(ctx, extBudget, n); cubicBits > 0 {
		if ctx.encode {
			return cubicQuant(x, n, cubicBits, B, ctx.extEnc, gain, ctx.resynth, ctx.encScratch), x
		}
		return cubicUnquant(x, n, cubicBits, B, ctx.extDec, gain, ctx.scratch), x
	}
	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0, x
		}
		if lowband == nil {
			var seedPtr *uint32
			if ctx.seedActive {
				seedPtr = &ctx.seed
			}
			if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
				if ctx.seedActive {
					for i := range x {
						ctx.seed = ctx.seed*1664525 + 1013904223
						// Match libopus: arithmetic shift on signed seed before scaling.
						x[i] = celtNorm(int32(ctx.seed) >> 20)
					}
				}
				renormalizeVector(x, gain)
			}
			return cmMask, x
		}
		var seedPtr *uint32
		if ctx.seedActive {
			seedPtr = &ctx.seed
		}
		if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					tmp := 1.0 / 256.0
					if (ctx.seed & 0x8000) == 0 {
						tmp = -tmp
					}
					if i < len(lowband) {
						x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
					} else {
						x[i] = celtNorm(tmp)
					}
				}
			}
			renormalizeVector(x, gain)
		}
		return fill, x
	}
	return fill, x
}

// quantPartitionDecodeNoExt is the libopus quant_partition() decode path
// without an extension decoder. len(x) is the partition size N: while the
// bits exceed the band's pulse cache the partition splits in halves, and a
// leaf decodes its PVQ pulses or noise-fills when it has none.
func quantPartitionDecodeNoExt(ctx *bandCtx, x []celtNorm, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int) int {
	n := len(x)
	if n == 1 {
		return 1
	}

	// cache = m->cache.bits + m->cache.index[(LM+1)*nbEBands+i]; the standard
	// mode reads its precomputed maximum and bits-to-pulses tables.
	cacheStart := -1
	var cache pulseCacheView
	maxBits := 0
	if ctx.stdCache {
		cacheStart = int(cacheIndex50[(lm+1)*MaxBands+ctx.band])
		if lm != -1 {
			maxBits = int(pulseCacheLookup50.maxBits[cacheStart])
		}
	} else if c, ok := ctx.pulseCacheForBand(lm); ok {
		cache = c
		if lm != -1 {
			maxBits = pulseCacheMaxBits(cache)
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		x = x[:nHalf]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		var sctx splitCtx
		computeThetaDecode(ctx, &sctx, nHalf, &b, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)
		delta := sctx.delta
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				delta -= delta >> (4 - lm)
			} else {
				delta = min(0, delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband2 []celtNorm
		if len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}
		if len(lowband) >= nHalf {
			lowband = lowband[:nHalf]
		} else {
			lowband = nil
		}

		rebalance := ctx.remainingBits
		if mbits >= sbits {
			cm := quantPartitionDecodeNoExt(ctx, x, mbits, B, lowband, lm, celtMul32(gain, opusVal16(mid)), fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			return cm | quantPartitionDecodeNoExt(ctx, y, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B)<<(B0>>1)
		}
		cm := quantPartitionDecodeNoExt(ctx, y, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B) << (B0 >> 1)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		return cm | quantPartitionDecodeNoExt(ctx, x, mbits, B, lowband, lm, celtMul32(gain, opusVal16(mid)), fill)
	}

	// bits2pulses()/pulses2bits(), then back off until the budget holds.
	q := 0
	if cacheStart >= 0 {
		if b > 0 {
			lut := &pulseCacheLookup50.lut[cacheStart]
			q = int(lut[min(b-1, pulseCacheLookupBits-1)])
			bits := cacheBits50[cacheStart:]
			currBits := 0
			if q > 0 {
				currBits = int(bits[q]) + 1
			}
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = 0
				if q > 0 {
					currBits = int(bits[q]) + 1
				}
				ctx.remainingBits -= currBits
			}
		}
	} else if len(cache.bits) > 0 {
		if b > 0 {
			q = bitsToPulsesCached(cache, b)
			currBits := pulsesToBitsCached(cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = ctx.bitsToPulses(lm, b)
		currBits := ctx.pulsesToBits(lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
		}
	}
	if q != 0 {
		return algUnquantNoExtInto(x, ctx.rd, getPulses(q), ctx.spread, B, gain, ctx.scratch)
	}

	// No pulses: fill the band anyway (the decoder always resynthesizes).
	cmMask := (1 << B) - 1
	fill &= cmMask
	if fill == 0 {
		clear(x)
		return 0
	}
	var seedPtr *uint32
	if ctx.seedActive {
		seedPtr = &ctx.seed
	}
	if lowband == nil {
		if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					x[i] = celtNorm(int32(ctx.seed) >> 20)
				}
			}
			renormalizeVector(x, gain)
		}
		return cmMask
	}
	if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
		// seededZeroPulseResynth takes only lowbands that cover x.
		if ctx.seedActive {
			for i := range x {
				ctx.seed = ctx.seed*1664525 + 1013904223
				tmp := 1.0 / 256.0
				if (ctx.seed & 0x8000) == 0 {
					tmp = -tmp
				}
				if i < len(lowband) {
					x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
				} else {
					x[i] = celtNorm(tmp)
				}
			}
		}
		renormalizeVector(x, gain)
	}
	return fill
}

func quantPartitionDecodeWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int, extBudget int) int {
	if n == 1 {
		return 1
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	cache, hasCache := ctx.pulseCacheForBand(lm)
	maxBits := 0
	if hasCache {
		if lm != -1 {
			maxBits = pulseCacheMaxBits(cache)
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		computeThetaWithExtBudget(ctx, &sctx, x[:nHalf], y, nHalf, &b, &extBudget, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1 []celtNorm
		var lowband2 []celtNorm
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			cm = quantPartitionDecodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill, extBudget/2)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B, extBudget/2)
			cm |= scm << (B0 >> 1)
		} else {
			cm = quantPartitionDecodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B, extBudget/2)
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill, extBudget/2)
			cm |= scm
		}
		return cm
	}

	q := 0
	currBits := 0
	remBefore := ctx.remainingBits
	qInit := 0
	if hasCache {
		if b > 0 {
			q = bitsToPulsesCached(cache, b)
			qInit = q
			currBits = pulsesToBitsCached(cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = ctx.bitsToPulses(lm, b)
		qInit = q
		currBits = ctx.pulsesToBits(lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
		}
	}
	_, _, _ = remBefore, qInit, currBits
	if q != 0 {
		k := getPulses(q)
		pvqExtraBits := 0
		if extBudget > 0 && ctx.extDec != nil {
			pvqExtraBits = computeQEXTExtraBits(ctx, extBudget, n, 12)
		}
		cm := algUnquantInto(x, ctx.rd, ctx.band, n, k, ctx.spread, B, gain, ctx.extDec, pvqExtraBits, ctx.scratch)
		return cm
	}
	if cubicBits := computeQEXTCubicBits(ctx, extBudget, n); cubicBits > 0 {
		return cubicUnquant(x, n, cubicBits, B, ctx.extDec, gain, ctx.scratch)
	}
	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0
		}
		if lowband == nil {
			var seedPtr *uint32
			if ctx.seedActive {
				seedPtr = &ctx.seed
			}
			if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
				if ctx.seedActive {
					for i := range x {
						ctx.seed = ctx.seed*1664525 + 1013904223
						x[i] = celtNorm(int32(ctx.seed) >> 20)
					}
				}
				renormalizeVector(x, gain)
			}
			return cmMask
		}
		var seedPtr *uint32
		if ctx.seedActive {
			seedPtr = &ctx.seed
		}
		if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					tmp := 1.0 / 256.0
					if (ctx.seed & 0x8000) == 0 {
						tmp = -tmp
					}
					if i < len(lowband) {
						x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
					} else {
						x[i] = celtNorm(tmp)
					}
				}
			}
			renormalizeVector(x, gain)
		}
		return fill
	}
	return fill
}
func quantBandN1(ctx *bandCtx, x, y []celtNorm, b int, lowbandOut []celtNorm) int {
	stereo := y != nil
	x0 := x
	for c := 0; c < 1+boolToInt(stereo); c++ {
		sign := 0
		if ctx.remainingBits >= 1<<bitRes {
			if ctx.encode {
				if ctx.re != nil {
					if x[0] < 0 {
						sign = 1
					}
					ctx.re.EncodeRawBits(uint32(sign), 1)
				}
			} else if ctx.rd != nil {
				sign = int(ctx.rd.DecodeRawBit())
			}
			ctx.remainingBits -= 1 << bitRes
		}
		if ctx.resynth {
			val := 1.0
			if sign != 0 {
				val = -1.0
			}
			x[0] = celtNorm(val)
		}
		if stereo {
			x = y
		}
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op,
		// so we just copy the value directly without any scaling.
		lowbandOut[0] = celtNorm(x0[0])
	}
	return 1
}

func quantBandN1Decode(ctx *bandCtx, x, y []celtNorm, b int, lowbandOut []celtNorm) int {
	if y == nil {
		return quantBandN1DecodeMono(ctx, x, b, lowbandOut)
	}
	return quantBandN1DecodeStereo(ctx, x, y, b, lowbandOut)
}

func quantBandN1DecodeMono(ctx *bandCtx, x []celtNorm, b int, lowbandOut []celtNorm) int {
	sign := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign = int(ctx.rd.DecodeRawBit())
		}
		ctx.remainingBits -= 1 << bitRes
	}
	if ctx.resynth {
		val := 1.0
		if sign != 0 {
			val = -1.0
		}
		x[0] = celtNorm(val)
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op.
		lowbandOut[0] = celtNorm(x[0])
	}
	return 1
}

func quantBandN1DecodeStereo(ctx *bandCtx, x, y []celtNorm, b int, lowbandOut []celtNorm) int {
	sign0 := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign0 = int(ctx.rd.DecodeRawBit())
		}
		ctx.remainingBits -= 1 << bitRes
	}
	sign1 := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign1 = int(ctx.rd.DecodeRawBit())
		}
		ctx.remainingBits -= 1 << bitRes
	}
	if ctx.resynth {
		val0 := 1.0
		if sign0 != 0 {
			val0 = -1.0
		}
		val1 := 1.0
		if sign1 != 0 {
			val1 = -1.0
		}
		x[0] = celtNorm(val0)
		y[0] = celtNorm(val1)
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op.
		lowbandOut[0] = celtNorm(x[0])
	}
	return 1
}

func copyLowbandScratch(dst, src []celtNorm, n int) []celtNorm {
	if n > 0 && len(dst) >= n && len(src) >= n {
		copy(dst[:n], src[:n])
		return dst[:n:n]
	}
	copy(dst, src)
	return dst
}

func quantBandWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int, extBudget int) int {
	return quantBandPreparedLowbandWithExtBudget(ctx, x, n, b, B, lowband, lm, lowbandOut, gain, lowbandScratch, fill, false, extBudget)
}

func quantBandPreparedLowbandWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int, lowbandPrepared bool, extBudget int) int {
	if n == 1 {
		return quantBandN1(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	N0 := n
	N_B := celtUdivBlocks(n, B)
	longBlocks := B == 1

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if !lowbandPrepared && lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	if recombine != 0 {
		for k := 0; k < recombine; k++ {
			if ctx.encode {
				haar1(x, n>>k, 1<<k)
			}
			if lowband != nil && !lowbandPrepared {
				haar1Norm(lowband, n>>k, 1<<k)
			}
			fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
		}
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		if ctx.encode {
			haar1(x, N_B, B)
		}
		if lowband != nil && !lowbandPrepared {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		if ctx.encScratch != nil {
			x = ctx.encScratch.ensureQuantWork(n)
			deinterleaveHadamardInto(x, xOrig, N_B>>recombine, B0<<recombine, longBlocks)
		} else {
			deinterleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
		if lowband != nil && !lowbandPrepared {
			deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}
	cm := 0
	if ctx.extraBands && b > cubicQEXTThresholdQ3(ctx, n, lm) {
		cm = cubicQuantPartition(ctx, x, n, b, B, lm, gain)
	} else {
		// quant_band's enclosing stereo theta remains active for the sibling tree.
		var traceContext quantBandTraceRestorePoint
		if celtQuantBandTraceEnabled {
			traceContext = saveQuantBandTraceContext()
		}
		cm, _ = quantPartitionEncodeWithExtBudget(ctx, x, n, b, B, lowband, lm, gain, fill, extBudget)
		if celtQuantBandTraceEnabled {
			restoreQuantBandTraceContext(traceContext)
		}
	}

	if ctx.resynth {
		if B0 > 1 {
			if ctx.encScratch != nil {
				interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
				x = xOrig
			} else {
				interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
			}
		}
		N_B = N_B0
		B = B0
		for k := 0; k < timeDivide; k++ {
			B >>= 1
			N_B <<= 1
			cm |= cm >> B
			haar1(x, N_B, B)
		}
		for k := 0; k < recombine; k++ {
			cm = bitDeinterleaveTable[cm&0xF]
			haar1(x, N0>>k, 1<<k)
		}
		B <<= recombine

		if lowbandOut != nil && len(lowbandOut) >= N0 {
			scaleLowbandOutForFoldingNorm(lowbandOut, x, N0)
		}
		cm &= (1 << B) - 1
	}
	return cm
}

func prepareQuantBandLowband(dst, src []celtNorm, n, B, tfChange int, scratch *bandEncodeScratch) []celtNorm {
	if src == nil || len(src) < n || len(dst) < n {
		return nil
	}
	dst = dst[:n]
	copy(dst, src[:n])

	N_B := celtUdivBlocks(n, B)
	recombine := max(tfChange, 0)
	if recombine != 0 {
		for k := range recombine {
			haar1Norm(dst, n>>k, 1<<k)
		}
	}
	longBlocks := B == 1
	B >>= recombine
	N_B <<= recombine
	for (N_B&1) == 0 && tfChange < 0 {
		haar1Norm(dst, N_B, B)
		B <<= 1
		N_B >>= 1
		tfChange++
	}
	if B > 1 {
		deinterleaveHadamardScratchBufNorm(dst, N_B>>recombine, B<<recombine, longBlocks, nil, scratch)
	}
	return dst
}

func quantBandDecode(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int) int {
	if ctx.extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
		ctx.blocks, ctx.lm, ctx.lowbandScratch = B, lm, lowbandScratch
		return quantBandDecodeNoExtFast(ctx, x[:n], b, lowband, lowbandOut, gain, fill)
	}
	return quantBandDecodeWithExtBudget(ctx, x, n, b, B, lowband, lm, lowbandOut, gain, lowbandScratch, fill, ctx.extBudget)
}

// quantBandDecodeNoExtFast is the libopus quant_band() decode path without an
// extension decoder. len(x) is the band size N; the frame's block count,
// LM and lowband scratch come from ctx.blocks, ctx.lm and
// ctx.lowbandScratch. lowbandOut, when non-nil, has length N.
func quantBandDecodeNoExtFast(ctx *bandCtx, x []celtNorm, b int, lowband, lowbandOut []celtNorm, gain opusVal16, fill int) int {
	n := len(x)
	if n == 1 {
		return quantBandN1DecodeMono(ctx, x, b, lowbandOut)
	}
	B := ctx.blocks
	tfChange := ctx.tfChange
	if B == 1 && tfChange == 0 {
		// One long block without a TF change: no recombination, time
		// division or Hadamard reordering, so quant_band reduces to
		// quant_partition, the lowband_out scaling and a one-bit mask.
		cm := quantPartitionDecodeNoExt(ctx, x, b, 1, lowband, ctx.lm, gain, fill)
		if lowbandOut != nil {
			scaleLowbandOutForFoldingNorm(lowbandOut, x, n)
		}
		return cm & 1
	}

	N_B := celtUdivBlocks(n, B)
	longBlocks := B == 1
	recombine := max(tfChange, 0)

	if lowbandScratch := ctx.lowbandScratch; lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	for k := range recombine {
		if lowband != nil {
			haar1Norm(lowband, n>>k, 1<<k)
		}
		fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		if lowband != nil {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		// The decoder's quant_partition writes every element of X before
		// reading it, so libopus deinterleaves only the lowband here; X is
		// decoded straight into the work buffer. With the seeded noise fill
		// active, no path reads X first, so the buffer needs no zeroing.
		if ctx.scratch != nil {
			if ctx.seedActive {
				x = ctx.scratch.ensureQuantWorkNoClear(n)
			} else {
				x = ctx.scratch.ensureQuantWork(n)
			}
		}
		if lowband != nil {
			if ctx.scratch != nil && ctx.lowbandScratch != nil {
				// lowband is already the lowband_scratch copy, so it is
				// deinterleaved into the Hadamard scratch and read from there
				// instead of being copied back.
				tmp := ctx.scratch.ensureHadamardTmpNorm(n)
				deinterleaveHadamardIntoNorm(tmp, lowband, N_B>>recombine, B0<<recombine, longBlocks)
				lowband = tmp
			} else {
				deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
			}
		}
	}

	cm := quantPartitionDecodeNoExt(ctx, x, b, B, lowband, ctx.lm, gain, fill)

	if B0 > 1 {
		if ctx.scratch != nil {
			interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
			x = xOrig
		} else {
			interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}
	N_B = N_B0
	B = B0
	for range timeDivide {
		B >>= 1
		N_B <<= 1
		cm |= cm >> B
		haar1(x, N_B, B)
	}
	for k := range recombine {
		cm = bitDeinterleaveTable[cm&0xF]
		haar1(x, n>>k, 1<<k)
	}
	B <<= recombine

	if lowbandOut != nil {
		scaleLowbandOutForFoldingNorm(lowbandOut, x, n)
	}
	return cm & ((1 << B) - 1)
}

// quantBandStereoDecodeNoExtFast is the libopus quant_band_stereo() decode
// path without an extension decoder. len(x) is the band size N and y has at
// least N coefficients; see quantBandDecodeNoExtFast for the ctx fields.
func quantBandStereoDecodeNoExtFast(ctx *bandCtx, x, y []celtNorm, b int, lowband, lowbandOut []celtNorm, fill int) int {
	n := len(x)
	if n == 1 {
		return quantBandN1DecodeStereo(ctx, x, y, b, lowbandOut)
	}
	y = y[:n]

	origFill := fill
	B := ctx.blocks
	var sctx splitCtx
	computeThetaDecode(ctx, &sctx, n, &b, B, B, ctx.lm, true, &fill)
	mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)

	if n == 2 {
		// The side is the mid rotated by 90 degrees, with one sign bit.
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits := b - sbits
		ctx.remainingBits -= sctx.qalloc + sbits

		x2, y2 := x, y
		if sctx.itheta > 8192 {
			x2, y2 = y, x
		}
		sign := float32(1)
		if sbits > 0 && ctx.rd.DecodeRawBit() == 1 {
			sign = -1
		}
		var cm int
		if B == 1 && ctx.tfChange == 0 {
			cm = quantPartitionDecodeNoExt(ctx, x2, mbits, 1, lowband, ctx.lm, 1.0, origFill) & 1
			if lowbandOut != nil {
				scaleLowbandOutForFoldingNorm(lowbandOut, x2, n)
			}
		} else {
			cm = quantBandDecodeNoExtFast(ctx, x2, mbits, lowband, lowbandOut, 1.0, origFill)
		}
		y2[0] = celtNorm(-sign * float32(x2[1]))
		y2[1] = celtNorm(sign * float32(x2[0]))
		x0 := noFMA32Mul(float32(mid), float32(x[0]))
		x1 := noFMA32Mul(float32(mid), float32(x[1]))
		y0 := noFMA32Mul(float32(side), float32(y[0]))
		y1 := noFMA32Mul(float32(side), float32(y[1]))
		x[0] = celtNorm(noFMA32Sub(x0, y0))
		y[0] = celtNorm(noFMA32Add(x0, y0))
		x[1] = celtNorm(noFMA32Sub(x1, y1))
		y[1] = celtNorm(noFMA32Add(x1, y1))
		if sctx.inv != 0 {
			y[0] = -y[0]
			y[1] = -y[1]
		}
		return cm
	}

	mbits := max(0, min(b, (b-sctx.delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	// With one long block and no TF change, quant_band() of the mid and the
	// side is quant_partition() and the lowband_out scaling, so both call
	// the partition directly.
	long := B == 1 && ctx.tfChange == 0
	rebalance := ctx.remainingBits
	var cm int
	if mbits >= sbits {
		if long {
			cm = quantPartitionDecodeNoExt(ctx, x, mbits, 1, lowband, ctx.lm, 1.0, fill) & 1
			if lowbandOut != nil {
				scaleLowbandOutForFoldingNorm(lowbandOut, x, n)
			}
		} else {
			cm = quantBandDecodeNoExtFast(ctx, x, mbits, lowband, lowbandOut, 1.0, fill)
		}
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		if long {
			cm |= quantPartitionDecodeNoExt(ctx, y, sbits, 1, nil, ctx.lm, opusVal16(side), fill>>1) & 1
		} else {
			cm |= quantBandDecodeNoExtFast(ctx, y, sbits, nil, nil, opusVal16(side), fill>>B)
		}
	} else {
		if long {
			cm = quantPartitionDecodeNoExt(ctx, y, sbits, 1, nil, ctx.lm, opusVal16(side), fill>>1) & 1
		} else {
			cm = quantBandDecodeNoExtFast(ctx, y, sbits, nil, nil, opusVal16(side), fill>>B)
		}
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		if long {
			cm |= quantPartitionDecodeNoExt(ctx, x, mbits, 1, lowband, ctx.lm, 1.0, fill) & 1
			if lowbandOut != nil {
				scaleLowbandOutForFoldingNorm(lowbandOut, x, n)
			}
		} else {
			cm |= quantBandDecodeNoExtFast(ctx, x, mbits, lowband, lowbandOut, 1.0, fill)
		}
	}

	stereoMerge(x, y, opusVal16(mid))
	if sctx.inv != 0 {
		for i := range y {
			y[i] = -y[i]
		}
	}
	return cm
}

func quantBandDecodeWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int, extBudget int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	N0 := n
	N_B := celtUdivBlocks(n, B)
	longBlocks := B == 1

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	if recombine != 0 {
		for k := 0; k < recombine; k++ {
			if lowband != nil {
				haar1Norm(lowband, n>>k, 1<<k)
			}
			fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
		}
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		if lowband != nil {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		// The decoder's quant_partition writes every element of X before
		// reading it, so libopus deinterleaves only the lowband here; X is
		// decoded straight into the work buffer.
		if ctx.scratch != nil {
			x = ctx.scratch.ensureQuantWork(n)
		}
		if lowband != nil {
			deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}

	cm := 0
	if ctx.extraBands && b > cubicQEXTThresholdQ3(ctx, n, lm) {
		cm = cubicQuantPartition(ctx, x, n, b, B, lm, gain)
	} else if extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
		cm = quantPartitionDecodeNoExt(ctx, x[:n], b, B, lowband, lm, gain, fill)
	} else {
		cm = quantPartitionDecodeWithExtBudget(ctx, x, n, b, B, lowband, lm, gain, fill, extBudget)
	}

	if ctx.resynth {
		if B0 > 1 {
			if ctx.scratch != nil {
				interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
				x = xOrig
			} else {
				interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
			}
		}
		N_B = N_B0
		B = B0
		for k := 0; k < timeDivide; k++ {
			B >>= 1
			N_B <<= 1
			cm |= cm >> B
			haar1(x, N_B, B)
		}
		for k := 0; k < recombine; k++ {
			cm = bitDeinterleaveTable[cm&0xF]
			haar1(x, N0>>k, 1<<k)
		}
		B <<= recombine

		if lowbandOut != nil && len(lowbandOut) >= N0 {
			scaleLowbandOutForFoldingNorm(lowbandOut, x, N0)
		}
		cm &= (1 << B) - 1
	}
	return cm
}

func quantBandStereo(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int) int {
	return quantBandStereoWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, ctx.extBudget)
}

func quantBandStereoWithExtBudget(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, extBudget int) int {
	return quantBandStereoPreparedLowbandWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, false, extBudget)
}

func quantBandStereoPreparedLowband(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, lowbandPrepared bool) int {
	return quantBandStereoPreparedLowbandWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, lowbandPrepared, ctx.extBudget)
}

func quantBandStereoPreparedLowbandWithExtBudget(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, lowbandPrepared bool, extBudget int) int {
	if n == 1 {
		return quantBandN1(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}
	var bandTrace quantBandTraceState
	if celtQuantBandTraceEnabled {
		bandTrace = beginQuantBandOutputTrace(ctx, x, y, n, b, B, lm)
	}

	origFill := fill

	if ctx.encode && ctx.bandE != nil && ctx.channels == 2 {
		l := ctx.bandEnergy(0)
		r := ctx.bandEnergy(1)
		if l < 1e-10 || r < 1e-10 {
			if l > r {
				copy(y, x)
			} else {
				copy(x, y)
			}
		}
	}

	sctx := splitCtx{}
	var extB *int
	if extBudget != 0 && (ctx.extEnc != nil || ctx.extDec != nil) {
		extB = &extBudget
	}
	computeThetaWithExtBudget(ctx, &sctx, x, y, n, &b, extB, B, B, lm, true, &fill)
	var topThetaTraceContext quantBandTraceContext
	if celtQuantBandTraceEnabled {
		topThetaTraceContext = quantBandTraceCurrentContext()
		setQuantBandOutputTraceContext(&bandTrace, topThetaTraceContext)
	}
	mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)

	if n == 2 {
		mbits := b
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := sctx.itheta > 8192
		ctx.remainingBits -= sctx.qalloc + sbits

		x2 := x
		y2 := y
		if c {
			x2 = y
			y2 = x
		}
		sign := 1
		if sbits > 0 {
			if ctx.encode {
				if ctx.re != nil {
					bit := 0
					if float32(x2[0])*float32(y2[1])-float32(x2[1])*float32(y2[0]) < 0 {
						bit = 1
					}
					ctx.re.EncodeRawBits(uint32(bit), 1)
					if bit != 0 {
						sign = -1
					}
				}
			} else if ctx.rd != nil {
				if ctx.rd.DecodeRawBit() == 1 {
					sign = -1
				}
			}
		}
		cm := quantBandPreparedLowbandWithExtBudget(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill, lowbandPrepared, extBudget)
		sign32 := float32(sign)
		y2[0] = celtNorm(-sign32 * float32(x2[1]))
		y2[1] = celtNorm(sign32 * float32(x2[0]))
		if ctx.resynth {
			x[0] = celtNorm(float32(mid) * float32(x[0]))
			x[1] = celtNorm(float32(mid) * float32(x[1]))
			y[0] = celtNorm(float32(side) * float32(y[0]))
			y[1] = celtNorm(float32(side) * float32(y[1]))
			tmp := float32(x[0])
			y0 := float32(y[0])
			x[0] = celtNorm(noFMA32Sub(tmp, y0))
			y[0] = celtNorm(noFMA32Add(tmp, y0))
			tmp = float32(x[1])
			y1 := float32(y[1])
			x[1] = celtNorm(noFMA32Sub(tmp, y1))
			y[1] = celtNorm(noFMA32Add(tmp, y1))
			// Apply inv negation (same as common resynth path)
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		if celtQuantBandTraceEnabled {
			finishQuantBandOutputTrace(&bandTrace, ctx, x, y, n, cm)
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, mbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandPreparedLowbandWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, lowbandPrepared, extBudget/2+qextExtra)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			sbits = min(sbits, ctx.remainingBits)
		}
		cm |= quantBandWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2-qextExtra)
	} else {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, sbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2+qextExtra)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			mbits = min(mbits, ctx.remainingBits)
		}
		cm |= quantBandPreparedLowbandWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, lowbandPrepared, extBudget/2-qextExtra)
	}

	if ctx.resynth {
		if n != 2 {
			var mergeTrace quantBandTraceState
			if celtQuantBandTraceEnabled {
				mergeTrace = beginQuantStereoMergeTrace(ctx, x, y, n, B, lm, opusVal16(mid), topThetaTraceContext)
			}
			stereoMerge(x, y, opusVal16(mid))
			if celtQuantBandTraceEnabled {
				finishQuantStereoMergeTrace(&mergeTrace, ctx, x, y, n)
			}
		}
		if sctx.inv != 0 {
			for i := range n {
				y[i] = -y[i]
			}
		}
	}
	if celtQuantBandTraceEnabled {
		finishQuantBandOutputTrace(&bandTrace, ctx, x, y, n, cm)
	}
	return cm
}

func quantBandStereoDecode(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int) int {
	if ctx.extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
		ctx.blocks, ctx.lm, ctx.lowbandScratch = B, lm, lowbandScratch
		return quantBandStereoDecodeNoExtFast(ctx, x[:n], y, b, lowband, lowbandOut, fill)
	}
	return quantBandStereoDecodeWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, ctx.extBudget)
}

func quantBandStereoDecodeWithExtBudget(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, extBudget int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}

	origFill := fill

	sctx := splitCtx{}
	computeThetaWithExtBudget(ctx, &sctx, x, y, n, &b, &extBudget, B, B, lm, true, &fill)
	mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)

	if n == 2 {
		mbits := b
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := sctx.itheta > 8192
		ctx.remainingBits -= sctx.qalloc + sbits

		x2 := x
		y2 := y
		if c {
			x2 = y
			y2 = x
		}
		sign := 1
		if sbits > 0 {
			if ctx.rd != nil {
				if ctx.rd.DecodeRawBit() == 1 {
					sign = -1
				}
			}
		}
		cm := quantBandDecodeWithExtBudget(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill, extBudget)
		sign32 := float32(sign)
		y2[0] = celtNorm(-sign32 * float32(x2[1]))
		y2[1] = celtNorm(sign32 * float32(x2[0]))
		if ctx.resynth {
			x[0] = celtNorm(float32(mid) * float32(x[0]))
			x[1] = celtNorm(float32(mid) * float32(x[1]))
			y[0] = celtNorm(float32(side) * float32(y[0]))
			y[1] = celtNorm(float32(side) * float32(y[1]))
			tmp := float32(x[0])
			y0 := float32(y[0])
			x[0] = celtNorm(noFMA32Sub(tmp, y0))
			y[0] = celtNorm(noFMA32Add(tmp, y0))
			tmp = float32(x[1])
			y1 := float32(y[1])
			x[1] = celtNorm(noFMA32Sub(tmp, y1))
			y[1] = celtNorm(noFMA32Add(tmp, y1))
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, mbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandDecodeWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, extBudget/2+qextExtra)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			sbits = min(sbits, ctx.remainingBits)
		}
		cm |= quantBandDecodeWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2-qextExtra)
	} else {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, sbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandDecodeWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2+qextExtra)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			mbits = min(mbits, ctx.remainingBits)
		}
		cm |= quantBandDecodeWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, extBudget/2-qextExtra)
	}

	if ctx.resynth {
		if n != 2 {
			stereoMerge(x, y, opusVal16(mid))
		}
		if sctx.inv != 0 {
			for i := range n {
				y[i] = -y[i]
			}
		}
	}
	return cm
}
