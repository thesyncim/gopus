// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
// CELT (Constrained Energy Lapped Transform) is the transform-based layer
// of Opus for music and general audio.
package celt

// MaxBands is the maximum number of frequency bands in CELT.
// These are Bark-scale bands covering 0-20kHz at 48kHz sample rate.
const MaxBands = 21

// Overlap is the number of overlap samples at 48kHz (2.5ms window overlap).
// This is fixed for all CELT frame sizes.
const Overlap = 120

// DB6 is the coarse energy quantization step size in decibels.
// Coarse energy uses 6 dB steps.
const DB6 = 6.0

// PreemphCoef is the de-emphasis filter coefficient.
// The encoder applies pre-emphasis; decoder applies inverse de-emphasis:
// y[n] = x[n] + PreemphCoef * y[n-1]
const PreemphCoef = 0.85

// SilkCELTDelay is the delay compensation in samples at 48kHz for hybrid mode.
// SILK needs to be delayed relative to CELT for proper time alignment.
const SilkCELTDelay = 60

// EBands contains the MDCT bin indices for band edges at 48kHz with 5ms base frame.
// These 22 values define 21 bands. Each band spans from EBands[i] to EBands[i+1].
// For other frame sizes, these indices are scaled appropriately.
//
// Frequency boundaries (approximate):
// 0Hz, 200Hz, 400Hz, 600Hz, 800Hz, 1000Hz, 1200Hz, 1400Hz, 1600Hz, 2000Hz,
// 2400Hz, 2800Hz, 3200Hz, 4000Hz, 4800Hz, 5600Hz, 6800Hz, 8000Hz, 9600Hz,
// 12000Hz, 15600Hz, 20000Hz
//
// Source: libopus celt/modes.c (eBand5ms table)
var EBands = [22]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
	12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
	78, 100,
}

// AlphaCoef contains inter-frame energy prediction coefficients by LM (log mode).
// Used for coarse energy decoding in inter-frame mode.
// Index corresponds to LM: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms
//
// Source: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c
var AlphaCoef = [4]float64{
	29440.0 / 32768.0, // LM=0 (2.5ms): 0.8984375
	26112.0 / 32768.0, // LM=1 (5ms):   0.796875
	21248.0 / 32768.0, // LM=2 (10ms):  0.6484375
	16384.0 / 32768.0, // LM=3 (20ms):  0.5
}

// BetaCoefInter contains inter-band energy prediction coefficients for INTER-frame mode.
// Values vary by LM (log mode / frame size). Source: libopus celt/quant_bands.c
var BetaCoefInter = [4]float64{
	30147.0 / 32768.0, // LM=0 (2.5ms): 0.9200744...
	22282.0 / 32768.0, // LM=1 (5ms):   0.6800537...
	12124.0 / 32768.0, // LM=2 (10ms):  0.3700561...
	6554.0 / 32768.0,  // LM=3 (20ms):  0.2000122...
}

// BetaIntra is the inter-band prediction coefficient for INTRA-frame mode.
// No inter-frame prediction, only inter-band. Source: libopus celt/quant_bands.c
const BetaIntra = 4915.0 / 32768.0 // 0.15

// LogN contains log2 of band widths (in Q8 fixed-point) for bit allocation.
// This is used in the bit allocation algorithm to weight bands.
// For band i, width = EBands[i+1] - EBands[i], and LogN[i] = round(log2(width) * 256)
//
// Source: libopus celt/modes.c (logN400 table)
var LogN = [21]int{
	0,   // Band 0:  width=1, log2(1)=0
	0,   // Band 1:  width=1, log2(1)=0
	0,   // Band 2:  width=1, log2(1)=0
	0,   // Band 3:  width=1, log2(1)=0
	0,   // Band 4:  width=1, log2(1)=0
	0,   // Band 5:  width=1, log2(1)=0
	0,   // Band 6:  width=1, log2(1)=0
	0,   // Band 7:  width=1, log2(1)=0
	256, // Band 8:  width=2, log2(2)=1
	256, // Band 9:  width=2, log2(2)=1
	256, // Band 10: width=2, log2(2)=1
	256, // Band 11: width=2, log2(2)=1
	512, // Band 12: width=4, log2(4)=2
	512, // Band 13: width=4, log2(4)=2
	512, // Band 14: width=4, log2(4)=2
	717, // Band 15: width=6, log2(6)~=2.58
	768, // Band 16: width=6, log2(6)~=2.58 (rounded)
	858, // Band 17: width=8, log2(8)=3 (adj for allocation)
	922, // Band 18: width=12, log2(12)~=3.58
	1024, // Band 19: width=18, log2(18)~=4.17
	1100, // Band 20: width=22, log2(22)~=4.46
}

// SmallDiv contains precomputed values for efficient small division in Laplace decoding.
// SmallDiv[i] = (1 << 16) / (i + 1) for i in 0..128
// Used for energy decoding to avoid expensive division operations.
//
// Source: libopus celt/laplace.c (ec_laplace_decode)
var SmallDiv = [129]uint16{
	65535, 32768, 21845, 16384, 13107, 10923, 9362, 8192, 7282, 6554, // 0-9
	5958, 5461, 5041, 4681, 4369, 4096, 3855, 3641, 3449, 3277, // 10-19
	3121, 2979, 2849, 2731, 2621, 2521, 2427, 2341, 2260, 2185, // 20-29
	2114, 2048, 1986, 1928, 1872, 1820, 1771, 1725, 1680, 1638, // 30-39
	1598, 1560, 1524, 1489, 1456, 1425, 1394, 1365, 1337, 1311, // 40-49
	1285, 1260, 1237, 1214, 1192, 1170, 1150, 1130, 1111, 1092, // 50-59
	1074, 1057, 1040, 1024, 1008, 993, 978, 964, 950, 936, // 60-69
	923, 910, 898, 886, 874, 862, 851, 840, 830, 819, // 70-79
	809, 799, 790, 780, 771, 762, 753, 745, 736, 728, // 80-89
	720, 712, 705, 697, 690, 683, 676, 669, 662, 655, // 90-99
	649, 643, 636, 630, 624, 618, 612, 607, 601, 596, // 100-109
	590, 585, 580, 575, 570, 565, 560, 555, 551, 546, // 110-119
	542, 537, 533, 529, 524, 520, 516, 512, 508, // 120-128
}

// BandWidth returns the number of MDCT bins in the given band at base frame size.
// For band i, this is EBands[i+1] - EBands[i].
func BandWidth(band int) int {
	if band < 0 || band >= MaxBands {
		return 0
	}
	return EBands[band+1] - EBands[band]
}

// ScaledBandStart returns the scaled MDCT bin index for the start of a band.
// For frame sizes larger than 2.5ms, indices are scaled by (frameSize/120).
func ScaledBandStart(band, frameSize int) int {
	if band < 0 || band > MaxBands {
		return 0
	}
	scale := frameSize / Overlap // 1 for 2.5ms, 2 for 5ms, 4 for 10ms, 8 for 20ms
	return EBands[band] * scale
}

// ScaledBandEnd returns the scaled MDCT bin index for the end of a band.
func ScaledBandEnd(band, frameSize int) int {
	if band < 0 || band >= MaxBands {
		return 0
	}
	scale := frameSize / Overlap
	return EBands[band+1] * scale
}

// ScaledBandWidth returns the number of MDCT bins in a band for a given frame size.
func ScaledBandWidth(band, frameSize int) int {
	if band < 0 || band >= MaxBands {
		return 0
	}
	scale := frameSize / Overlap
	return (EBands[band+1] - EBands[band]) * scale
}
