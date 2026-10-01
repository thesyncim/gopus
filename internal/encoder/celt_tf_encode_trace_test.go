//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	celtTFTraceMaxBits  = 64
	celtTFTraceMaxBands = 21
	celtTFTraceHeader   = 112
)

type celtTFEncodeBit struct {
	Ordinal        uint32
	Symbol         int32
	LogP           uint32
	RangeBefore    uint32
	TellFracBefore uint32
	TellBefore     int32
	RangeAfter     uint32
	TellFracAfter  uint32
	TellAfter      int32
}

type celtTFEncodeTrace struct {
	Version              uint32
	Frame                uint32
	Overflow             uint32
	CoarseCalls          uint32
	BitCalls             uint32
	StoredBits           uint32
	ForeignCalls         uint32
	ICDFCalls            uint32
	SpreadCalls          uint32
	SpreadTableMatch     uint32
	SameCoder            uint32
	QuantCalls           uint32
	Start                int32
	End                  int32
	LM                   int32
	EntryTell            int32
	StorageBits          uint32
	Transient            uint32
	SelectEncoded        uint32
	SelectSymbol         int32
	PostTFRes            []int32
	SpreadSymbol         int32
	SpreadLogP           uint32
	SpreadRangeBefore    uint32
	SpreadTellFracBefore uint32
	SpreadRangeAfter     uint32
	SpreadTellFracAfter  uint32
	Bits                 []celtTFEncodeBit
}

// This copy of libopus celt.c tf_select_table keeps the trace oracle
// independent from the Go implementation table.
var celtTFTraceSelectTable = [4][8]int32{
	{0, -1, 0, -1, 0, -1, 0, -1},
	{0, -1, 0, -2, 1, 0, 1, -1},
	{0, -2, 0, -3, 2, 0, 1, -1},
	{0, -2, 0, -3, 3, 0, 1, -1},
}

