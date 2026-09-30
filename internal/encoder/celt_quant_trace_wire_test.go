//go:build linux && amd64 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

const (
	celtQuantTraceWireVersion   = 1
	celtQuantTraceWireFrame     = 1
	celtQuantTraceWireBand      = 17
	celtQuantTraceWireMaxEvents = 64
	celtQuantTraceWireMaxWidth  = 256
	celtQuantTraceWireEventHead = 64
)

type celtQuantBandTrace struct {
	Frame  uint32
	Events []celt.CELTQuantBandTraceSnapshot
}

type celtQuantTraceWireReader struct {
	data []byte
	off  int
}

func (r *celtQuantTraceWireReader) u32(field string) (uint32, error) {
	if len(r.data)-r.off < 4 {
		return 0, fmt.Errorf("truncated GQTR while reading %s at byte %d", field, r.off)
	}
	value := binary.LittleEndian.Uint32(r.data[r.off : r.off+4])
	r.off += 4
	return value, nil
}

func (r *celtQuantTraceWireReader) i32(field string) (int32, error) {
	value, err := r.u32(field)
	return int32(value), err
}

func (r *celtQuantTraceWireReader) f32(field string) (float32, error) {
	bits, err := r.u32(field)
	if err != nil {
		return 0, err
	}
	value := math.Float32frombits(bits)
	if bits&0x7f800000 == 0x7f800000 {
		return 0, fmt.Errorf("non-finite GQTR value %s at byte %d: %#08x", field, r.off-4, bits)
	}
	return value, nil
}

func (r *celtQuantTraceWireReader) vector(dst *[celtQuantTraceWireMaxWidth]float32, n uint32, field string) error {
	if n == 0 || n > celtQuantTraceWireMaxWidth {
		return fmt.Errorf("invalid GQTR %s width %d", field, n)
	}
	for i := uint32(0); i < n; i++ {
		value, err := r.f32(fmt.Sprintf("%s[%d]", field, i))
		if err != nil {
			return err
		}
		dst[i] = value
	}
	return nil
}

// parseCELTQuantBandTrace parses one GQTR v1 trailer. The returned byte count
// lets a caller prove that this trailer consumes the remainder of its helper
// output after the packet, GCET, and GENT sections.
func parseCELTQuantBandTrace(data []byte) (celtQuantBandTrace, int, error) {
	return parseCELTQuantBandTraceForTarget(data, celtQuantTraceWireFrame, celtQuantTraceWireBand)
}

func parseCELTQuantBandTraceForTarget(data []byte, expectedFrame, expectedBand uint32) (celtQuantBandTrace, int, error) {
	var result celtQuantBandTrace
	if expectedBand >= celt.MaxBands {
		return result, 0, fmt.Errorf("invalid expected GQTR band %d", expectedBand)
	}
	if len(data) < 4 || string(data[:4]) != "GQTR" {
		return result, 0, fmt.Errorf("invalid GQTR magic")
	}
	reader := celtQuantTraceWireReader{data: data, off: 4}
	version, err := reader.u32("version")
	if err != nil {
		return result, 0, err
	}
	if version != celtQuantTraceWireVersion {
		return result, 0, fmt.Errorf("unsupported GQTR version %d", version)
	}
	if result.Frame, err = reader.u32("frame"); err != nil {
		return result, 0, err
	}
	count, err := reader.u32("event count")
	if err != nil {
		return result, 0, err
	}
	overflow, err := reader.u32("overflow")
	if err != nil {
		return result, 0, err
	}
	maxEvents, err := reader.u32("max events")
	if err != nil {
		return result, 0, err
	}
	maxWidth, err := reader.u32("max width")
	if err != nil {
		return result, 0, err
	}
	if result.Frame != expectedFrame {
		return result, 0, fmt.Errorf("GQTR selected frame=%d, want %d", result.Frame, expectedFrame)
	}
	if count == 0 || count > celtQuantTraceWireMaxEvents {
		return result, 0, fmt.Errorf("invalid GQTR event count %d", count)
	}
	if overflow != 0 {
		return result, 0, fmt.Errorf("C GQTR producer overflowed: %d", overflow)
	}
	if maxEvents != celtQuantTraceWireMaxEvents || maxWidth != celtQuantTraceWireMaxWidth {
		return result, 0, fmt.Errorf("unexpected GQTR bounds events=%d width=%d", maxEvents, maxWidth)
	}
	result.Events = make([]celt.CELTQuantBandTraceSnapshot, 0, count)
	for index := uint32(0); index < count; index++ {
		var event celt.CELTQuantBandTraceSnapshot
		var header [16]uint32
		for fieldIndex := range header {
			value, readErr := reader.u32(fmt.Sprintf("event %d header field %d", index, fieldIndex))
			if readErr != nil {
				return result, 0, readErr
			}
			header[fieldIndex] = value
		}
		event.Stage, event.Ordinal, event.ThetaOrdinal = header[0], header[1], header[2]
		event.Band, event.N, event.B, event.B0 = header[3], header[4], header[5], header[6]
		event.LM, event.Channels, event.Encode, event.Stereo = int32(header[7]), header[8], header[9], header[10]
		event.ThetaRound = int32(header[11])
		event.RangeBefore, event.RangeAfter = header[12], header[13]
		event.TellFracBefore, event.TellFracAfter = header[14], header[15]
		if event.N == 0 || event.N > celtQuantTraceWireMaxWidth {
			return result, 0, fmt.Errorf("event %d has invalid vector width %d", index, event.N)
		}
		switch event.Stage {
		case uint32(celt.CELTQuantBandTraceTheta):
			ints := []*int32{
				&event.BBefore, &event.BAfter, &event.FillBefore, &event.FillAfter,
				&event.QN, &event.PulseCap, &event.Offset, &event.RawIthetaQ30,
				&event.Itheta, &event.IthetaQ30, &event.Inv, &event.IMid, &event.ISide,
				&event.Delta, &event.QAlloc, &event.RemainingBefore, &event.RemainingAfter,
			}
			for fieldIndex, field := range ints {
				if *field, err = reader.i32(fmt.Sprintf("event %d theta integer %d", index, fieldIndex)); err != nil {
					return result, 0, err
				}
			}
			if event.EnergyL, err = reader.f32(fmt.Sprintf("event %d energy L", index)); err != nil {
				return result, 0, err
			}
			if event.EnergyR, err = reader.f32(fmt.Sprintf("event %d energy R", index)); err != nil {
				return result, 0, err
			}
			for _, vector := range []struct {
				dst   *[celtQuantTraceWireMaxWidth]float32
				field string
			}{
				{&event.XBefore, "theta X before"}, {&event.YBefore, "theta Y before"},
				{&event.XAfter, "theta X after"}, {&event.YAfter, "theta Y after"},
			} {
				if err = reader.vector(vector.dst, event.N, fmt.Sprintf("event %d %s", index, vector.field)); err != nil {
					return result, 0, err
				}
			}
		case uint32(celt.CELTQuantBandTracePVQ):
			if event.K, err = reader.i32(fmt.Sprintf("event %d PVQ K", index)); err != nil {
				return result, 0, err
			}
			if event.Spread, err = reader.i32(fmt.Sprintf("event %d PVQ spread", index)); err != nil {
				return result, 0, err
			}
			if event.Resynth, err = reader.i32(fmt.Sprintf("event %d PVQ resynth", index)); err != nil {
				return result, 0, err
			}
			if event.Collapse, err = reader.u32(fmt.Sprintf("event %d PVQ collapse", index)); err != nil {
				return result, 0, err
			}
			if event.Gain, err = reader.f32(fmt.Sprintf("event %d PVQ gain", index)); err != nil {
				return result, 0, err
			}
			if err = reader.vector(&event.XBefore, event.N, fmt.Sprintf("event %d PVQ X before", index)); err != nil {
				return result, 0, err
			}
			if err = reader.vector(&event.XAfter, event.N, fmt.Sprintf("event %d PVQ X after", index)); err != nil {
				return result, 0, err
			}
		case uint32(celt.CELTQuantBandTraceStereoMerge):
			if event.Mid, err = reader.f32(fmt.Sprintf("event %d merge mid", index)); err != nil {
				return result, 0, err
			}
			for _, vector := range []struct {
				dst   *[celtQuantTraceWireMaxWidth]float32
				field string
			}{
				{&event.XBefore, "merge X before"}, {&event.YBefore, "merge Y before"},
				{&event.XAfter, "merge X after"}, {&event.YAfter, "merge Y after"},
			} {
				if err = reader.vector(vector.dst, event.N, fmt.Sprintf("event %d %s", index, vector.field)); err != nil {
					return result, 0, err
				}
			}
		case uint32(celt.CELTQuantBandTraceBandOutput):
			if event.Collapse, err = reader.u32(fmt.Sprintf("event %d band collapse", index)); err != nil {
				return result, 0, err
			}
			if err = reader.vector(&event.XAfter, event.N, fmt.Sprintf("event %d band X", index)); err != nil {
				return result, 0, err
			}
			if err = reader.vector(&event.YAfter, event.N, fmt.Sprintf("event %d band Y", index)); err != nil {
				return result, 0, err
			}
		case uint32(celt.CELTQuantBandTraceRDOSelect):
			if event.Dist0, err = reader.f32(fmt.Sprintf("event %d RDO dist0", index)); err != nil {
				return result, 0, err
			}
			if event.Dist1, err = reader.f32(fmt.Sprintf("event %d RDO dist1", index)); err != nil {
				return result, 0, err
			}
			if event.SelectedRound, err = reader.i32(fmt.Sprintf("event %d RDO selected round", index)); err != nil {
				return result, 0, err
			}
			if err = reader.vector(&event.XAfter, event.N, fmt.Sprintf("event %d RDO X", index)); err != nil {
				return result, 0, err
			}
			if err = reader.vector(&event.YAfter, event.N, fmt.Sprintf("event %d RDO Y", index)); err != nil {
				return result, 0, err
			}
		default:
			return result, 0, fmt.Errorf("event %d has unknown GQTR stage %d", index, event.Stage)
		}
		result.Events = append(result.Events, event)
	}
	if err := validateCELTQuantBandTraceEventsForBand(result.Events, expectedBand); err != nil {
		return result, 0, err
	}
	return result, reader.off, nil
}

