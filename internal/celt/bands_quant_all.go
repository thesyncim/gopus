package celt

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func quantAllBandsDecodeWithScratch(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int32, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32,
	scratch *bandDecodeScratch, extDec *rangecoding.Decoder, extraBits []int32, extTotalBits int) (left, right []celtNorm, collapse []byte) {
	if extDec == nil && scratch != nil && (channels == 2 || dualStereo == 0) {
		return quantAllBandsDecodeStd(rd, channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, totalBitsQ3, balance, codedBands, disableInv, seed, scratch)
	}
	return quantAllBandsDecodeWithScratchWithMode(rd, channels, frameSize, lm, start, end,
		pulses, shortBlocks, spread, dualStereo, intensity, tfRes, totalBitsQ3, balance,
		codedBands, disableInv, seed, scratch, extDec, extraBits, extTotalBits,
		nil, nil, nil, nil)
}

func clearDecodedBandEdges(buf []celtNorm, frameSize, start, end int) {
	if start < 0 {
		start = 0
	}
	if start > frameSize {
		start = frameSize
	}
	if end < start {
		end = start
	}
	if end > frameSize {
		end = frameSize
	}
	clear(buf[:start])
	clear(buf[end:frameSize])
}

// quantAllBandsDecodeStd is quantAllBandsDecodeWithScratchWithMode for the
// standard band layout without an extension decoder, the libopus
// quant_all_bands() decode setup of every CELT and Hybrid frame: it sizes the
// band scratch, clears the coefficients outside [start, end) and runs the mono
// or stereo band loop.
func quantAllBandsDecodeStd(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int32, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32,
	scratch *bandDecodeScratch) (left, right []celtNorm, collapse []byte) {
	start = max(start, 0)
	end = min(end, MaxBands)
	if end <= start {
		return nil, nil, nil
	}
	M := 1 << lm
	B := max(shortBlocks, 1)
	edges := EBands[:]
	normOffset := M * edges[start]
	normLen := max(M*edges[MaxBands-1]-normOffset, 0)
	// The standard mode's band widths never decrease, so its widest band in
	// [start, end) is the last.
	maxBand := M * eBandWidths[end-1]
	scratch.ensureFloatScratch(channels, frameSize, normLen, maxBand)
	// The band loop writes every coefficient of bands [start, end), so only
	// the coefficients outside them need clearing.
	left = ensureNormSliceNoClear(&scratch.left, frameSize)
	clearDecodedBandEdges(left, frameSize, M*edges[start], M*edges[end])
	if channels == 2 {
		right = ensureNormSliceNoClear(&scratch.right, frameSize)
		clearDecodedBandEdges(right, frameSize, M*edges[start], M*edges[end])
	} else if cap(scratch.right) > 0 {
		scratch.right = scratch.right[:0]
	}
	collapse = ensureByteSlice(&scratch.collapse, channels*MaxBands)
	clear(collapse)
	norm := ensureNormSliceNoClear(&scratch.norm, channels*normLen)
	lowbandScratch := ensureNormSliceNoClear(&scratch.lowband, maxBand)
	if edges[end]*M > frameSize {
		// The final physical band is the folding scratch.
		effectiveEnd := end
		for edges[effectiveEnd]*M > frameSize {
			effectiveEnd--
		}
		lowbandScratch = left[edges[effectiveEnd-1]*M:]
	}
	ctx := bandCtx{
		rd:              rd,
		spread:          spread,
		intensity:       intensity,
		resynth:         true,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
		scratch:         scratch,
		bandEdges:       edges,
		stdCache:        true,
		lm:              lm,
		blocks:          B,
		lowbandScratch:  lowbandScratch,
	}
	if seed != nil {
		ctx.seed = *seed
		ctx.seedActive = true
	}
	if channels == 1 {
		quantAllBandsDecodeMono(&ctx, left, norm, collapse, edges, pulses, tfRes,
			start, end, normOffset, totalBitsQ3, balance, codedBands)
	} else {
		quantAllBandsDecodeStereo(&ctx, left, right, norm, norm[normLen:], collapse, edges, pulses, tfRes,
			frameSize, start, end, normOffset, totalBitsQ3, balance, codedBands, dualStereo)
	}
	if seed != nil {
		*seed = ctx.seed
	}
	return left, right, collapse
}