func parseCELTQuantityTFTrace(data []byte, expectedFrame uint32) (celtTFEncodeTrace, int, error) {
	var trace celtTFEncodeTrace
	if len(data) < celtTFTraceHeader || string(data[:4]) != "GCTF" {
		return trace, 0, fmt.Errorf("missing or truncated GCTF header")
	}
	words := [27]uint32{}
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(data[4+i*4:])
	}
	trace.Version = words[0]
	trace.Frame = words[1]
	trace.Overflow = words[2]
	trace.CoarseCalls = words[3]
	trace.BitCalls = words[4]
	trace.StoredBits = words[5]
	trace.ForeignCalls = words[6]
	trace.ICDFCalls = words[7]
	trace.SpreadCalls = words[8]
	trace.SpreadTableMatch = words[9]
	trace.SameCoder = words[10]
	trace.QuantCalls = words[11]
	trace.Start = int32(words[12])
	trace.End = int32(words[13])
	trace.LM = int32(words[14])
	trace.EntryTell = int32(words[15])
	trace.StorageBits = words[16]
	trace.Transient = words[17]
	trace.SelectEncoded = words[18]
	trace.SelectSymbol = int32(words[19])
	postCount := words[20]
	trace.SpreadSymbol = int32(words[21])
	trace.SpreadLogP = words[22]
	trace.SpreadRangeBefore = words[23]
	trace.SpreadTellFracBefore = words[24]
	trace.SpreadRangeAfter = words[25]
	trace.SpreadTellFracAfter = words[26]
	if trace.Version != 1 {
		return trace, 0, fmt.Errorf("unsupported GCTF version %d", trace.Version)
	}
	if trace.Frame != expectedFrame {
		return trace, 0, fmt.Errorf("GCTF frame=%d, want %d", trace.Frame, expectedFrame)
	}
	if trace.Overflow != 0 || trace.CoarseCalls != 1 || trace.ForeignCalls != 0 ||
		trace.ICDFCalls != 1 || trace.SpreadCalls != 1 || trace.SpreadTableMatch != 1 ||
		trace.SameCoder != 1 || trace.QuantCalls != 1 {
		return trace, 0, fmt.Errorf("incomplete GCTF capture: overflow=%d coarse=%d foreign=%d icdf=%d spread=%d table=%d same_coder=%d quant=%d",
			trace.Overflow, trace.CoarseCalls, trace.ForeignCalls, trace.ICDFCalls, trace.SpreadCalls,
			trace.SpreadTableMatch, trace.SameCoder, trace.QuantCalls)
	}
	if trace.Start < 0 || trace.End <= trace.Start || trace.End > celtTFTraceMaxBands ||
		trace.LM < 0 || trace.LM > 3 || trace.Transient > 1 || trace.SelectEncoded > 1 ||
		postCount != uint32(trace.End-trace.Start) || trace.SelectSymbol < 0 || trace.SelectSymbol > 1 ||
		trace.EntryTell < 0 || trace.StorageBits == 0 || uint32(trace.EntryTell) > trace.StorageBits {
		return trace, 0, fmt.Errorf("invalid GCTF TF geometry/state: start=%d end=%d LM=%d transient=%d select_encoded=%d select_symbol=%d post=%d entry_tell=%d storage_bits=%d",
			trace.Start, trace.End, trace.LM, trace.Transient, trace.SelectEncoded, trace.SelectSymbol, postCount, trace.EntryTell, trace.StorageBits)
	}
	if trace.SpreadSymbol < 0 || trace.SpreadSymbol > 3 || trace.SpreadLogP != 5 ||
		trace.SpreadRangeBefore == 0 || trace.SpreadRangeAfter == 0 ||
		uint64(trace.SpreadTellFracBefore) > uint64(trace.StorageBits)*8+8 ||
		uint64(trace.SpreadTellFracAfter) > uint64(trace.StorageBits)*8+8 ||
		trace.SpreadTellFracAfter < trace.SpreadTellFracBefore {
		return trace, 0, fmt.Errorf("invalid GCTF spread close: symbol=%d logp=%d range=%08x/%08x",
			trace.SpreadSymbol, trace.SpreadLogP, trace.SpreadRangeBefore, trace.SpreadRangeAfter)
	}
	if trace.BitCalls != trace.StoredBits || trace.StoredBits > celtTFTraceMaxBits {
		return trace, 0, fmt.Errorf("GCTF TF calls=%d stored=%d, bound=%d", trace.BitCalls, trace.StoredBits, celtTFTraceMaxBits)
	}
	postBytes := int(postCount) * 4
	bitBytes := int(trace.StoredBits) * 36
	need := celtTFTraceHeader + postBytes + bitBytes
	if len(data) < need {
		return trace, 0, fmt.Errorf("truncated GCTF records: have %d bytes, need %d", len(data), need)
	}
	trace.PostTFRes = make([]int32, int(postCount))
	off := celtTFTraceHeader
	for i := range trace.PostTFRes {
		trace.PostTFRes[i] = int32(binary.LittleEndian.Uint32(data[off:]))
		off += 4
	}
	trace.Bits = make([]celtTFEncodeBit, int(trace.StoredBits))
	for i := range trace.Bits {
		bit := &trace.Bits[i]
		bit.Ordinal = binary.LittleEndian.Uint32(data[off:])
		bit.Symbol = int32(binary.LittleEndian.Uint32(data[off+4:]))
		bit.LogP = binary.LittleEndian.Uint32(data[off+8:])
		bit.RangeBefore = binary.LittleEndian.Uint32(data[off+12:])
		bit.TellFracBefore = binary.LittleEndian.Uint32(data[off+16:])
		bit.TellBefore = int32(binary.LittleEndian.Uint32(data[off+20:]))
		bit.RangeAfter = binary.LittleEndian.Uint32(data[off+24:])
		bit.TellFracAfter = binary.LittleEndian.Uint32(data[off+28:])
		bit.TellAfter = int32(binary.LittleEndian.Uint32(data[off+32:]))
		off += 36
	}
	if err := validateCELTQuantityTFTrace(trace); err != nil {
		return trace, 0, err
	}
	return trace, need, nil
}