func validateCELTQuantBandTracePayload(data []byte) error {
	return validateCELTQuantBandTracePayloadForTarget(data, celtQuantTraceWireFrame, celtQuantTraceWireBand)
}

func validateCELTQuantBandTracePayloadForTarget(data []byte, expectedFrame, expectedBand uint32) error {
	_, consumed, err := parseCELTQuantBandTraceForTarget(data, expectedFrame, expectedBand)
	if err != nil {
		return err
	}
	if consumed != len(data) {
		return fmt.Errorf("GQTR has %d trailing bytes", len(data)-consumed)
	}
	return nil
}

func validateCELTQuantBandTraceEvents(events []celt.CELTQuantBandTraceSnapshot) error {
	return validateCELTQuantBandTraceEventsForBand(events, celtQuantTraceWireBand)
}

func validateCELTQuantBandTraceEventsForBand(events []celt.CELTQuantBandTraceSnapshot, expectedBand uint32) error {
	if expectedBand >= celt.MaxBands {
		return fmt.Errorf("invalid expected GQTR band %d", expectedBand)
	}
	if len(events) == 0 || len(events) > celtQuantTraceWireMaxEvents {
		return fmt.Errorf("invalid GQTR event cardinality %d", len(events))
	}
	var thetaCount, topThetaCount, pvqCount, mergeCount, bandOutputCount, rdoCount uint32
	var latestTheta uint32
	thetaByOrdinal := make(map[uint32]celt.CELTQuantBandTraceSnapshot, len(events))
	topThetaRound := make(map[uint32]int32, 2)
	mergeTheta := make(map[uint32]bool, len(events))
	bandOutputTheta := make(map[uint32]bool, len(events))
	for index := range events {
		event := &events[index]
		if event.Ordinal != uint32(index) {
			return fmt.Errorf("GQTR event %d has ordinal %d", index, event.Ordinal)
		}
		if event.Band != expectedBand || event.N == 0 || event.N > celtQuantTraceWireMaxWidth ||
			event.B == 0 || event.B0 == 0 || event.LM < -1 || event.LM > 3 ||
			event.Channels != 2 || event.Encode != 1 || event.Stereo > 1 ||
			event.ThetaRound < -1 || event.ThetaRound > 1 {
			return fmt.Errorf("invalid GQTR event %d header: stage=%d band=%d wantBand=%d n=%d B=%d B0=%d LM=%d channels=%d encode=%d stereo=%d thetaRound=%d",
				index, event.Stage, event.Band, expectedBand, event.N, event.B, event.B0, event.LM,
				event.Channels, event.Encode, event.Stereo, event.ThetaRound)
		}
		if event.RangeBefore == 0 || event.RangeAfter == 0 {
			return fmt.Errorf("GQTR event %d has zero entropy range", index)
		}
		if err := validateCELTQuantBandTraceFinite(event); err != nil {
			return fmt.Errorf("GQTR event %d: %w", index, err)
		}
		switch event.Stage {
		case uint32(celt.CELTQuantBandTraceTheta):
			thetaCount++
			if event.ThetaOrdinal != event.Ordinal {
				return fmt.Errorf("GQTR theta event %d links to theta ordinal %d", index, event.ThetaOrdinal)
			}
			if event.RangeBefore == 0 || event.RangeAfter == 0 {
				return fmt.Errorf("GQTR theta event %d has zero entropy range", index)
			}
			latestTheta = event.Ordinal
			thetaByOrdinal[event.Ordinal] = *event
			if event.Stereo == 1 {
				topThetaCount++
				topThetaRound[event.Ordinal] = event.ThetaRound
			}
		case uint32(celt.CELTQuantBandTracePVQ):
			pvqCount++
			theta, ok := thetaByOrdinal[event.ThetaOrdinal]
			if !ok {
				return fmt.Errorf("GQTR PVQ event %d references thetaOrdinal=%d, but no earlier theta exists (latestTheta=%d found=%t): leaf N=%d B=%d B0=%d LM=%d K=%d spread=%d resynth=%d",
					index, event.ThetaOrdinal, latestTheta, ok, event.N, event.B, event.B0, event.LM,
					event.K, event.Spread, event.Resynth)
			}
			var mismatches []string
			if event.B0 != event.B {
				mismatches = append(mismatches, "leaf B0 != B")
			}
			if theta.Band != event.Band {
				mismatches = append(mismatches, "band != linked theta band")
			}
			if event.Channels != theta.Channels {
				mismatches = append(mismatches, "channels != linked theta channels")
			}
			if event.Encode != theta.Encode {
				mismatches = append(mismatches, "encode != linked theta encode")
			}
			if event.Stereo != theta.Stereo {
				mismatches = append(mismatches, "stereo != linked theta stereo")
			}
			if event.ThetaRound != theta.ThetaRound {
				mismatches = append(mismatches, "thetaRound != linked theta round")
			}
			// quant_band transforms the block count before it enters
			// quant_partition, but leaves N and LM intact. Recursive
			// quant_partition splits emit their own theta context with the
			// geometry passed to both children, so the active theta's N and LM
			// match the leaf PVQ exactly.
			if event.N != theta.N {
				mismatches = append(mismatches, "leaf N != active theta N")
			}
			if event.LM != theta.LM {
				mismatches = append(mismatches, "leaf LM != active theta LM")
			}
			if event.B > event.N || event.N%event.B != 0 {
				mismatches = append(mismatches, "leaf B must divide N with B <= N")
			}
			if theta.Stereo == 0 {
				if event.B != theta.B {
					mismatches = append(mismatches, "recursive leaf B != active split theta B")
				}
			} else {
				// quant_band can recombine blocks (B >>= recombine) or
				// time-divide them (B <<= 1). Both changes are powers of
				// two; theta.B0 is the pre-split count and need not equal the
				// leaf's B0.
				var blockRatio uint32
				if event.B >= theta.B {
					if event.B%theta.B == 0 {
						blockRatio = event.B / theta.B
					}
				} else if theta.B%event.B == 0 {
					blockRatio = theta.B / event.B
				}
				if blockRatio == 0 || blockRatio&(blockRatio-1) != 0 {
					mismatches = append(mismatches, "stereo leaf B is not a power-of-two transform of theta B")
				}
			}
			if event.K <= 0 || event.Spread < 0 || event.Spread > 3 || event.Resynth < 0 || event.Resynth > 1 {
				mismatches = append(mismatches, "invalid alg_quant K/spread/resynth")
			}
			if len(mismatches) != 0 {
				return fmt.Errorf("invalid GQTR PVQ event %d thetaOrdinal=%d latestTheta=%d thetaFound=%t: leaf{band=%d N=%d B=%d B0=%d LM=%d channels=%d encode=%d stereo=%d thetaRound=%d K=%d spread=%d resynth=%d} theta{band=%d N=%d B=%d B0=%d LM=%d channels=%d encode=%d stereo=%d thetaRound=%d} mismatches=%v",
					index, event.ThetaOrdinal, latestTheta, ok,
					event.Band, event.N, event.B, event.B0, event.LM, event.Channels, event.Encode,
					event.Stereo, event.ThetaRound, event.K, event.Spread, event.Resynth,
					theta.Band, theta.N, theta.B, theta.B0, theta.LM, theta.Channels, theta.Encode,
					theta.Stereo, theta.ThetaRound, mismatches)
			}
		case uint32(celt.CELTQuantBandTraceStereoMerge):
			mergeCount++
			theta, ok := thetaByOrdinal[event.ThetaOrdinal]
			if !ok || theta.Stereo != 1 || !sameCELTQuantBandThetaContext(*event, theta) || theta.N == 2 {
				return fmt.Errorf("GQTR stereo-merge event %d has invalid saved theta context %d", index, event.ThetaOrdinal)
			}
			if mergeTheta[event.ThetaOrdinal] {
				return fmt.Errorf("duplicate GQTR stereo-merge for theta %d", event.ThetaOrdinal)
			}
			mergeTheta[event.ThetaOrdinal] = true
		case uint32(celt.CELTQuantBandTraceBandOutput):
			bandOutputCount++
			theta, ok := thetaByOrdinal[event.ThetaOrdinal]
			if !ok || theta.Stereo != 1 || !sameCELTQuantBandThetaContext(*event, theta) {
				return fmt.Errorf("GQTR band-output event %d has invalid saved theta context %d", index, event.ThetaOrdinal)
			}
			if bandOutputTheta[event.ThetaOrdinal] {
				return fmt.Errorf("duplicate GQTR band-output for theta %d", event.ThetaOrdinal)
			}
			if theta.N != 2 && !mergeTheta[event.ThetaOrdinal] {
				return fmt.Errorf("GQTR band-output for theta %d is missing its stereo merge", event.ThetaOrdinal)
			}
			bandOutputTheta[event.ThetaOrdinal] = true
		case uint32(celt.CELTQuantBandTraceRDOSelect):
			rdoCount++
			theta, ok := thetaByOrdinal[event.ThetaOrdinal]
			if !ok || !bandOutputTheta[event.ThetaOrdinal] {
				return fmt.Errorf("GQTR RDO event %d links to unselected theta %d", index, event.ThetaOrdinal)
			}
			if (event.SelectedRound != -1 && event.SelectedRound != 1) ||
				event.ThetaRound != event.SelectedRound || !sameCELTQuantBandThetaContext(*event, theta) {
				return fmt.Errorf("GQTR RDO event %d has invalid selected trial %d (theta round %d)", index, event.SelectedRound, event.ThetaRound)
			}
		default:
			return fmt.Errorf("unknown GQTR stage %d at event %d", event.Stage, index)
		}
	}
	if thetaCount == 0 || pvqCount == 0 {
		return fmt.Errorf("incomplete GQTR event cardinality: theta=%d pvq=%d", thetaCount, pvqCount)
	}
	if topThetaCount != 0 && topThetaCount != 1 && topThetaCount != 2 {
		return fmt.Errorf("invalid GQTR stereo-trial cardinality %d", topThetaCount)
	}
	if rdoCount > 1 {
		return fmt.Errorf("duplicate GQTR RDO selection events: %d", rdoCount)
	}
	if bandOutputCount != topThetaCount || len(bandOutputTheta) != int(topThetaCount) {
		return fmt.Errorf("GQTR top-level output cardinality=%d for %d stereo trials", bandOutputCount, topThetaCount)
	}
	for thetaOrdinal, theta := range thetaByOrdinal {
		if theta.Stereo == 1 && theta.N != 2 && !mergeTheta[thetaOrdinal] {
			return fmt.Errorf("GQTR stereo theta %d is missing its merge", thetaOrdinal)
		}
	}
	if mergeCount != uint32(len(mergeTheta)) {
		return fmt.Errorf("GQTR stereo merge cardinality=%d unique=%d", mergeCount, len(mergeTheta))
	}
	if topThetaCount == 0 {
		// The selected low-band trace has the source-shaped graph emitted by
		// quant_all_bands' per-channel path: two mono partition contexts and
		// four linked PVQ calls. This shape supports inferring dual-stereo for
		// this diagnostic; the branch control itself is not serialized.
		if rdoCount != 0 || mergeCount != 0 || bandOutputCount != 0 {
			return fmt.Errorf("invalid GQTR non-stereo trace: RDO=%d merges=%d bandOutputs=%d",
				rdoCount, mergeCount, bandOutputCount)
		}
		if thetaCount != 2 || pvqCount != 4 || len(events) != 6 {
			return fmt.Errorf("invalid GQTR non-stereo graph: theta=%d pvq=%d events=%d, want 2/4/6",
				thetaCount, pvqCount, len(events))
		}
		for ordinal, theta := range thetaByOrdinal {
			if theta.Stereo != 0 {
				return fmt.Errorf("GQTR non-stereo trace has stereo theta %d", ordinal)
			}
			if theta.N != 4 || theta.LM != 2 || theta.ThetaRound != 0 {
				return fmt.Errorf("GQTR non-stereo theta %d has N=%d LM=%d thetaRound=%d, want 4/2/0",
					ordinal, theta.N, theta.LM, theta.ThetaRound)
			}
		}
		for index := range events {
			event := &events[index]
			if event.Stage == uint32(celt.CELTQuantBandTracePVQ) &&
				(event.Stereo != 0 || event.N != 4 || event.LM != 2 || event.ThetaRound != 0) {
				return fmt.Errorf("GQTR non-stereo PVQ %d has N=%d LM=%d stereo=%d thetaRound=%d, want 4/2/0/0",
					event.Ordinal, event.N, event.LM, event.Stereo, event.ThetaRound)
			}
		}
		return nil
	}
	if rdoCount == 1 {
		if topThetaCount != 2 || bandOutputCount != 2 || events[len(events)-1].Stage != uint32(celt.CELTQuantBandTraceRDOSelect) {
			return fmt.Errorf("incomplete GQTR RDO trials: theta=%d outputs=%d", topThetaCount, bandOutputCount)
		}
		foundMinus, foundPlus := false, false
		for _, round := range topThetaRound {
			foundMinus = foundMinus || round == -1
			foundPlus = foundPlus || round == 1
		}
		if !foundMinus || !foundPlus {
			return fmt.Errorf("GQTR RDO trials have rounds %v, want -1 and +1", topThetaRound)
		}
	} else if topThetaCount != 1 {
		return fmt.Errorf("GQTR has %d stereo trials but no RDO selection", topThetaCount)
	} else {
		for _, round := range topThetaRound {
			if round != 0 {
				return fmt.Errorf("GQTR non-RDO stereo trial has theta round %d, want 0", round)
			}
		}
	}
	return nil
}