func quantAllBandsDecodeWithScratchWithMode(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int32, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32,
	scratch *bandDecodeScratch, extDec *rangecoding.Decoder, extraBits []int32, extTotalBits int,
	bandEdges []int, bandLogN []int, cacheIndex []int16, cacheBits []uint8) (left, right []celtNorm, collapse []byte) {
	M := 1 << lm
	B := max(shortBlocks, 1)
	edges := bandEdges
	if len(edges) < 2 {
		edges = EBands[:]
	}
	maxBands := len(edges) - 1
	if maxBands <= 0 {
		return nil, nil, nil
	}
	if start < 0 {
		start = 0
	}
	if end > maxBands {
		end = maxBands
	}
	if end <= start {
		return nil, nil, nil
	}
	N := frameSize
	normOffset := M * edges[start]
	normLen := max(M*edges[maxBands-1]-normOffset, 0)
	var maxWidth int
	if len(bandEdges) < 2 {
		// The standard mode's band widths never decrease, so its widest
		// band in [start, end) is the last.
		maxWidth = eBandWidths[end-1]
	} else {
		prevEdge := edges[start]
		for _, e := range edges[start+1 : end+1] {
			maxWidth = max(maxWidth, e-prevEdge)
			prevEdge = e
		}
	}
	maxBand := M * maxWidth
	if scratch != nil {
		// Back the band-decode-local float scratch with one contiguous arena
		// before the inline/getter sizing below reslices within each slot.
		scratch.ensureFloatScratch(channels, N, normLen, maxBand)
	}
	if scratch == nil {
		left = make([]celtNorm, N)
		if channels == 2 {
			right = make([]celtNorm, N)
		}
		collapse = make([]byte, channels*maxBands)
	} else {
		// The band loop writes every coefficient of bands [start, end), so only
		// the coefficients outside them need clearing.
		left = ensureNormSliceNoClear(&scratch.left, N)
		clearDecodedBandEdges(left, N, M*edges[start], M*edges[end])
		if channels == 2 {
			right = ensureNormSliceNoClear(&scratch.right, N)
			clearDecodedBandEdges(right, N, M*edges[start], M*edges[end])
		} else if cap(scratch.right) > 0 {
			scratch.right = scratch.right[:0]
			right = nil
		}
		collapse = ensureByteSlice(&scratch.collapse, channels*maxBands)
		for i := range collapse {
			collapse[i] = 0
		}
	}

	var norm []celtNorm
	if scratch == nil {
		norm = make([]celtNorm, channels*normLen)
	} else {
		norm = ensureNormSliceNoClear(&scratch.norm, channels*normLen)
	}
	var norm2 []celtNorm
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	var lowbandScratch []celtNorm
	if scratch == nil {
		lowbandScratch = make([]celtNorm, maxBand)
	} else {
		lowbandScratch = ensureNormSliceNoClear(&scratch.lowband, maxBand)
	}

	if edges[end]*M > frameSize {
		// The decoder uses its final physical band as folding scratch, even
		// when additional signaled QEXT bands are decoded only for their bits.
		effectiveEnd := end
		for edges[effectiveEnd]*M > frameSize {
			effectiveEnd--
		}
		lowbandScratch = left[edges[effectiveEnd-1]*M:]
	}

	lowbandOffset := 0
	updateLowband := true
	extraBands := extDec != nil && extraBits != nil && start == 0 && len(edges) >= 2 && edges[0] > 0 && (end == nbQEXTBands || end == 2)
	bandCapsSlice := []int32(nil)
	if channels == 2 && extDec != nil && !extraBands {
		var bandCaps [MaxBands]int32
		initCapsInto(bandCaps[:end], end, lm, channels)
		bandCapsSlice = bandCaps[:end]
	}
	ctx := bandCtx{
		rd:              rd,
		encode:          false,
		spread:          spread,
		remainingBits:   0,
		intensity:       intensity,
		resynth:         true,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
		scratch:         scratch,
		extTotalBits:    extTotalBits,
		extraBands:      extraBands,
		bandEdges:       edges,
		bandLogN:        bandLogN,
		cacheIndex:      cacheIndex,
		cacheBits:       cacheBits,
		bandCaps:        bandCapsSlice,
		stdCache:        len(cacheIndex) == 0 && len(cacheBits) == 0,
		lm:              lm,
		blocks:          B,
		lowbandScratch:  lowbandScratch,
	}
	if seed != nil {
		ctx.seed = *seed
		ctx.seedActive = true
	}
	if channels == 1 && dualStereo == 0 && extDec == nil {
		quantAllBandsDecodeMono(&ctx, left, norm, collapse, edges, pulses, tfRes,
			start, end, normOffset, totalBitsQ3, balance, codedBands)
		if seed != nil {
			*seed = ctx.seed
		}
		return left, right, collapse
	}
	if channels == 2 && extDec == nil {
		quantAllBandsDecodeStereo(&ctx, left, right, norm, norm2, collapse, edges, pulses, tfRes,
			frameSize, start, end, normOffset, totalBitsQ3, balance, codedBands, dualStereo)
		if seed != nil {
			*seed = ctx.seed
		}
		return left, right, collapse
	}
	extBalance := 0
	extTell := 0

	for i := start; i < end; i++ {
		ctx.band = i
		ctx.extBudget = 0
		if extDec != nil && extraBits != nil {
			ctx.extDec = extDec
			if i != start && i-1 < len(extraBits) {
				extBalance += int(extraBits[i-1]) + extTell
			}
			extTell = extDec.TellFrac()
			if i != start {
				extBalance -= extTell
			}
			if i <= codedBands-1 && i < len(extraBits) {
				extCurrBalance := celtSudivBalance(extBalance, min(3, codedBands-i))
				extRemaining := ctx.extTotalBits - extTell
				ctx.extBudget = max(0, min(16383, min(extRemaining, int(extraBits[i])+extCurrBalance)))
			}
		}
		last := i == end-1
		bandStart := edges[i] * M
		bandEnd := edges[i+1] * M
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		var x, y []celtNorm
		if bandEnd > frameSize {
			// celt/bands.c quant_all_bands routes bands beyond effEBands
			// into the shared normalization scratch while consuming their bits.
			x = norm[:nBand]
			if channels == 2 {
				y = norm[:nBand]
			}
			lowbandScratch = nil
		} else {
			x = left[bandStart:bandEnd]
			if channels == 2 {
				y = right[bandStart:bandEnd]
			}
		}

		tell := rd.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		currBalance := 0
		if i <= codedBands-1 {
			currBalance = celtSudivBalance(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, int(pulses[i])+currBalance)))
		}
		if ctx.resynth && (M*edges[i]-nBand >= M*edges[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0)
		}

		ctx.tfChange = int(tfRes[i])
		if last {
			lowbandScratch = nil
		}

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = max(0, M*edges[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if M*edges[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if M*edges[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				mergeLimit := min(max(M*edges[i]-normOffset, 0), len(norm))
				if channels == 2 && mergeLimit > len(norm2) {
					mergeLimit = len(norm2)
				}
				for j := 0; j < mergeLimit; j++ {
					norm[j] = celtNorm(float32(0.5) * (float32(norm[j]) + float32(norm2[j])))
				}
			}
		}

		var lowbandX []celtNorm
		var lowbandY []celtNorm
		if effectiveLowband >= 0 && effectiveLowband+nBand <= len(norm) {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 && effectiveLowband+nBand <= len(norm2) {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}

		var lowbandOutX []celtNorm
		var lowbandOutY []celtNorm
		outStart := M*edges[i] - normOffset
		if !last && outStart >= 0 && outStart+nBand <= len(norm) {
			lowbandOutX = norm[outStart : outStart+nBand]
			if channels == 2 && outStart+nBand <= len(norm2) {
				lowbandOutY = norm2[outStart : outStart+nBand]
			}
		}

		if dualStereo != 0 {
			if ctx.extDec != nil {
				// The QEXT branch in pinned celt/bands.c quant_all_bands
				// passes ext_b/2 to each dual-stereo quant_band call.
				xCM = quantBandDecodeWithExtBudget(&ctx, x, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM, ctx.extBudget/2)
				if channels == 2 {
					yCM = quantBandDecodeWithExtBudget(&ctx, y, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM, ctx.extBudget/2)
				}
			} else {
				xCM = quantBandDecode(&ctx, x, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM)
				if channels == 2 {
					yCM = quantBandDecode(&ctx, y, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM)
				}
			}
		} else {
			if channels == 2 {
				xCM = quantBandStereoDecode(&ctx, x, y, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM)
				yCM = xCM
			} else {
				// Mono bands without QEXT go straight to the fast path
				// quantBandDecode would pick.
				if ctx.extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
					ctx.lowbandScratch = lowbandScratch
					xCM = quantBandDecodeNoExtFast(&ctx, x, b, lowbandX, lowbandOutX, 1.0, xCM|yCM)
				} else {
					xCM = quantBandDecode(&ctx, x, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM)
				}
				yCM = xCM
			}
		}

		collapse[i*channels] = byte(xCM)
		if channels == 2 {
			collapse[i*channels+channels-1] = byte(yCM)
		}
		balance += int(pulses[i]) + tell

		updateLowband = b > (nBand << bitRes)
		ctx.avoidSplitNoise = false
	}
	if seed != nil {
		*seed = ctx.seed
	}

	return left, right, collapse
}