func validateCELTQuantityTFTrace(trace celtTFEncodeTrace) error {
	bandCalls := int(trace.End - trace.Start)
	firstLogP := uint32(4)
	nextLogP := uint32(5)
	if trace.Transient != 0 {
		firstLogP = 2
		nextLogP = 4
	}
	budget := int64(trace.StorageBits)
	tell := int64(trace.EntryTell)
	selectReserved := trace.LM > 0 && tell+int64(firstLogP)+1 <= budget
	if selectReserved {
		budget--
	}
	bitIndex := 0
	curr := int32(0)
	tfChanged := int32(0)
	budgetedFlags := make([]int32, bandCalls)
	for index := 0; index < bandCalls; index++ {
		logp := nextLogP
		if index == 0 {
			logp = firstLogP
		}
		if tell+int64(logp) <= budget {
			if bitIndex >= len(trace.Bits) {
				return fmt.Errorf("GCTF omitted budget-eligible TF band %d (tell=%d logp=%d budget=%d)", trace.Start+int32(index), tell, logp, budget)
			}
			bit := trace.Bits[bitIndex]
			if bit.Ordinal != uint32(bitIndex) || bit.Symbol < 0 || bit.Symbol > 1 || bit.LogP != logp ||
				bit.TellBefore != int32(tell) || bit.TellBefore < 0 || bit.TellAfter < bit.TellBefore ||
				uint64(bit.TellFracBefore) > uint64(trace.StorageBits)*8+8 ||
				uint64(bit.TellFracAfter) > uint64(trace.StorageBits)*8+8 ||
				bit.TellFracAfter < bit.TellFracBefore || bit.RangeBefore == 0 || bit.RangeAfter == 0 {
				return fmt.Errorf("invalid GCTF TF event %d: ordinal=%d symbol=%d logp=%d tell=%d→%d want logp=%d tell-before=%d range=%08x/%08x",
					bitIndex, bit.Ordinal, bit.Symbol, bit.LogP, bit.TellBefore, bit.TellAfter,
					logp, tell, bit.RangeBefore, bit.RangeAfter)
			}
			if bitIndex == 0 {
				if bit.TellBefore != trace.EntryTell {
					return fmt.Errorf("GCTF first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
				}
			} else {
				previous := trace.Bits[bitIndex-1]
				if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
					previous.TellAfter != bit.TellBefore {
					return fmt.Errorf("GCTF event %d state does not continue event %d: range/tell-frac/tell=%08x/%d/%d→%08x/%d/%d",
						bitIndex, bitIndex-1, previous.RangeAfter, previous.TellFracAfter, previous.TellAfter,
						bit.RangeBefore, bit.TellFracBefore, bit.TellBefore)
				}
			}
			curr ^= bit.Symbol
			tfChanged |= curr
			budgetedFlags[index] = curr
			tell = int64(bit.TellAfter)
			bitIndex++
		} else {
			return fmt.Errorf("selected GCTF fixture skipped TF band %d at tell=%d logp=%d budget=%d; raw C flags are incomplete",
				trace.Start+int32(index), tell, logp, budget)
		}
	}
	if !selectReserved && trace.SelectEncoded != 0 {
		return fmt.Errorf("GCTF encoded tf_select without reservation at tell=%d LM=%d", trace.EntryTell, trace.LM)
	}
	tableBase := int(trace.Transient) * 4
	selectChangesTable := celtTFTraceSelectTable[trace.LM][tableBase+int(tfChanged)] !=
		celtTFTraceSelectTable[trace.LM][tableBase+2+int(tfChanged)]
	wantSelect := selectReserved && selectChangesTable
	if (trace.SelectEncoded != 0) != wantSelect {
		return fmt.Errorf("GCTF tf_select event=%d, want=%t from reservation=%t tf_changed=%d table row LM=%d transient=%d",
			trace.SelectEncoded, wantSelect, selectReserved, tfChanged, trace.LM, trace.Transient)
	}
	effectiveSelect := int32(0)
	if trace.SelectEncoded != 0 {
		if bitIndex >= len(trace.Bits) {
			return fmt.Errorf("GCTF omitted reserved tf_select event")
		}
		bit := trace.Bits[bitIndex]
		if bit.Ordinal != uint32(bitIndex) || bit.Symbol != trace.SelectSymbol || bit.Symbol < 0 || bit.Symbol > 1 ||
			bit.LogP != 1 || bit.TellBefore != int32(tell) || bit.TellBefore < 0 || bit.TellAfter < bit.TellBefore ||
			uint64(bit.TellFracBefore) > uint64(trace.StorageBits)*8+8 ||
			uint64(bit.TellFracAfter) > uint64(trace.StorageBits)*8+8 ||
			bit.TellFracAfter < bit.TellFracBefore || bit.RangeBefore == 0 || bit.RangeAfter == 0 {
			return fmt.Errorf("invalid GCTF tf_select event %d: symbol=%d logp=%d tell=%d→%d", bitIndex, bit.Symbol, bit.LogP, bit.TellBefore, bit.TellAfter)
		}
		if bitIndex == 0 {
			if bit.TellBefore != trace.EntryTell {
				return fmt.Errorf("GCTF first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
			}
		} else {
			previous := trace.Bits[bitIndex-1]
			if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
				previous.TellAfter != bit.TellBefore {
				return fmt.Errorf("GCTF event %d state does not continue event %d", bitIndex, bitIndex-1)
			}
		}
		effectiveSelect = bit.Symbol
		bitIndex++
	}
	if bitIndex != len(trace.Bits) {
		return fmt.Errorf("GCTF has %d unexplained TF events after budget simulation", len(trace.Bits)-bitIndex)
	}
	lastBit := trace.Bits[len(trace.Bits)-1]
	if lastBit.RangeAfter != trace.SpreadRangeBefore || lastBit.TellFracAfter != trace.SpreadTellFracBefore {
		return fmt.Errorf("GCTF coder state before spread ICDF differs: last TF range/tell-frac=%08x/%d, spread-before=%08x/%d",
			lastBit.RangeAfter, lastBit.TellFracAfter, trace.SpreadRangeBefore, trace.SpreadTellFracBefore)
	}
	for i, flag := range budgetedFlags {
		index := tableBase + 2*int(effectiveSelect) + int(flag)
		want := celtTFTraceSelectTable[trace.LM][index]
		if trace.PostTFRes[i] != want {
			return fmt.Errorf("GCTF post-TF band %d=%d, want %d from tf_select_table row LM=%d transient=%d select=%d flag=%d",
				trace.Start+int32(i), trace.PostTFRes[i], want, trace.LM, trace.Transient, effectiveSelect, flag)
		}
	}
	return nil
}

func compareCELTQuantityTFTrace(goTrace celt.TFEncodeTraceSnapshot, cTrace celtTFEncodeTrace) string {
	if goTrace.Coder == nil || goTrace.CallCount != 1 || goTrace.ForeignCalls != 0 || goTrace.Overflow || !goTrace.Complete {
		return fmt.Sprintf("invalid Go TF trace: coder=%p calls=%d foreign=%d overflow=%t complete=%t",
			goTrace.Coder, goTrace.CallCount, goTrace.ForeignCalls, goTrace.Overflow, goTrace.Complete)
	}
	if int32(len(goTrace.TFResBefore)) != cTrace.End-cTrace.Start ||
		int32(len(goTrace.TFResBudgeted)) != cTrace.End-cTrace.Start ||
		int32(len(goTrace.TFResAfter)) != cTrace.End-cTrace.Start || len(goTrace.Bits) != len(cTrace.Bits) {
		return fmt.Sprintf("TF shape differs: Go start/end=%d/%d raw/budgeted/post/events=%d/%d/%d/%d C start/end=%d/%d raw/post/events=%d/%d/%d",
			goTrace.Start, goTrace.End, len(goTrace.TFResBefore), len(goTrace.TFResBudgeted), len(goTrace.TFResAfter), len(goTrace.Bits),
			cTrace.Start, cTrace.End, cTrace.End-cTrace.Start, len(cTrace.PostTFRes), len(cTrace.Bits))
	}
	if goTrace.Start != cTrace.Start || goTrace.End != cTrace.End || goTrace.LM != cTrace.LM || boolToUint32(goTrace.Transient) != cTrace.Transient {
		return fmt.Sprintf("TF call controls differ: Go start/end/LM/transient=%d/%d/%d/%t C=%d/%d/%d/%t",
			goTrace.Start, goTrace.End, goTrace.LM, goTrace.Transient,
			cTrace.Start, cTrace.End, cTrace.LM, cTrace.Transient != 0)
	}
	if goTrace.EntryTell != cTrace.EntryTell || goTrace.StorageBits != cTrace.StorageBits {
		return fmt.Sprintf("TF budget entry differs: Go tell/storage=%d/%d C=%d/%d",
			goTrace.EntryTell, goTrace.StorageBits, cTrace.EntryTell, cTrace.StorageBits)
	}
	firstLogP := int32(4)
	if goTrace.Transient {
		firstLogP = 2
	}
	wantSelectReserved := goTrace.LM > 0 && goTrace.EntryTell+firstLogP+1 <= int32(goTrace.StorageBits)
	if goTrace.TFSelectReserved != wantSelectReserved {
		return fmt.Sprintf("Go tf_select reservation=%t disagrees with budget tell=%d first_logp=%d storage=%d",
			goTrace.TFSelectReserved, goTrace.EntryTell, firstLogP, goTrace.StorageBits)
	}
	bandCalls := int(cTrace.End - cTrace.Start)
	var cRaw int32
	for i := 0; i < bandCalls; i++ {
		cRaw ^= cTrace.Bits[i].Symbol
		if goTrace.TFResBefore[i] != cRaw {
			return fmt.Sprintf("first raw tf_res difference at band %d: Go=%d C=%d reconstructed from actual XOR calls",
				cTrace.Start+int32(i), goTrace.TFResBefore[i], cRaw)
		}
		if goTrace.TFResBudgeted[i] != cRaw {
			return fmt.Sprintf("first budgeted tf_res difference at band %d: Go=%d C=%d",
				cTrace.Start+int32(i), goTrace.TFResBudgeted[i], cRaw)
		}
	}
	for i := range cTrace.PostTFRes {
		if goTrace.TFResAfter[i] != cTrace.PostTFRes[i] {
			return fmt.Sprintf("first effective tf_res difference at band %d: Go=%d C=%d",
				cTrace.Start+int32(i), goTrace.TFResAfter[i], cTrace.PostTFRes[i])
		}
	}
	if goTrace.TFSelectEncoded != (cTrace.SelectEncoded != 0) {
		return fmt.Sprintf("tf_select coding differs: Go encoded=%t C encoded=%d", goTrace.TFSelectEncoded, cTrace.SelectEncoded)
	}
	if cTrace.SelectEncoded != 0 && goTrace.TFSelectValue != cTrace.SelectSymbol {
		return fmt.Sprintf("tf_select symbol differs: Go=%d C=%d", goTrace.TFSelectValue, cTrace.SelectSymbol)
	}
	if goTrace.TFSelectEffective != cTrace.SelectSymbol && cTrace.SelectEncoded != 0 {
		return fmt.Sprintf("effective tf_select differs: Go=%d C encoded=%d", goTrace.TFSelectEffective, cTrace.SelectSymbol)
	}
	if cTrace.SelectEncoded == 0 && goTrace.TFSelectEffective != 0 {
		return fmt.Sprintf("Go effective tf_select=%d without an encoded select bit", goTrace.TFSelectEffective)
	}
	for i, goBit := range goTrace.Bits {
		cBit := cTrace.Bits[i]
		wantSelectBit := i >= bandCalls
		if int32(i) != goBit.Ordinal || cBit.Ordinal != uint32(i) || goBit.Symbol != cBit.Symbol ||
			goBit.LogP != cBit.LogP || goBit.RangeBefore != cBit.RangeBefore ||
			uint32(goBit.TellFracBefore) != cBit.TellFracBefore || goBit.TellBefore != cBit.TellBefore ||
			goBit.RangeAfter != cBit.RangeAfter || uint32(goBit.TellFracAfter) != cBit.TellFracAfter ||
			goBit.TellAfter != cBit.TellAfter || goBit.SelectBit != wantSelectBit {
			return fmt.Sprintf("first TF entropy-call difference at event %d: Go{symbol=%d logp=%d range/tell=%08x/%d/%d→%08x/%d/%d select=%t} C{symbol=%d logp=%d range/tell=%08x/%d/%d→%08x/%d/%d}",
				i, goBit.Symbol, goBit.LogP, goBit.RangeBefore, goBit.TellFracBefore, goBit.TellBefore, goBit.RangeAfter, goBit.TellFracAfter, goBit.TellAfter, goBit.SelectBit,
				cBit.Symbol, cBit.LogP, cBit.RangeBefore, cBit.TellFracBefore, cBit.TellBefore, cBit.RangeAfter, cBit.TellFracAfter, cBit.TellAfter)
		}
	}
	lastGoBit := goTrace.Bits[len(goTrace.Bits)-1]
	if lastGoBit.RangeAfter != cTrace.SpreadRangeBefore || uint32(lastGoBit.TellFracAfter) != cTrace.SpreadTellFracBefore {
		return fmt.Sprintf("coder state before spread ICDF differs: Go range/tell-frac=%08x/%d C spread-before=%08x/%d",
			lastGoBit.RangeAfter, lastGoBit.TellFracAfter, cTrace.SpreadRangeBefore, cTrace.SpreadTellFracBefore)
	}
	if goTrace.TFSelectEffective != 0 && cTrace.SelectEncoded == 0 {
		return fmt.Sprintf("TF data matches but C select input is unobserved; Go effective select=%d", goTrace.TFSelectEffective)
	}
	return ""
}

func boolToUint32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func validCELTQuantityTFTraceWireForTesting() []byte {
	data := make([]byte, celtTFTraceHeader+2*4+2*36)
	copy(data[:4], "GCTF")
	words := [...]uint32{
		1, 25, 0, 1, 2, 2, 0, 1, 1, 1, 1, 1,
		3, 5, 0, 300, 512, 0, 0, 0, 2, 3, 5, 0x0400, 2472, 0x0300, 2512,
	}
	for i, word := range words {
		binary.LittleEndian.PutUint32(data[4+i*4:], word)
	}
	off := celtTFTraceHeader
	for _, value := range []int32{0, -1} {
		binary.LittleEndian.PutUint32(data[off:], uint32(value))
		off += 4
	}
	for i, bit := range [][8]uint32{
		{0, 4, 0x1000, 2400, 300, 0x0800, 2432, 304},
		{1, 5, 0x0800, 2432, 304, 0x0400, 2472, 309},
	} {
		values := [...]uint32{uint32(i), bit[0], bit[1], bit[2], bit[3], bit[4], bit[5], bit[6], bit[7]}
		for _, value := range values {
			binary.LittleEndian.PutUint32(data[off:], value)
			off += 4
		}
	}
	return data
}

func TestCELTQuantityTFTraceRejectsMalformed(t *testing.T) {
	valid := validCELTQuantityTFTraceWireForTesting()
	trace, consumed, err := parseCELTQuantityTFTrace(valid, 25)
	if err != nil || consumed != len(valid) || len(trace.Bits) != 2 || trace.PostTFRes[1] != -1 {
		t.Fatalf("valid GCTF rejected or decoded incorrectly: trace=%+v consumed=%d/%d err=%v", trace, consumed, len(valid), err)
	}
	mutate := func(word int, value uint32) []byte {
		data := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(data[4+word*4:], value)
		return data
	}
	badLogP := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badLogP[celtTFTraceHeader+8:], 2)
	zeroRange := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(zeroRange[celtTFTraceHeader+8+12:], 0)
	badSpreadRange := mutate(23, 0)
	badSpreadTellUnit := mutate(24, 309)
	badTellFracChain := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badTellFracChain[celtTFTraceHeader+8+36+16:], 2433)
	badTellChain := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badTellChain[celtTFTraceHeader+8+36+20:], 303)
	truncated := append([]byte(nil), valid[:len(valid)-1]...)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"truncated header", valid[:celtTFTraceHeader-1]},
		{"bad magic", append([]byte("XCTF"), valid[4:]...)},
		{"wrong version", mutate(0, 2)},
		{"wrong selected frame", mutate(1, 24)},
		{"overflow", mutate(2, 1)},
		{"missing coarse identity", mutate(10, 0)},
		{"missing spread match", mutate(9, 0)},
		{"wrong count", mutate(4, 1)},
		{"post count mismatch", mutate(20, 1)},
		{"invalid geometry", mutate(13, 2)},
		{"invalid transient", mutate(17, 2)},
		{"invalid select bit", mutate(18, 2)},
		{"zero spread range", badSpreadRange},
		{"spread fractional tell uses integer tell units", badSpreadTellUnit},
		{"zero event range", zeroRange},
		{"discontinuous fractional tell", badTellFracChain},
		{"discontinuous integer tell", badTellChain},
		{"wrong probability", badLogP},
		{"truncated events", truncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseCELTQuantityTFTrace(tc.data, 25); err == nil {
				t.Fatal("malformed GCTF payload was accepted")
			}
		})
	}
	if _, consumed, err := parseCELTQuantityTFTrace(append(valid, []byte("GQTR")...), 25); err != nil || consumed != len(valid) {
		t.Fatalf("GCTF parser did not leave the following GQTR trailer intact: consumed=%d err=%v", consumed, err)
	}
}