func sameCELTQuantBandThetaContext(event, theta celt.CELTQuantBandTraceSnapshot) bool {
	return event.Band == theta.Band && event.N == theta.N && event.B == theta.B && event.B0 == theta.B0 &&
		event.LM == theta.LM && event.Channels == theta.Channels && event.Encode == theta.Encode &&
		event.Stereo == theta.Stereo && event.ThetaRound == theta.ThetaRound
}

func validateCELTQuantBandTraceFinite(event *celt.CELTQuantBandTraceSnapshot) error {
	finite := func(value float32) bool { return math.Float32bits(value)&0x7f800000 != 0x7f800000 }
	check := func(field string, value float32) error {
		if !finite(value) {
			return fmt.Errorf("non-finite %s: %#08x", field, math.Float32bits(value))
		}
		return nil
	}
	checkVector := func(field string, values *[celtQuantTraceWireMaxWidth]float32) error {
		for i := uint32(0); i < event.N; i++ {
			if err := check(fmt.Sprintf("%s[%d]", field, i), values[i]); err != nil {
				return err
			}
		}
		return nil
	}
	switch event.Stage {
	case uint32(celt.CELTQuantBandTraceTheta):
		for _, item := range []struct {
			name  string
			value float32
		}{{"energyL", event.EnergyL}, {"energyR", event.EnergyR}} {
			if err := check(item.name, item.value); err != nil {
				return err
			}
		}
		for _, vector := range []struct {
			name   string
			values *[celtQuantTraceWireMaxWidth]float32
		}{{"XBefore", &event.XBefore}, {"YBefore", &event.YBefore}, {"XAfter", &event.XAfter}, {"YAfter", &event.YAfter}} {
			if err := checkVector(vector.name, vector.values); err != nil {
				return err
			}
		}
	case uint32(celt.CELTQuantBandTracePVQ):
		if err := check("gain", event.Gain); err != nil {
			return err
		}
		if err := checkVector("XBefore", &event.XBefore); err != nil {
			return err
		}
		return checkVector("XAfter", &event.XAfter)
	case uint32(celt.CELTQuantBandTraceStereoMerge):
		if err := check("mid", event.Mid); err != nil {
			return err
		}
		for _, vector := range []struct {
			name   string
			values *[celtQuantTraceWireMaxWidth]float32
		}{{"XBefore", &event.XBefore}, {"YBefore", &event.YBefore}, {"XAfter", &event.XAfter}, {"YAfter", &event.YAfter}} {
			if err := checkVector(vector.name, vector.values); err != nil {
				return err
			}
		}
	case uint32(celt.CELTQuantBandTraceBandOutput):
		if err := checkVector("XAfter", &event.XAfter); err != nil {
			return err
		}
		return checkVector("YAfter", &event.YAfter)
	case uint32(celt.CELTQuantBandTraceRDOSelect):
		if err := check("dist0", event.Dist0); err != nil {
			return err
		}
		if err := check("dist1", event.Dist1); err != nil {
			return err
		}
		if err := checkVector("XAfter", &event.XAfter); err != nil {
			return err
		}
		return checkVector("YAfter", &event.YAfter)
	}
	return nil
}