// quantAllBandsDecodeStereo is the band loop of
// quantAllBandsDecodeWithScratchWithMode for a stereo frame without QEXT, the
// libopus quant_all_bands() decode loop with C == 2 and no extension decoder.
// left and right receive the decoded bands, norm and norm2 hold the folding
// history of each channel and collapse the interleaved per-band collapse
// masks.
func quantAllBandsDecodeStereo(ctx *bandCtx, left, right, norm, norm2 []celtNorm, collapse []byte, edges []int,
	pulses, tfRes []int32, frameSize, start, end, normOffset, totalBitsQ3, balance, codedBands, dualStereo int) {
	rd := ctx.rd
	lm := ctx.lm
	B := ctx.blocks
	startEdge := edges[start] << lm
	lowbandOffset := 0
	updateLowband := true
	// Slicing the per-band tables to end lets the band loop index them
	// without bounds checks.
	edges = edges[:end+1]
	pulses = pulses[:end]
	tfRes = tfRes[:end]
	collapse = collapse[:2*end]
	for i := start; i < end; i++ {
		ctx.band = i
		last := i == end-1
		bandStart := edges[i] << lm
		bandEnd := edges[i+1] << lm
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		var x, y []celtNorm
		if bandEnd > frameSize {
			// celt/bands.c quant_all_bands routes bands beyond effEBands
			// into the shared normalization scratch while consuming their bits.
			x = norm[:nBand]
			y = norm[:nBand]
			ctx.lowbandScratch = nil
		} else {
			x = left[bandStart:bandEnd]
			y = right[bandStart:bandEnd]
		}

		tell := rd.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		if i < codedBands {
			currBalance := celtSudivBalance(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, int(pulses[i])+currBalance)))
		}
		balance += int(pulses[i]) + tell
		if (bandStart-nBand >= startEdge || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		updateLowband = b > (nBand << bitRes)
		if i == start+1 {
			specialHybridFoldingWithEdges(norm, norm2, edges, start, 1<<lm, dualStereo != 0)
		}
		outStart := bandStart - normOffset
		if last {
			ctx.lowbandScratch = nil
			outStart = -1
		}

		if nBand == 1 && dualStereo == 0 {
			// quant_band_n1 for both channels: a raw sign bit each while a
			// whole bit remains, no lowband, and full collapse masks.
			v0, v1 := celtNorm(1.0), celtNorm(1.0)
			if remaining >= 1<<bitRes {
				if rd.DecodeRawBit() != 0 {
					v0 = -1.0
				}
				remaining -= 1 << bitRes
			}
			if remaining >= 1<<bitRes && rd.DecodeRawBit() != 0 {
				v1 = -1.0
			}
			x[0] = v0
			y[0] = v1
			if uint(outStart) < uint(len(norm)) {
				norm[outStart] = v0
			}
			collapse[2*i] = 1
			collapse[2*i+1] = 1
			ctx.avoidSplitNoise = false
			continue
		}

		ctx.tfChange = int(tfRes[i])
		var lowbandX, lowbandY []celtNorm
		xCM, yCM := (1<<B)-1, (1<<B)-1
		if lowbandOffset != 0 && (ctx.spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband := max(0, edges[lowbandOffset]<<lm-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if edges[foldStart]<<lm <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if edges[foldEnd]<<lm >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			xCM, yCM = 0, 0
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[2*fold])
				yCM |= int(collapse[2*fold+1])
			}
			if effectiveLowband+nBand <= len(norm2) {
				lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}

		if dualStereo != 0 && i == ctx.intensity {
			dualStereo = 0
			mergeLimit := min(max(bandStart-normOffset, 0), len(norm2))
			for j := range mergeLimit {
				norm[j] = celtNorm(float32(0.5) * (float32(norm[j]) + float32(norm2[j])))
			}
		}

		var lowbandOutX, lowbandOutY []celtNorm
		if outStart >= 0 && outStart+nBand <= len(norm2) {
			lowbandOutX = norm[outStart : outStart+nBand]
			lowbandOutY = norm2[outStart : outStart+nBand]
		}

		if dualStereo != 0 {
			xCM = quantBandDecodeNoExtFast(ctx, x, b/2, lowbandX, lowbandOutX, 1.0, xCM)
			yCM = quantBandDecodeNoExtFast(ctx, y, b/2, lowbandY, lowbandOutY, 1.0, yCM)
		} else {
			xCM = quantBandStereoDecodeNoExtFast(ctx, x, y, b, lowbandX, lowbandOutX, xCM|yCM)
			yCM = xCM
		}
		collapse[2*i] = byte(xCM)
		collapse[2*i+1] = byte(yCM)
		ctx.avoidSplitNoise = false
	}
}