func TestCELTQuantityTFTraceComparisonReportsFirstActualDifference(t *testing.T) {
	cTrace, _, err := parseCELTQuantityTFTrace(validCELTQuantityTFTraceWireForTesting(), 25)
	if err != nil {
		t.Fatal(err)
	}
	goTrace := celt.TFEncodeTraceSnapshot{
		Start: 3, End: 5, LM: 0, EntryTell: 300, StorageBits: 512, Transient: false, TFSelectInput: 0,
		TFSelectReserved: false,
		TFResBefore:      []int32{0, 1}, TFResBudgeted: []int32{0, 1}, TFResAfter: []int32{0, -1},
		TFSelectEffective: 0, CallCount: 1, Complete: true,
		Bits: []celt.TFEncodeBitTrace{
			{Ordinal: 0, Symbol: 0, LogP: 4, RangeBefore: 0x1000, TellFracBefore: 2400, TellBefore: 300, RangeAfter: 0x0800, TellFracAfter: 2432, TellAfter: 304},
			{Ordinal: 1, Symbol: 1, LogP: 5, RangeBefore: 0x0800, TellFracBefore: 2432, TellBefore: 304, RangeAfter: 0x0400, TellFracAfter: 2472, TellAfter: 309},
		},
	}
	if difference := compareCELTQuantityTFTrace(goTrace, cTrace); difference == "" {
		t.Fatal("comparison accepted a missing actual coder identity")
	}
	goTrace.Coder = new(rangecoding.Encoder)
	if err := validateGoCELTQuantityTFTrace(goTrace); err != nil {
		t.Fatalf("valid Go TF trace rejected: %v", err)
	}
	badBudget := goTrace
	badBudget.TFResBudgeted = append([]int32(nil), goTrace.TFResBudgeted...)
	badBudget.TFResBudgeted[1] = 0
	if err := validateGoCELTQuantityTFTrace(badBudget); err == nil {
		t.Fatal("Go TF validator accepted an incorrect budgeted flag")
	}
	badTell := goTrace
	badTell.Bits = append([]celt.TFEncodeBitTrace(nil), goTrace.Bits...)
	badTell.Bits[1].TellBefore++
	if err := validateGoCELTQuantityTFTrace(badTell); err == nil {
		t.Fatal("Go TF validator accepted a discontinuous coder tell")
	}
	if difference := compareCELTQuantityTFTrace(goTrace, cTrace); difference != "" {
		t.Fatalf("matching actual TF call trace rejected: %s", difference)
	}
	goTrace.Bits[1].TellFracAfter++
	if difference := compareCELTQuantityTFTrace(goTrace, cTrace); !strings.Contains(difference, "event 1") {
		t.Fatalf("comparison did not report the first changed actual TF event: %q", difference)
	}
}