// compareCELTQuantBandTrace reports the first fine-boundary C/Go difference;
// an empty result means every captured field and float32 bit matches.
func compareCELTQuantBandTrace(goEvents []celt.CELTQuantBandTraceSnapshot, cTrace celtQuantBandTrace) string {
	return compareCELTQuantBandTraceForTarget(goEvents, cTrace, celtQuantTraceWireFrame, celtQuantTraceWireBand)
}

func compareCELTQuantBandTraceForTarget(goEvents []celt.CELTQuantBandTraceSnapshot, cTrace celtQuantBandTrace, expectedFrame, expectedBand uint32) string {
	if expectedBand >= celt.MaxBands {
		return fmt.Sprintf("invalid expected GQTR band %d", expectedBand)
	}
	if cTrace.Frame != expectedFrame {
		return fmt.Sprintf("GQTR selected frame=%d, want %d", cTrace.Frame, expectedFrame)
	}
	if err := validateCELTQuantBandTraceEventsForBand(goEvents, expectedBand); err != nil {
		return "invalid Go GQTR trace: " + err.Error()
	}
	if err := validateCELTQuantBandTraceEventsForBand(cTrace.Events, expectedBand); err != nil {
		return "invalid C GQTR trace: " + err.Error()
	}
	if len(goEvents) != len(cTrace.Events) {
		return fmt.Sprintf("event count Go=%d C=%d", len(goEvents), len(cTrace.Events))
	}
	for index := range goEvents {
		if difference := compareCELTQuantBandTraceEvent(goEvents[index], cTrace.Events[index]); difference != "" {
			return fmt.Sprintf("event %d (%s): %s", index, celtQuantTraceStageName(goEvents[index].Stage), difference)
		}
	}
	return ""
}