// quantAllBandsDecodeMono is the band loop of quantAllBandsDecodeWithScratchWithMode
// for a mono frame without QEXT, the libopus quant_all_bands() decode loop
// with C == 1, dual_stereo == 0 and no extension decoder. left receives the
// decoded bands, norm holds the folding history and collapse the per-band
// collapse masks.
func quantAllBandsDecodeMono(ctx *bandCtx, left, norm []celtNorm, collapse []byte, edges []int,
	pulses, tfRes []int32, start, end, normOffset, totalBitsQ3, balance, codedBands int) {
	rd := ctx.rd
	lm := ctx.lm
	B := ctx.blocks
	startEdge := edges[start] << lm
	lowbandOffset := 0
	updateLowband := true
	// Slicing the per-band tables to end lets the band loop index them
	// without bounds checks.
	edges = edges[:end+1]
	pulses = pulses[:end]
	tfRes = tfRes[:end]
	collapse = collapse[:end]
	for i := start; i < end; i++ {
		ctx.band = i
		bandStart := edges[i] << lm
		nBand := edges[i+1]<<lm - bandStart
		if nBand <= 0 {
			continue
		}
		x := left[bandStart : bandStart+nBand]

		tell := rd.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining
		b := 0
		if i < codedBands {
			currBalance := celtSudivBalance(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, int(pulses[i])+currBalance)))
		}
		balance += int(pulses[i]) + tell
		if (bandStart-nBand >= startEdge || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		updateLowband = b > (nBand << bitRes)
		if i == start+1 {
			specialHybridFoldingWithEdges(norm, nil, edges, start, 1<<lm, false)
		}
		outStart := bandStart - normOffset
		if i == end-1 {
			ctx.lowbandScratch = nil
			outStart = -1
		}
		if nBand == 1 {
			// quant_band_n1: one raw sign bit when a whole bit remains, no
			// lowband, and a full collapse mask.
			v := celtNorm(1.0)
			if remaining >= 1<<bitRes && rd.DecodeRawBit() != 0 {
				v = -1.0
			}
			x[0] = v
			if uint(outStart) < uint(len(norm)) {
				norm[outStart] = v
			}
			collapse[i] = 1
			ctx.avoidSplitNoise = false
			continue
		}

		ctx.tfChange = int(tfRes[i])
		var lowbandX []celtNorm
		xCM := (1 << B) - 1
		if lowbandOffset != 0 && (ctx.spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband := max(0, edges[lowbandOffset]<<lm-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if edges[foldStart]<<lm <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if edges[foldEnd]<<lm >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			xCM = 0
			for _, cm := range collapse[foldStart:foldEnd] {
				xCM |= int(cm)
			}
			if effectiveLowband+nBand <= len(norm) {
				lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			}
		}

		var lowbandOutX []celtNorm
		if outStart >= 0 && outStart+nBand <= len(norm) {
			lowbandOutX = norm[outStart : outStart+nBand]
		}
		if B == 1 && ctx.tfChange == 0 {
			// quant_band() of one long block without a TF change.
			collapse[i] = byte(quantPartitionDecodeNoExt(ctx, x, b, 1, lowbandX, lm, 1.0, xCM) & 1)
			if lowbandOutX != nil {
				scaleLowbandOutForFoldingNorm(lowbandOutX, x, nBand)
			}
		} else {
			collapse[i] = byte(quantBandDecodeNoExtFast(ctx, x, b, lowbandX, lowbandOutX, 1.0, xCM))
		}
		ctx.avoidSplitNoise = false
	}
}