func validateGoCELTQuantityTFTrace(trace celt.TFEncodeTraceSnapshot) error {
	if trace.Coder == nil || trace.CallCount != 1 || trace.ForeignCalls != 0 || trace.Overflow || !trace.Complete {
		return fmt.Errorf("Go trace coder=%p calls=%d foreign=%d overflow=%t complete=%t",
			trace.Coder, trace.CallCount, trace.ForeignCalls, trace.Overflow, trace.Complete)
	}
	if trace.Start < 0 || trace.End <= trace.Start || trace.End > celtTFTraceMaxBands ||
		trace.LM < 0 || trace.LM > 3 || len(trace.TFResBefore) != int(trace.End-trace.Start) ||
		len(trace.TFResBudgeted) != len(trace.TFResBefore) || len(trace.TFResAfter) != len(trace.TFResBefore) ||
		trace.EntryTell < 0 || trace.StorageBits == 0 || uint64(trace.EntryTell) > uint64(trace.StorageBits) ||
		trace.TFSelectInput < 0 || trace.TFSelectInput > 1 || trace.TFSelectEffective < 0 || trace.TFSelectEffective > 1 {
		return fmt.Errorf("invalid Go TF geometry start=%d end=%d LM=%d raw/budgeted/post=%d/%d/%d entry_tell=%d storage_bits=%d select-input/effective=%d/%d",
			trace.Start, trace.End, trace.LM, len(trace.TFResBefore), len(trace.TFResBudgeted), len(trace.TFResAfter),
			trace.EntryTell, trace.StorageBits, trace.TFSelectInput, trace.TFSelectEffective)
	}
	maxTellFrac := int64(trace.StorageBits)*8 + 8
	if len(trace.Bits) > celtTFTraceMaxBits {
		return fmt.Errorf("Go TF trace has %d entropy calls, bound=%d", len(trace.Bits), celtTFTraceMaxBits)
	}

	firstLogP, nextLogP := int64(4), int64(5)
	if trace.Transient {
		firstLogP, nextLogP = 2, 4
	}
	budget := int64(trace.StorageBits)
	tell := int64(trace.EntryTell)
	selectReserved := trace.LM > 0 && tell+firstLogP+1 <= budget
	if selectReserved {
		budget--
	}
	if trace.TFSelectReserved != selectReserved {
		return fmt.Errorf("Go tf_select reservation=%t, want %t from tell=%d first_logp=%d storage=%d",
			trace.TFSelectReserved, selectReserved, trace.EntryTell, firstLogP, trace.StorageBits)
	}

	bitIndex := 0
	curr, tfChanged := int32(0), int32(0)
	budgeted := make([]int32, len(trace.TFResBefore))
	for band, raw := range trace.TFResBefore {
		if raw < 0 || raw > 1 {
			return fmt.Errorf("Go raw tf_res band %d=%d, want 0 or 1", trace.Start+int32(band), raw)
		}
		logp := nextLogP
		if band == 0 {
			logp = firstLogP
		}
		if tell+logp > budget {
			return fmt.Errorf("selected Go TF fixture skips band %d at tell=%d logp=%d budget=%d",
				trace.Start+int32(band), tell, logp, budget)
		}
		if bitIndex >= len(trace.Bits) {
			return fmt.Errorf("Go trace omits budget-eligible TF band %d", trace.Start+int32(band))
		}
		bit := trace.Bits[bitIndex]
		wantSymbol := raw ^ curr
		if bit.Ordinal != int32(bitIndex) || bit.SelectBit || bit.Symbol != wantSymbol || bit.LogP != uint32(logp) ||
			bit.TellBefore != int32(tell) || bit.TellBefore < 0 || bit.TellAfter < bit.TellBefore ||
			int64(bit.TellFracBefore) < 0 || int64(bit.TellFracBefore) > maxTellFrac ||
			int64(bit.TellFracAfter) > maxTellFrac ||
			bit.TellFracAfter < bit.TellFracBefore ||
			bit.RangeBefore == 0 || bit.RangeAfter == 0 {
			return fmt.Errorf("invalid Go TF band event %d: symbol=%d want=%d logp=%d want=%d tell=%d→%d want-before=%d range=%08x/%08x tell-frac=%d→%d",
				bitIndex, bit.Symbol, wantSymbol, bit.LogP, logp, bit.TellBefore, bit.TellAfter, tell,
				bit.RangeBefore, bit.RangeAfter, bit.TellFracBefore, bit.TellFracAfter)
		}
		if bitIndex == 0 {
			if bit.TellBefore != trace.EntryTell {
				return fmt.Errorf("Go first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
			}
		} else {
			previous := trace.Bits[bitIndex-1]
			if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
				previous.TellAfter != bit.TellBefore {
				return fmt.Errorf("Go TF event %d state does not continue event %d", bitIndex, bitIndex-1)
			}
		}
		curr = raw
		tfChanged |= curr
		budgeted[band] = curr
		if trace.TFResBudgeted[band] != curr {
			return fmt.Errorf("Go budgeted tf_res band %d=%d, want %d from actual XOR calls",
				trace.Start+int32(band), trace.TFResBudgeted[band], curr)
		}
		tell = int64(bit.TellAfter)
		bitIndex++
	}

	tableBase := 4 * boolToUint32(trace.Transient)
	selectTableChanges := celtTFTraceSelectTable[trace.LM][int(tableBase)+int(tfChanged)] !=
		celtTFTraceSelectTable[trace.LM][int(tableBase)+2+int(tfChanged)]
	wantSelect := selectReserved && selectTableChanges
	if trace.TFSelectEncoded != wantSelect {
		return fmt.Errorf("Go tf_select encoded=%t, want %t from reservation=%t tf_changed=%d LM=%d transient=%t",
			trace.TFSelectEncoded, wantSelect, selectReserved, tfChanged, trace.LM, trace.Transient)
	}
	effectiveSelect := int32(0)
	if trace.TFSelectEncoded {
		if bitIndex >= len(trace.Bits) {
			return fmt.Errorf("Go trace omits reserved tf_select event")
		}
		bit := trace.Bits[bitIndex]
		if bit.Ordinal != int32(bitIndex) || !bit.SelectBit || bit.Symbol != trace.TFSelectInput ||
			bit.LogP != 1 || bit.TellBefore != int32(tell) || bit.TellAfter < bit.TellBefore ||
			int64(bit.TellFracBefore) < 0 || int64(bit.TellFracBefore) > maxTellFrac ||
			int64(bit.TellFracAfter) > maxTellFrac ||
			bit.TellFracAfter < bit.TellFracBefore || bit.RangeBefore == 0 || bit.RangeAfter == 0 {
			return fmt.Errorf("invalid Go tf_select event %d: symbol=%d input=%d logp=%d tell=%d→%d",
				bitIndex, bit.Symbol, trace.TFSelectInput, bit.LogP, bit.TellBefore, bit.TellAfter)
		}
		if bitIndex == 0 {
			if bit.TellBefore != trace.EntryTell {
				return fmt.Errorf("Go first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
			}
		} else {
			previous := trace.Bits[bitIndex-1]
			if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
				previous.TellAfter != bit.TellBefore {
				return fmt.Errorf("Go tf_select event state does not continue event %d", bitIndex-1)
			}
		}
		effectiveSelect = bit.Symbol
		if trace.TFSelectValue != bit.Symbol {
			return fmt.Errorf("Go tf_select value=%d differs from actual event symbol=%d", trace.TFSelectValue, bit.Symbol)
		}
		bitIndex++
	} else if trace.TFSelectEffective != 0 {
		return fmt.Errorf("Go effective tf_select=%d without an encoded select bit", trace.TFSelectEffective)
	}
	if bitIndex != len(trace.Bits) {
		return fmt.Errorf("Go TF trace has %d unexplained entropy events", len(trace.Bits)-bitIndex)
	}
	if trace.TFSelectEffective != effectiveSelect {
		return fmt.Errorf("Go effective tf_select=%d, want %d", trace.TFSelectEffective, effectiveSelect)
	}
	for band, flag := range budgeted {
		index := int(tableBase) + 2*int(effectiveSelect) + int(flag)
		want := celtTFTraceSelectTable[trace.LM][index]
		if trace.TFResAfter[band] != want {
			return fmt.Errorf("Go post-TF band %d=%d, want %d from tf_select_table flag=%d select=%d",
				trace.Start+int32(band), trace.TFResAfter[band], want, flag, effectiveSelect)
		}
	}
	return nil
}