func compareCELTQuantBandTraceEvent(goEvent, cEvent celt.CELTQuantBandTraceSnapshot) string {
	uints := []struct {
		name string
		goV  uint32
		cV   uint32
	}{
		{"stage", goEvent.Stage, cEvent.Stage}, {"ordinal", goEvent.Ordinal, cEvent.Ordinal},
		{"thetaOrdinal", goEvent.ThetaOrdinal, cEvent.ThetaOrdinal}, {"band", goEvent.Band, cEvent.Band},
		{"n", goEvent.N, cEvent.N}, {"B", goEvent.B, cEvent.B}, {"B0", goEvent.B0, cEvent.B0},
		{"channels", goEvent.Channels, cEvent.Channels}, {"encode", goEvent.Encode, cEvent.Encode},
		{"stereo", goEvent.Stereo, cEvent.Stereo}, {"rangeBefore", goEvent.RangeBefore, cEvent.RangeBefore},
		{"rangeAfter", goEvent.RangeAfter, cEvent.RangeAfter}, {"tellFracBefore", goEvent.TellFracBefore, cEvent.TellFracBefore},
		{"tellFracAfter", goEvent.TellFracAfter, cEvent.TellFracAfter},
	}
	for _, field := range uints {
		if field.goV != field.cV {
			return fmt.Sprintf("%s Go=%#x C=%#x", field.name, field.goV, field.cV)
		}
	}
	if goEvent.LM != cEvent.LM {
		return fmt.Sprintf("LM Go=%d C=%d", goEvent.LM, cEvent.LM)
	}
	if goEvent.ThetaRound != cEvent.ThetaRound {
		return fmt.Sprintf("thetaRound Go=%d C=%d", goEvent.ThetaRound, cEvent.ThetaRound)
	}
	floatDiff := func(name string, goValue, cValue float32) string {
		if math.Float32bits(goValue) != math.Float32bits(cValue) {
			return fmt.Sprintf("%s Go=%#08x C=%#08x", name, math.Float32bits(goValue), math.Float32bits(cValue))
		}
		return ""
	}
	vectorDiff := func(name string, goValues, cValues *[celtQuantTraceWireMaxWidth]float32, n uint32) string {
		for i := uint32(0); i < n; i++ {
			if difference := floatDiff(fmt.Sprintf("%s[%d]", name, i), goValues[i], cValues[i]); difference != "" {
				return difference
			}
		}
		return ""
	}
	intDiff := func(name string, goValue, cValue int32) string {
		if goValue != cValue {
			return fmt.Sprintf("%s Go=%d C=%d", name, goValue, cValue)
		}
		return ""
	}
	uintDiff := func(name string, goValue, cValue uint32) string {
		if goValue != cValue {
			return fmt.Sprintf("%s Go=%#x C=%#x", name, goValue, cValue)
		}
		return ""
	}
	checkInts := func(fields ...struct {
		name string
		goV  int32
		cV   int32
	}) string {
		for _, field := range fields {
			if difference := intDiff(field.name, field.goV, field.cV); difference != "" {
				return difference
			}
		}
		return ""
	}
	switch goEvent.Stage {
	case uint32(celt.CELTQuantBandTraceTheta):
		if difference := checkInts(
			struct {
				name    string
				goV, cV int32
			}{"bBefore", goEvent.BBefore, cEvent.BBefore},
			struct {
				name    string
				goV, cV int32
			}{"bAfter", goEvent.BAfter, cEvent.BAfter},
			struct {
				name    string
				goV, cV int32
			}{"fillBefore", goEvent.FillBefore, cEvent.FillBefore},
			struct {
				name    string
				goV, cV int32
			}{"fillAfter", goEvent.FillAfter, cEvent.FillAfter},
			struct {
				name    string
				goV, cV int32
			}{"qn", goEvent.QN, cEvent.QN},
			struct {
				name    string
				goV, cV int32
			}{"pulseCap", goEvent.PulseCap, cEvent.PulseCap},
			struct {
				name    string
				goV, cV int32
			}{"offset", goEvent.Offset, cEvent.Offset},
			struct {
				name    string
				goV, cV int32
			}{"rawIthetaQ30", goEvent.RawIthetaQ30, cEvent.RawIthetaQ30},
			struct {
				name    string
				goV, cV int32
			}{"itheta", goEvent.Itheta, cEvent.Itheta},
			struct {
				name    string
				goV, cV int32
			}{"inv", goEvent.Inv, cEvent.Inv},
			struct {
				name    string
				goV, cV int32
			}{"imid", goEvent.IMid, cEvent.IMid},
			struct {
				name    string
				goV, cV int32
			}{"iside", goEvent.ISide, cEvent.ISide},
			struct {
				name    string
				goV, cV int32
			}{"delta", goEvent.Delta, cEvent.Delta},
			struct {
				name    string
				goV, cV int32
			}{"qalloc", goEvent.QAlloc, cEvent.QAlloc},
			struct {
				name    string
				goV, cV int32
			}{"remainingBefore", goEvent.RemainingBefore, cEvent.RemainingBefore},
			struct {
				name    string
				goV, cV int32
			}{"remainingAfter", goEvent.RemainingAfter, cEvent.RemainingAfter},
		); difference != "" {
			return difference
		}
		// This file excludes gopus_qext. In pinned celt/bands.c compute_theta,
		// the quantized itheta_q30 projection and split_ctx storage are guarded
		// by ENABLE_QEXT. C therefore records its unprojected local value here,
		// while Go retains its local projection in the snapshot. Keep both
		// recorded Q30 values intact, but compare RawIthetaQ30 and the effective
		// Q14 Itheta above as the source-valid values for this non-QEXT build.
		for _, field := range []struct {
			name    string
			goV, cV float32
		}{{"energyL", goEvent.EnergyL, cEvent.EnergyL}, {"energyR", goEvent.EnergyR, cEvent.EnergyR}} {
			if difference := floatDiff(field.name, field.goV, field.cV); difference != "" {
				return difference
			}
		}
		for _, vector := range []struct {
			name    string
			goV, cV *[celtQuantTraceWireMaxWidth]float32
		}{
			{"XBefore", &goEvent.XBefore, &cEvent.XBefore}, {"YBefore", &goEvent.YBefore, &cEvent.YBefore},
			{"XAfter", &goEvent.XAfter, &cEvent.XAfter}, {"YAfter", &goEvent.YAfter, &cEvent.YAfter},
		} {
			if difference := vectorDiff(vector.name, vector.goV, vector.cV, goEvent.N); difference != "" {
				return difference
			}
		}
	case uint32(celt.CELTQuantBandTracePVQ):
		if difference := checkInts(
			struct {
				name    string
				goV, cV int32
			}{"k", goEvent.K, cEvent.K},
			struct {
				name    string
				goV, cV int32
			}{"spread", goEvent.Spread, cEvent.Spread},
			struct {
				name    string
				goV, cV int32
			}{"resynth", goEvent.Resynth, cEvent.Resynth},
		); difference != "" {
			return difference
		}
		if difference := uintDiff("collapse", goEvent.Collapse, cEvent.Collapse); difference != "" {
			return difference
		}
		if difference := floatDiff("gain", goEvent.Gain, cEvent.Gain); difference != "" {
			return difference
		}
		for _, vector := range []struct {
			name    string
			goV, cV *[celtQuantTraceWireMaxWidth]float32
		}{{"XBefore", &goEvent.XBefore, &cEvent.XBefore}, {"XAfter", &goEvent.XAfter, &cEvent.XAfter}} {
			if difference := vectorDiff(vector.name, vector.goV, vector.cV, goEvent.N); difference != "" {
				return difference
			}
		}
	case uint32(celt.CELTQuantBandTraceStereoMerge):
		if difference := floatDiff("mid", goEvent.Mid, cEvent.Mid); difference != "" {
			return difference
		}
		for _, vector := range []struct {
			name    string
			goV, cV *[celtQuantTraceWireMaxWidth]float32
		}{
			{"XBefore", &goEvent.XBefore, &cEvent.XBefore}, {"YBefore", &goEvent.YBefore, &cEvent.YBefore},
			{"XAfter", &goEvent.XAfter, &cEvent.XAfter}, {"YAfter", &goEvent.YAfter, &cEvent.YAfter},
		} {
			if difference := vectorDiff(vector.name, vector.goV, vector.cV, goEvent.N); difference != "" {
				return difference
			}
		}
	case uint32(celt.CELTQuantBandTraceBandOutput):
		if difference := uintDiff("collapse", goEvent.Collapse, cEvent.Collapse); difference != "" {
			return difference
		}
		for _, vector := range []struct {
			name    string
			goV, cV *[celtQuantTraceWireMaxWidth]float32
		}{{"X", &goEvent.XAfter, &cEvent.XAfter}, {"Y", &goEvent.YAfter, &cEvent.YAfter}} {
			if difference := vectorDiff(vector.name, vector.goV, vector.cV, goEvent.N); difference != "" {
				return difference
			}
		}
	case uint32(celt.CELTQuantBandTraceRDOSelect):
		if difference := floatDiff("dist0", goEvent.Dist0, cEvent.Dist0); difference != "" {
			return difference
		}
		if difference := floatDiff("dist1", goEvent.Dist1, cEvent.Dist1); difference != "" {
			return difference
		}
		if difference := intDiff("selectedRound", goEvent.SelectedRound, cEvent.SelectedRound); difference != "" {
			return difference
		}
		for _, vector := range []struct {
			name    string
			goV, cV *[celtQuantTraceWireMaxWidth]float32
		}{{"X", &goEvent.XAfter, &cEvent.XAfter}, {"Y", &goEvent.YAfter, &cEvent.YAfter}} {
			if difference := vectorDiff(vector.name, vector.goV, vector.cV, goEvent.N); difference != "" {
				return difference
			}
		}
	default:
		return fmt.Sprintf("unknown stage %d", goEvent.Stage)
	}
	return ""
}