// quantAllBandsEncode encodes all frequency bands using PVQ quantization.
// Parameters:
//   - re: range encoder for the main bitstream
//   - channels: number of audio channels (1 or 2)
//   - frameSize: frame size in samples
//   - lm: log2 of M (time resolution multiplier)
//   - start, end: band range to encode
//   - x, y: normalized MDCT coefficients for left/right channels
//   - pulses: bit allocation per band
//   - shortBlocks: number of short blocks (1 for long block)
//   - spread: spreading parameter (0-3)
//   - tapset: window taper selection for comb filter (0-2), tracked for state consistency
//   - dualStereo: whether to use dual stereo mode
//   - intensity: intensity stereo band threshold
//   - tfRes: time-frequency resolution per band
//   - totalBitsQ3: total bit budget in Q3 format
//   - balance: bit balance for allocation
//   - codedBands: number of bands that will be coded
//   - seed: RNG seed for noise filling
//   - complexity: encoder complexity (0-10)
//   - bandE: band energies for stereo decisions
//   - extEnc: extended encoder for high-precision mode (can be nil)
//   - extraBits: per-band QEXT budgets in Q3 bits (can be nil)
//
// Reference: libopus celt/bands.c quant_all_bands()
// quantAllBandsEncodeScratch is the scratch-aware version of quantAllBandsEncode.
func quantAllBandsEncodeScratch(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	x, y []celtNorm, pulses []int32, shortBlocks int, spread int, tapset int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32, complexity int,
	bandE []celtEner, extEnc *rangecoding.Encoder, extraBits []int32, scratch *bandEncodeScratch) (collapse []byte) {
	return quantAllBandsEncodeScratchWithMode(re, channels, frameSize, lm, start, end,
		x, y, pulses, shortBlocks, spread, tapset, dualStereo, intensity,
		tfRes, totalBitsQ3, balance, codedBands, disableInv, seed, complexity,
		bandE, extEnc, extraBits, scratch, nil, nil, nil, nil)
}