func TestCELTQuantBandTraceNonQEXTQ30StorageDifference(t *testing.T) {
	trace, _, err := parseCELTQuantBandTrace(validCELTQuantBandTraceWireForTesting())
	if err != nil {
		t.Fatalf("parse valid trace fixture: %v", err)
	}
	goEvents := append([]celt.CELTQuantBandTraceSnapshot(nil), trace.Events...)
	cEvents := append([]celt.CELTQuantBandTraceSnapshot(nil), trace.Events...)
	thetaIndex := -1
	for index := range goEvents {
		if goEvents[index].Stage == uint32(celt.CELTQuantBandTraceTheta) {
			thetaIndex = index
			break
		}
	}
	if thetaIndex < 0 {
		t.Fatal("trace fixture has no theta event")
	}

	const rawQ30 = int32(0x23456789)
	const effectiveQ14 = int32(0x2345)
	goEvents[thetaIndex].RawIthetaQ30 = rawQ30
	cEvents[thetaIndex].RawIthetaQ30 = rawQ30
	goEvents[thetaIndex].Itheta = effectiveQ14
	cEvents[thetaIndex].Itheta = effectiveQ14
	goEvents[thetaIndex].IthetaQ30 = effectiveQ14 << 16
	cEvents[thetaIndex].IthetaQ30 = rawQ30
	cTrace := celtQuantBandTrace{Frame: celtQuantTraceWireFrame, Events: cEvents}

	if difference := compareCELTQuantBandTrace(goEvents, cTrace); difference != "" {
		t.Fatalf("non-QEXT diagnostic Q30 storage difference rejected: %s", difference)
	}
	if got := goEvents[thetaIndex].IthetaQ30; got != effectiveQ14<<16 {
		t.Fatalf("Go diagnostic Q30 value was modified: got %#x", got)
	}
	if got := cEvents[thetaIndex].IthetaQ30; got != rawQ30 {
		t.Fatalf("C diagnostic Q30 value was modified: got %#x", got)
	}

	cEvents[thetaIndex].RawIthetaQ30++
	if difference := compareCELTQuantBandTrace(goEvents, cTrace); !strings.Contains(difference, "rawIthetaQ30") {
		t.Fatalf("raw angle mismatch was not compared exactly: %q", difference)
	}
	cEvents[thetaIndex].RawIthetaQ30 = rawQ30
	cEvents[thetaIndex].Itheta++
	if difference := compareCELTQuantBandTrace(goEvents, cTrace); !strings.Contains(difference, "itheta") {
		t.Fatalf("effective Q14 theta mismatch was not compared exactly: %q", difference)
	}
}

func celtQuantTraceStageName(stage uint32) string {
	switch stage {
	case uint32(celt.CELTQuantBandTraceTheta):
		return "theta"
	case uint32(celt.CELTQuantBandTracePVQ):
		return "pvq"
	case uint32(celt.CELTQuantBandTraceStereoMerge):
		return "stereo_merge"
	case uint32(celt.CELTQuantBandTraceBandOutput):
		return "band_output"
	case uint32(celt.CELTQuantBandTraceRDOSelect):
		return "rdo_select"
	default:
		return fmt.Sprintf("stage_%d", stage)
	}
}

func validCELTQuantBandTraceWireForTesting() []byte {
	return validCELTQuantBandTraceWireForTargetTesting(celtQuantTraceWireFrame, celtQuantTraceWireBand)
}

func validCELTQuantBandTraceWireForTargetTesting(frame, band uint32) []byte {
	data := make([]byte, 0, 1024)
	data = append(data, "GQTR"...)
	for _, value := range []uint32{celtQuantTraceWireVersion, frame, 9, 0, 64, 256} {
		data = appendCELTQuantTraceWord(data, value)
	}
	appendEvent := func(stage, ordinal, thetaOrdinal uint32, thetaRound int32, payload func([]byte) []byte) {
		header := []uint32{stage, ordinal, thetaOrdinal, band, 4, 2, 2, 0, 2, 1, 1, uint32(thetaRound),
			0x80000000 + ordinal, 0x70000000 + ordinal, 100 + ordinal, 200 + ordinal}
		for _, value := range header {
			data = appendCELTQuantTraceWord(data, value)
		}
		data = payload(data)
	}
	appendTheta := func(ordinal uint32, round int32) {
		appendEvent(1, ordinal, ordinal, round, func(dst []byte) []byte {
			for range 17 {
				dst = appendCELTQuantTraceWord(dst, 0)
			}
			for _, energy := range []float32{1, 2} {
				dst = appendCELTQuantTraceFloat(dst, energy)
			}
			for i := range 16 {
				dst = appendCELTQuantTraceFloat(dst, float32(i+1)/16)
			}
			return dst
		})
	}
	appendTheta(0, -1)
	appendEvent(2, 1, 0, -1, func(dst []byte) []byte {
		for _, value := range []uint32{1, 2, 1, 1} {
			dst = appendCELTQuantTraceWord(dst, value)
		}
		dst = appendCELTQuantTraceFloat(dst, 0.75)
		for i := range 8 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/8)
		}
		return dst
	})
	appendEvent(3, 2, 0, -1, func(dst []byte) []byte {
		for i := range 17 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/16)
		}
		return dst
	})
	appendEvent(4, 3, 0, -1, func(dst []byte) []byte {
		dst = appendCELTQuantTraceWord(dst, 1)
		for i := range 8 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/8)
		}
		return dst
	})
	appendTheta(4, 1)
	appendEvent(2, 5, 4, 1, func(dst []byte) []byte {
		for _, value := range []uint32{1, 2, 1, 1} {
			dst = appendCELTQuantTraceWord(dst, value)
		}
		dst = appendCELTQuantTraceFloat(dst, 0.75)
		for i := range 8 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/8)
		}
		return dst
	})
	appendEvent(3, 6, 4, 1, func(dst []byte) []byte {
		for i := range 17 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/16)
		}
		return dst
	})
	appendEvent(4, 7, 4, 1, func(dst []byte) []byte {
		dst = appendCELTQuantTraceWord(dst, 1)
		for i := range 8 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/8)
		}
		return dst
	})
	appendEvent(5, 8, 0, -1, func(dst []byte) []byte {
		for _, value := range []float32{1, 2} {
			dst = appendCELTQuantTraceFloat(dst, value)
		}
		dst = appendCELTQuantTraceWord(dst, ^uint32(0))
		for i := range 8 {
			dst = appendCELTQuantTraceFloat(dst, float32(i+1)/8)
		}
		return dst
	})
	return data
}

func appendCELTQuantTraceWord(data []byte, value uint32) []byte {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return append(data, encoded[:]...)
}

func appendCELTQuantTraceFloat(data []byte, value float32) []byte {
	return appendCELTQuantTraceWord(data, math.Float32bits(value))
}

func celtQuantTraceWireEventOffset(data []byte, wanted int) int {
	offset := 28
	for index := 0; index < wanted; index++ {
		if len(data)-offset < celtQuantTraceWireEventHead {
			return -1
		}
		stage := binary.LittleEndian.Uint32(data[offset : offset+4])
		n := binary.LittleEndian.Uint32(data[offset+16 : offset+20])
		payloadWords := uint64(0)
		switch stage {
		case 1:
			payloadWords = 19 + uint64(4*n)
		case 2:
			payloadWords = 5 + uint64(2*n)
		case 3:
			payloadWords = 1 + uint64(4*n)
		case 4:
			payloadWords = 1 + uint64(2*n)
		case 5:
			payloadWords = 3 + uint64(2*n)
		default:
			return -1
		}
		offset += celtQuantTraceWireEventHead + int(payloadWords*4)
	}
	return offset
}

func TestCELTQuantBandTraceWireValidation(t *testing.T) {
	valid := validCELTQuantBandTraceWireForTesting()
	if err := validateCELTQuantBandTracePayload(valid); err != nil {
		t.Fatalf("valid GQTR payload rejected: %v", err)
	}
	trace, consumed, err := parseCELTQuantBandTrace(valid)
	if err != nil || consumed != len(valid) || len(trace.Events) != 9 {
		t.Fatalf("parse valid GQTR: events=%d consumed=%d/%d err=%v", len(trace.Events), consumed, len(valid), err)
	}
	firstEvent := celtQuantTraceWireEventOffset(valid, 0)
	secondEvent := celtQuantTraceWireEventOffset(valid, 1)
	secondTrialPVQ := celtQuantTraceWireEventOffset(valid, 5)
	secondTrialMerge := celtQuantTraceWireEventOffset(valid, 6)
	secondTrialTheta := celtQuantTraceWireEventOffset(valid, 4)
	secondTrialOutput := celtQuantTraceWireEventOffset(valid, 7)
	rdoEvent := celtQuantTraceWireEventOffset(valid, 8)
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"truncated", func(data []byte) []byte { return data[:len(data)-1] }},
		{"trailing byte", func(data []byte) []byte { return append(data, 0) }},
		{"version", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[4:8], 2); return data }},
		{"event count bound", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[12:16], 65); return data }},
		{"producer overflow", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[16:20], 1); return data }},
		{"width bound header", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[24:28], 257); return data }},
		{"unknown stage", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[firstEvent:firstEvent+4], 99)
			return data
		}},
		{"ordinal", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[firstEvent+4:firstEvent+8], 1)
			return data
		}},
		{"theta linkage", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[secondEvent+8:secondEvent+12], 1)
			return data
		}},
		{"vector width", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[firstEvent+16:firstEvent+20], 257)
			return data
		}},
		{"encode control", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[firstEvent+36:firstEvent+40], 2)
			return data
		}},
		{"non-finite scalar", func(data []byte) []byte {
			energyL := firstEvent + celtQuantTraceWireEventHead + 17*4
			binary.LittleEndian.PutUint32(data[energyL:energyL+4], 0x7fc00000)
			return data
		}},
		{"missing PVQ stage", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[secondEvent:secondEvent+4], 3)
			return data
		}},
		{"duplicate trial output", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[secondTrialOutput+8:secondTrialOutput+12], 0)
			return data
		}},
		{"wrong selected trial", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[rdoEvent+8:rdoEvent+12], 4)
			return data
		}},
		{"missing RDO selection", func(data []byte) []byte {
			data = data[:rdoEvent]
			binary.LittleEndian.PutUint32(data[12:16], 8)
			return data
		}},
		{"duplicate RDO trial round", func(data []byte) []byte {
			for _, eventOffset := range []int{secondTrialTheta, secondTrialPVQ, secondTrialMerge, secondTrialOutput} {
				binary.LittleEndian.PutUint32(data[eventOffset+44:eventOffset+48], ^uint32(0))
			}
			return data
		}},
		{"duplicate RDO record", func(data []byte) []byte {
			duplicate := append([]byte(nil), data...)
			duplicate = append(duplicate, data[rdoEvent:]...)
			binary.LittleEndian.PutUint32(duplicate[12:16], 10)
			duplicateOrdinal := len(data) + 4
			binary.LittleEndian.PutUint32(duplicate[duplicateOrdinal:duplicateOrdinal+4], 9)
			return duplicate
		}},
		{"invalid signed LM", func(data []byte) []byte {
			binary.LittleEndian.PutUint32(data[firstEvent+28:firstEvent+32], ^uint32(1))
			return data
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if err := validateCELTQuantBandTracePayload(test.mutate(data)); err == nil {
				t.Fatal("malformed GQTR payload was accepted")
			}
		})
	}
}

func TestCELTQuantBandTraceForTargetKeepsFrameAndBandStrict(t *testing.T) {
	const frame, band = uint32(25), uint32(7)
	data := validCELTQuantBandTraceWireForTargetTesting(frame, band)
	trace, consumed, err := parseCELTQuantBandTraceForTarget(data, frame, band)
	if err != nil || trace.Frame != frame || consumed != len(data) {
		t.Fatalf("parse selected frame/band: frame=%d consumed=%d/%d err=%v", trace.Frame, consumed, len(data), err)
	}
	if err := validateCELTQuantBandTracePayloadForTarget(data, frame, band); err != nil {
		t.Fatalf("validate selected frame/band payload: %v", err)
	}
	if difference := compareCELTQuantBandTraceForTarget(trace.Events, trace, frame, band); difference != "" {
		t.Fatalf("matching selected trace differs: %s", difference)
	}
	if _, _, err := parseCELTQuantBandTraceForTarget(data, frame+1, band); err == nil || !strings.Contains(err.Error(), "selected frame") {
		t.Fatalf("wrong expected frame accepted: %v", err)
	}
	if _, _, err := parseCELTQuantBandTraceForTarget(data, frame, band+1); err == nil || !strings.Contains(err.Error(), "wantBand") {
		t.Fatalf("wrong expected band accepted: %v", err)
	}
	if _, _, err := parseCELTQuantBandTrace(data); err == nil || !strings.Contains(err.Error(), "selected frame") {
		t.Fatalf("default frame-1 wrapper accepted frame %d: %v", frame, err)
	}
	wrongFrame := trace
	wrongFrame.Frame++
	if difference := compareCELTQuantBandTraceForTarget(trace.Events, wrongFrame, frame, band); !strings.Contains(difference, "selected frame") {
		t.Fatalf("compare accepted a mismatched frame: %q", difference)
	}
	wrongBand := append([]celt.CELTQuantBandTraceSnapshot(nil), trace.Events...)
	wrongBand[0].Band++
	if err := validateCELTQuantBandTraceEventsForBand(wrongBand, band); err == nil || !strings.Contains(err.Error(), "wantBand") {
		t.Fatalf("event validator accepted a mismatched band: %v", err)
	}
}

func TestCELTQuantCoefficient98MapsToFiveMillisecondBand18(t *testing.T) {
	const frameSize, coefficient = 240, 98
	mode := celt.GetModeConfig(frameSize)
	if mode.LM != 1 {
		t.Fatalf("frame size %d has LM=%d, want LM=1", frameSize, mode.LM)
	}
	blocks := 1 << mode.LM
	start := blocks * celt.EBands[18]
	end := blocks * celt.EBands[19]
	if start != 96 || end != 120 || coefficient < start || coefficient >= end {
		t.Fatalf("coefficient %d maps to [%d,%d), want band 18 [96,120) from the eBand5ms table", coefficient, start, end)
	}
}