func quantAllBandsEncodeScratchWithMode(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	x, y []celtNorm, pulses []int32, shortBlocks int, spread int, tapset int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32, complexity int,
	bandE []celtEner, extEnc *rangecoding.Encoder, extraBits []int32, scratch *bandEncodeScratch,
	bandEdges []int, bandLogN []int, cacheIndex []int16, cacheBits []uint8) (collapse []byte) {
	if re == nil {
		return nil
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if len(x) < frameSize {
		return nil
	}

	edges := bandEdges
	if len(edges) < 2 {
		edges = EBands[:]
	}
	maxBands := len(edges) - 1
	if maxBands <= 0 {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if end > maxBands {
		end = maxBands
	}
	if end <= start {
		return nil
	}

	M := 1 << lm
	B := max(shortBlocks, 1)

	// Use scratch buffers if available
	if scratch != nil {
		collapse = scratch.ensureCollapse(channels * maxBands)
		clear(collapse)
	} else {
		collapse = make([]byte, channels*maxBands)
	}

	normOffset := M * edges[start]
	normLen := max(M*edges[maxBands-1]-normOffset, 0)

	var norm []celtNorm
	if scratch != nil {
		// ensureNorm already zero-fills (via ensureNormSlice clear).
		norm = scratch.ensureNorm(channels * normLen)
	} else {
		norm = make([]celtNorm, channels*normLen)
	}
	var norm2 []celtNorm
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	maxBand := 0
	for i := start; i < end; i++ {
		if w := M * (edges[i+1] - edges[i]); w > maxBand {
			maxBand = w
		}
	}

	var lowbandScratch []celtNorm
	if scratch != nil {
		lowbandScratch = scratch.ensureLowbandScratch(maxBand)
	} else {
		lowbandScratch = make([]celtNorm, maxBand)
	}

	lowbandOffset := 0
	updateLowband := true
	extraBands := extEnc != nil && extraBits != nil && start == 0 && len(edges) >= 2 && edges[0] > 0 && (end == nbQEXTBands || end == 2)
	thetaRDOEnabled := channels == 2 && dualStereo == 0 && complexity >= 8 && !extraBands
	// The standard-mode encoder with no extension coder takes the
	// bands_quant_encode.go functions.
	stdEnc := extEnc == nil && scratch != nil && len(bandLogN) == 0 && len(cacheIndex) == 0 && len(cacheBits) == 0
	var bandCaps [MaxBands]int32
	bandCapsSlice := []int32(nil)
	if channels == 2 && extEnc != nil && !extraBands {
		initCapsInto(bandCaps[:end], end, lm, channels)
		bandCapsSlice = bandCaps[:end]
	}
	ctx := bandCtx{
		re:            re,
		encode:        true,
		extEnc:        nil,
		extBudget:     0,
		extTotalBits:  0,
		extraBands:    extraBands,
		bandE:         bandE,
		nbBands:       end,
		channels:      channels,
		spread:        spread,
		remainingBits: 0,
		intensity:     intensity,
		bandEdges:     edges,
		bandLogN:      bandLogN,
		cacheIndex:    cacheIndex,
		cacheBits:     cacheBits,
		bandCaps:      bandCapsSlice,
		// Match libopus encode-side default: resynth only when theta RDO is active.
		// (decode path remains resynth=true).
		resynth:         thetaRDOEnabled,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
		tapset:          tapset,
		encScratch:      scratch,
		stdEnc:          stdEnc,
	}
	if seed != nil {
		ctx.seed = *seed
		ctx.seedActive = true
	}
	if ctx.channels > 0 && ctx.bandE != nil {
		ctx.nbBands = len(ctx.bandE) / ctx.channels
	}
	if extEnc != nil {
		ctx.extTotalBits = extEnc.StorageBits() << bitRes
	}
	extBalance := 0
	extTell := 0

	for i := start; i < end; i++ {
		ctx.band = i
		ctx.extBudget = 0
		if extEnc != nil && extraBits != nil {
			ctx.extEnc = extEnc
			if i != start && i-1 < len(extraBits) {
				extBalance += int(extraBits[i-1]) + extTell
			}
			extTell = extEnc.TellFrac()
			if i != start {
				extBalance -= extTell
			}
			if i <= codedBands-1 && i < len(extraBits) {
				extCurrBalance := celtSudivBalance(extBalance, min(3, codedBands-i))
				extRemaining := ctx.extTotalBits - extTell
				ctx.extBudget = max(0, min(16383, min(extRemaining, int(extraBits[i])+extCurrBalance)))
			}
		}
		last := i == end-1
		bandStart := edges[i] * M
		bandEnd := edges[i+1] * M
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		xBand := x[bandStart:bandEnd]
		var yBand []celtNorm
		if channels == 2 && len(y) >= bandEnd {
			yBand = y[bandStart:bandEnd]
		}

		tell := re.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		currBalance := 0
		if i <= codedBands-1 {
			currBalance = celtSudivBalance(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, int(pulses[i])+currBalance)))
		}
		if ctx.resynth && (M*edges[i]-nBand >= M*edges[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0)
		}

		ctx.tfChange = 0
		if tfRes != nil && i < len(tfRes) {
			ctx.tfChange = int(tfRes[i])
		}

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = max(0, M*edges[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if M*edges[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if M*edges[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				mergeLimit := min(max(M*edges[i]-normOffset, 0), len(norm))
				if channels == 2 && mergeLimit > len(norm2) {
					mergeLimit = len(norm2)
				}
				for j := 0; j < mergeLimit; j++ {
					norm[j] = celtNorm(float32(0.5) * (float32(norm[j]) + float32(norm2[j])))
				}
			}
		}

		var lowbandX []celtNorm
		var lowbandY []celtNorm
		if effectiveLowband >= 0 && effectiveLowband+nBand <= len(norm) {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 && effectiveLowband+nBand <= len(norm2) {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}

		var lowbandOutX []celtNorm
		var lowbandOutY []celtNorm
		outStart := M*edges[i] - normOffset
		if !last && outStart >= 0 && outStart+nBand <= len(norm) {
			lowbandOutX = norm[outStart : outStart+nBand]
			if channels == 2 && outStart+nBand <= len(norm2) {
				lowbandOutY = norm2[outStart : outStart+nBand]
			}
		}

		if stdEnc {
			switch {
			case dualStereo != 0:
				xCM = quantBandEnc(&ctx, xBand, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM)
				if yBand != nil {
					yCM = quantBandEnc(&ctx, yBand, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM)
				}
			case yBand != nil && thetaRDOEnabled && i < intensity:
				var leftE, rightE celtEner
				if bandE != nil && len(bandE) > ctx.nbBands+i {
					leftE = bandE[i]
					rightE = bandE[ctx.nbBands+i]
				}
				if i == start+1 {
					// The rounded-up trial of this band runs on a refolded norm.
					refold := func() { specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0) }
					xCM = quantBandStereoThetaRDO(&ctx, re, scratch, xBand, yBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch,
						xCM|yCM, leftE, rightE, refold)
				} else {
					xCM = quantBandStereoThetaRDOEnc(&ctx, re, scratch, xBand, yBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch,
						xCM|yCM, leftE, rightE)
				}
				yCM = xCM
			case yBand != nil:
				xCM = quantBandStereoEnc(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM)
				yCM = xCM
			default:
				xCM = quantBandEnc(&ctx, xBand, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM)
				yCM = xCM
			}
		} else if dualStereo != 0 {
			xCM = quantBandWithExtBudget(&ctx, xBand, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM, ctx.extBudget/2)
			if channels == 2 && yBand != nil {
				yCM = quantBandWithExtBudget(&ctx, yBand, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM, ctx.extBudget/2)
			}
		} else {
			if channels == 2 && yBand != nil {
				// Theta RDO: Try both rounding directions and pick the one with lower distortion.
				// Enabled only for high complexity stereo (match libopus theta_rdo).
				// Reference: libopus bands.c quant_all_bands(), theta_rdo logic
				if thetaRDOEnabled && i < intensity {
					var leftE, rightE celtEner
					if bandE != nil && len(bandE) > ctx.nbBands+i {
						leftE = bandE[i]
						rightE = bandE[ctx.nbBands+i]
					}
					var refold func()
					if i == start+1 {
						refold = func() { specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0) }
					}
					xCM = quantBandStereoThetaRDO(&ctx, re, scratch, xBand, yBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch,
						xCM|yCM, leftE, rightE, refold)
					yCM = xCM
				} else {
					// No theta RDO: use standard encoding
					ctx.thetaRound = 0
					xCM = quantBandStereoWithExtBudget(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM, ctx.extBudget)
					yCM = xCM
				}
			} else {
				xCM = quantBandWithExtBudget(&ctx, xBand, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM, ctx.extBudget)
				yCM = xCM
			}
		}

		collapse[i*channels] = byte(xCM)
		if channels == 2 {
			collapse[i*channels+channels-1] = byte(yCM)
		}
		balance += int(pulses[i]) + tell

		updateLowband = b > (nBand << bitRes)
		ctx.avoidSplitNoise = false
	}
	if seed != nil {
		*seed = ctx.seed
	}

	return collapse
}

// quantBandStereoThetaRDO is the theta_rdo branch of libopus quant_all_bands():
// it codes the stereo band with theta rounded down, codes it again from the
// same coder state with theta rounded up, and keeps the trial whose output has
// the larger weighted inner product with the input. refold, when set, redoes
// the special hybrid folding the first trial overwrote.
func quantBandStereoThetaRDO(ctx *bandCtx, re *rangecoding.Encoder, scratch *bandEncodeScratch,
	x, y []celtNorm, b, B int, lowband []celtNorm, lm int, lowbandOut, lowbandScratch []celtNorm,
	fill int, leftE, rightE celtEner, refold func()) int {
	var rdoTrace quantBandTraceState
	if celtQuantBandTraceEnabled {
		rdoTrace = beginQuantRDOTrace(ctx, x, y, len(x), b, B, lm)
	}
	if scratch == nil {
		scratch = &bandEncodeScratch{}
	}
	n := len(x)
	w0, w1 := computeChannelWeights(leftE, rightE)
	xSave := scratch.ensureXSave(n)
	ySave := scratch.ensureYSave(n)
	copy(xSave, x)
	copy(ySave, y)
	ecSave := &scratch.ecSave
	extSave := &scratch.extEcSave
	re.SaveStateShallowInto(ecSave)
	if ctx.extEnc != nil {
		ctx.extEnc.SaveStateShallowInto(extSave)
	}
	// quantBandStereoWithExtBudget changes only remainingBits and seed in
	// ctx, so those two fields carry the libopus ctx_save.
	remainingSave, seedSave := ctx.remainingBits, ctx.seed

	ctx.thetaRound = -1
	var cm0 int
	if ctx.stdEnc {
		cm0 = quantBandStereoEnc(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill)
	} else {
		cm0 = quantBandStereoWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, ctx.extBudget)
	}
	dist0 := thetaRDODistortion(w0, w1, xSave, x, ySave, y)
	var thetaContext0 quantBandTraceContext
	if celtQuantBandTraceEnabled {
		thetaContext0 = quantBandTraceLastBandOutputContext()
	}

	// Keep the first trial: coder state and the bytes written since ecSave,
	// band output and folding output.
	re.SaveStateSinceInto(&scratch.ecSave0, ecSave)
	if ctx.extEnc != nil {
		ctx.extEnc.SaveStateSinceInto(&scratch.extEcSave0, extSave)
	}
	remainingSave0, seedSave0 := ctx.remainingBits, ctx.seed
	xSave0 := scratch.ensureXResult0(n)
	ySave0 := scratch.ensureYResult0(n)
	copy(xSave0, x)
	copy(ySave0, y)
	var normSave0 []celtNorm
	if lowbandOut != nil {
		normSave0 = scratch.ensureNormResult0(n)
		copy(normSave0, lowbandOut)
	}

	re.RestoreStateShallow(ecSave)
	if ctx.extEnc != nil {
		ctx.extEnc.RestoreStateShallow(extSave)
	}
	ctx.remainingBits, ctx.seed = remainingSave, seedSave
	copy(x, xSave)
	copy(y, ySave)
	if refold != nil {
		refold()
	}

	ctx.thetaRound = 1
	var cm int
	if ctx.stdEnc {
		cm = quantBandStereoEnc(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill)
	} else {
		cm = quantBandStereoWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, ctx.extBudget)
	}
	dist1 := thetaRDODistortion(w0, w1, xSave, x, ySave, y)
	var thetaContext1 quantBandTraceContext
	if celtQuantBandTraceEnabled {
		thetaContext1 = quantBandTraceLastBandOutputContext()
	}
	if dist0 >= dist1 {
		cm = cm0
		re.RestoreState(&scratch.ecSave0)
		if ctx.extEnc != nil {
			ctx.extEnc.RestoreState(&scratch.extEcSave0)
		}
		ctx.remainingBits, ctx.seed = remainingSave0, seedSave0
		copy(x, xSave0)
		copy(y, ySave0)
		if normSave0 != nil {
			copy(lowbandOut, normSave0)
		}
	}
	if celtQuantBandTraceEnabled {
		selectedRound := 1
		selectedContext := thetaContext1
		if dist0 >= dist1 {
			selectedRound = -1
			selectedContext = thetaContext0
		}
		finishQuantRDOTrace(&rdoTrace, ctx, x, y, n, selectedRound, dist0, dist1, selectedContext)
	}
	ctx.thetaRound = 0
	ctx.rdoIthetaSet = false
	return cm
}