func TestCELTQuantBandTracePVQUsesActiveThetaGeometry(t *testing.T) {
	t.Run("resumed recursive parent", func(t *testing.T) {
		top := celtQuantTraceThetaForTesting(0, 16, 4, 4, 2, 1, 0)
		parentSplit := celtQuantTraceThetaForTesting(1, 8, 2, 4, 1, 0, 0)
		childSplit := celtQuantTraceThetaForTesting(2, 4, 1, 2, 0, 0, 0)
		leaf := celtQuantTracePVQForTesting(3, parentSplit)
		merge := celtQuantTraceEventForTesting(top, celt.CELTQuantBandTraceStereoMerge, 4)
		output := celtQuantTraceEventForTesting(top, celt.CELTQuantBandTraceBandOutput, 5)
		events := []celt.CELTQuantBandTraceSnapshot{top, parentSplit, childSplit, leaf, merge, output}
		if err := validateCELTQuantBandTraceEvents(events); err != nil {
			t.Fatalf("resumed PVQ leaf should link to its earlier active theta: %v", err)
		}

		badGeometry := append([]celt.CELTQuantBandTraceSnapshot(nil), events...)
		badGeometry[3].LM = 0
		err := validateCELTQuantBandTraceEvents(badGeometry)
		if err == nil || !strings.Contains(err.Error(), "leaf LM != active theta LM") {
			t.Fatalf("invalid recursive leaf geometry error=%v, want active-theta LM mismatch", err)
		}
	})

	t.Run("side leaf resumes outer stereo theta", func(t *testing.T) {
		topStereo := celtQuantTraceThetaForTesting(0, 16, 4, 4, 2, 1, 0)
		midDescendant := celtQuantTraceThetaForTesting(1, 8, 2, 4, 1, 0, 0)
		midLeaf := celtQuantTracePVQForTesting(2, midDescendant)
		sideLeaf := celtQuantTracePVQForTesting(3, topStereo)
		merge := celtQuantTraceEventForTesting(topStereo, celt.CELTQuantBandTraceStereoMerge, 4)
		output := celtQuantTraceEventForTesting(topStereo, celt.CELTQuantBandTraceBandOutput, 5)
		events := []celt.CELTQuantBandTraceSnapshot{topStereo, midDescendant, midLeaf, sideLeaf, merge, output}
		if err := validateCELTQuantBandTraceEvents(events); err != nil {
			t.Fatalf("side PVQ leaf should resume the enclosing stereo theta after its mid descendant: %v", err)
		}
	})

	t.Run("time-divided block count", func(t *testing.T) {
		top := celtQuantTraceThetaForTesting(0, 24, 2, 2, 1, 1, 0)
		leaf := celtQuantTracePVQForTesting(1, top)
		leaf.B = 4
		leaf.B0 = 4
		merge := celtQuantTraceEventForTesting(top, celt.CELTQuantBandTraceStereoMerge, 2)
		output := celtQuantTraceEventForTesting(top, celt.CELTQuantBandTraceBandOutput, 3)
		events := []celt.CELTQuantBandTraceSnapshot{top, leaf, merge, output}
		if err := validateCELTQuantBandTraceEvents(events); err != nil {
			t.Fatalf("power-of-two time-divided block count should be valid: %v", err)
		}

		events[1].B = 3
		events[1].B0 = 3
		err := validateCELTQuantBandTraceEvents(events)
		if err == nil || !strings.Contains(err.Error(), "power-of-two transform") {
			t.Fatalf("invalid transformed block geometry error=%v, want power-of-two B mismatch", err)
		}

		events[1].B = 4
		events[1].B0 = 4
		if err := validateCELTQuantBandTraceEvents(events); err != nil {
			t.Fatalf("restored valid transformed block count rejected: %v", err)
		}
		events[1].B = 32
		events[1].B0 = 32
		err = validateCELTQuantBandTraceEvents(events)
		if err == nil || !strings.Contains(err.Error(), "leaf B must divide N") {
			t.Fatalf("oversized leaf B error=%v, want B<=N geometry failure", err)
		}

		events[1].B = 16
		events[1].B0 = 16
		err = validateCELTQuantBandTraceEvents(events)
		if err == nil || !strings.Contains(err.Error(), "leaf B must divide N") {
			t.Fatalf("nondividing leaf B error=%v, want N%%B geometry failure", err)
		}
	})
}

func TestCELTQuantBandTraceAcceptsDualStereoMonoPartitions(t *testing.T) {
	leftTheta := celtQuantTraceThetaForTesting(0, 4, 2, 2, 2, 0, 0)
	leftPVQ := celtQuantTracePVQForTesting(1, leftTheta)
	leftPVQ2 := celtQuantTracePVQForTesting(2, leftTheta)
	rightTheta := celtQuantTraceThetaForTesting(3, 4, 2, 2, 2, 0, 0)
	rightPVQ := celtQuantTracePVQForTesting(4, rightTheta)
	rightPVQ2 := celtQuantTracePVQForTesting(5, rightTheta)
	events := []celt.CELTQuantBandTraceSnapshot{leftTheta, leftPVQ, leftPVQ2, rightTheta, rightPVQ, rightPVQ2}
	if err := validateCELTQuantBandTraceEvents(events); err != nil {
		t.Fatalf("source-shaped non-stereo partition graph should validate: %v", err)
	}

	if err := validateCELTQuantBandTraceEvents(events[:5]); err == nil {
		t.Fatal("incomplete non-stereo graph accepted")
	}
	wrongN := append([]celt.CELTQuantBandTraceSnapshot(nil), events...)
	wrongN[0].N = 8
	if err := validateCELTQuantBandTraceEvents(wrongN); err == nil {
		t.Fatal("non-stereo graph with wrong N accepted")
	}
	wrongLM := append([]celt.CELTQuantBandTraceSnapshot(nil), events...)
	wrongLM[0].LM = 1
	if err := validateCELTQuantBandTraceEvents(wrongLM); err == nil {
		t.Fatal("non-stereo graph with wrong LM accepted")
	}
	wrongStereo := append([]celt.CELTQuantBandTraceSnapshot(nil), events...)
	wrongStereo[3].Stereo = 1
	if err := validateCELTQuantBandTraceEvents(wrongStereo); err == nil {
		t.Fatal("non-stereo graph with a stereo theta accepted")
	}

	withRDO := append([]celt.CELTQuantBandTraceSnapshot(nil), events...)
	rdo := celtQuantTraceEventForTesting(leftTheta, celt.CELTQuantBandTraceRDOSelect, 6)
	rdo.SelectedRound = -1
	withRDO = append(withRDO, rdo)
	if err := validateCELTQuantBandTraceEvents(withRDO); err == nil {
		t.Fatal("non-stereo graph accepted an RDO selection")
	}
}

func celtQuantTraceThetaForTesting(ordinal, n, blocks, blocksBeforeSplit uint32, lm int32, stereo uint32, thetaRound int32) celt.CELTQuantBandTraceSnapshot {
	return celt.CELTQuantBandTraceSnapshot{
		Stage: uint32(celt.CELTQuantBandTraceTheta), Ordinal: ordinal, ThetaOrdinal: ordinal,
		Band: 17, N: n, B: blocks, B0: blocksBeforeSplit, LM: lm,
		Channels: 2, Encode: 1, Stereo: stereo, ThetaRound: thetaRound,
		RangeBefore: 0x80000000 + ordinal, RangeAfter: 0x70000000 + ordinal,
		TellFracBefore: 100 + ordinal, TellFracAfter: 200 + ordinal,
	}
}

func celtQuantTracePVQForTesting(ordinal uint32, theta celt.CELTQuantBandTraceSnapshot) celt.CELTQuantBandTraceSnapshot {
	event := celtQuantTraceEventForTesting(theta, celt.CELTQuantBandTracePVQ, ordinal)
	event.B0 = event.B
	event.K = 12
	event.Spread = 2
	event.Resynth = 1
	return event
}

func celtQuantTraceEventForTesting(theta celt.CELTQuantBandTraceSnapshot, stage celt.CELTQuantBandTraceStage, ordinal uint32) celt.CELTQuantBandTraceSnapshot {
	event := theta
	event.Stage = uint32(stage)
	event.Ordinal = ordinal
	event.ThetaOrdinal = theta.Ordinal
	event.RangeBefore = 0x80000000 + ordinal
	event.RangeAfter = 0x70000000 + ordinal
	event.TellFracBefore = 100 + ordinal
	event.TellFracAfter = 200 + ordinal
	return event
}
