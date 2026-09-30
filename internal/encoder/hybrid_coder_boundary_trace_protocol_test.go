//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

type hybridCoderBoundaryCTrace struct {
	TargetFrame           uint32
	FrameCalls            uint32
	SILKCalls             uint32
	CELTCalls             uint32
	TargetPublicCalls     uint32
	TargetSILKCalls       uint32
	TargetSILKRangeCalls  uint32
	TargetCELTCalls       uint32
	TargetSharedCELTCalls uint32
	Overflow              uint32
	SameEC                bool
	SameBuffer            bool
	Records               []hybridCoderBoundaryCRecord
}

type hybridCoderBoundaryCRecord struct {
	Frame     uint32
	Stage     uint32
	CallIndex uint32
	State     hybridCoderBoundaryState
}

type hybridCoderBoundaryState struct {
	Storage    uint32
	Offs       uint32
	EndOffs    uint32
	EndWindow  uint32
	Range      uint32
	Value      uint32
	Extension  uint32
	NBitsTotal int32
	NEndBits   int32
	Remainder  int32
	Error      int32
	Tell       int32
	TellFrac   int32
	Forward    []byte
	Backward   []byte
}

func (trace hybridCoderBoundaryCTrace) header() string {
	return fmt.Sprintf("target=%d frame-calls=%d silk-calls=%d celt-calls=%d selected-calls=%d/%d(coder=%d)/%d(shared=%d) records=%d overflow=%d same-ec=%t same-buffer=%t",
		trace.TargetFrame, trace.FrameCalls, trace.SILKCalls, trace.CELTCalls,
		trace.TargetPublicCalls, trace.TargetSILKCalls, trace.TargetSILKRangeCalls,
		trace.TargetCELTCalls, trace.TargetSharedCELTCalls,
		len(trace.Records), trace.Overflow, trace.SameEC, trace.SameBuffer)
}

func parseHybridCoderBoundaryTrace(data []byte) (hybridCoderBoundaryCTrace, error) {
	var trace hybridCoderBoundaryCTrace
	if len(data) < 60 || string(data[:4]) != "GCHB" || binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return trace, fmt.Errorf("invalid GCHB version-1 header")
	}
	reader := hybridCoderBoundaryReader{data: data, off: 8}
	var err error
	if trace.TargetFrame, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.FrameCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.SILKCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.CELTCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetPublicCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetSILKCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetSILKRangeCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetCELTCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetSharedCELTCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	recordCount, err := reader.u32()
	if err != nil {
		return trace, err
	}
	if trace.Overflow, err = reader.u32(); err != nil {
		return trace, err
	}
	sameEC, err := reader.u32()
	if err != nil {
		return trace, err
	}
	sameBuffer, err := reader.u32()
	if err != nil {
		return trace, err
	}
	if sameEC > 1 || sameBuffer > 1 || recordCount > 3 {
		return trace, fmt.Errorf("invalid GCHB flags/count: same_ec=%d same_buffer=%d records=%d", sameEC, sameBuffer, recordCount)
	}
	trace.SameEC, trace.SameBuffer = sameEC == 1, sameBuffer == 1
	trace.Records = make([]hybridCoderBoundaryCRecord, 0, recordCount)
	for i := uint32(0); i < recordCount; i++ {
		var record hybridCoderBoundaryCRecord
		if record.Frame, err = reader.u32(); err != nil {
			return trace, err
		}
		if record.Stage, err = reader.u32(); err != nil {
			return trace, err
		}
		if record.CallIndex, err = reader.u32(); err != nil {
			return trace, err
		}
		fields := []*uint32{
			&record.State.Storage, &record.State.Offs, &record.State.EndOffs, &record.State.EndWindow,
			&record.State.Range, &record.State.Value, &record.State.Extension,
		}
		for _, field := range fields {
			if *field, err = reader.u32(); err != nil {
				return trace, err
			}
		}
		signed := []*int32{&record.State.NBitsTotal, &record.State.NEndBits, &record.State.Remainder,
			&record.State.Error, &record.State.Tell, &record.State.TellFrac}
		for _, field := range signed {
			value, readErr := reader.u32()
			if readErr != nil {
				return trace, readErr
			}
			*field = int32(value)
		}
		forwardLen, readErr := reader.u32()
		if readErr != nil {
			return trace, readErr
		}
		backwardLen, readErr := reader.u32()
		if readErr != nil {
			return trace, readErr
		}
		if forwardLen > 4096 || backwardLen > 4096 || forwardLen > uint32(len(data)-reader.off) {
			return trace, fmt.Errorf("invalid GCHB byte lengths record=%d forward=%d backward=%d", i, forwardLen, backwardLen)
		}
		record.State.Forward, err = reader.bytes(forwardLen)
		if err != nil {
			return trace, err
		}
		record.State.Backward, err = reader.bytes(backwardLen)
		if err != nil {
			return trace, err
		}
		if record.State.Offs != forwardLen || record.State.EndOffs != backwardLen ||
			uint64(record.State.Offs)+uint64(record.State.EndOffs) > uint64(record.State.Storage) {
			return trace, fmt.Errorf("invalid GCHB cursor/byte lengths in record %d", i)
		}
		if record.State.Storage > 4096 || record.State.Range == 0 {
			return trace, fmt.Errorf("invalid GCHB coder state in record %d: storage=%d range=%d",
				i, record.State.Storage, record.State.Range)
		}
		trace.Records = append(trace.Records, record)
	}
	if reader.off != len(data) {
		return trace, fmt.Errorf("GCHB trace has %d trailing bytes", len(data)-reader.off)
	}
	return trace, nil
}

func assertHybridCoderBoundaryParserRejectsInvalidState(t *testing.T, validTrace []byte) {
	t.Helper()
	const firstRecord = 60
	if len(validTrace) < firstRecord+72 {
		t.Fatalf("valid GCHB trace has no complete record for malformed-state checks: %d bytes", len(validTrace))
	}
	for _, invalid := range []struct {
		name   string
		offset int
		value  uint32
	}{
		{name: "storage exceeds capture bound", offset: firstRecord + 12, value: 4097},
		{name: "zero range", offset: firstRecord + 28, value: 0},
	} {
		malformed := append([]byte(nil), validTrace...)
		binary.LittleEndian.PutUint32(malformed[invalid.offset:invalid.offset+4], invalid.value)
		if _, err := parseHybridCoderBoundaryTrace(malformed); err == nil {
			t.Fatalf("GCHB parser accepted %s", invalid.name)
		}
	}
}

type hybridCoderBoundaryReader struct {
	data []byte
	off  int
}

func (reader *hybridCoderBoundaryReader) u32() (uint32, error) {
	if reader.off < 0 || reader.off+4 > len(reader.data) {
		return 0, fmt.Errorf("truncated GCHB u32 at offset %d", reader.off)
	}
	value := binary.LittleEndian.Uint32(reader.data[reader.off:])
	reader.off += 4
	return value, nil
}

func (reader *hybridCoderBoundaryReader) bytes(count uint32) ([]byte, error) {
	if uint64(count) > uint64(len(reader.data)-reader.off) {
		return nil, fmt.Errorf("truncated GCHB byte range at offset %d length %d", reader.off, count)
	}
	start := reader.off
	reader.off += int(count)
	return append([]byte(nil), reader.data[start:reader.off]...), nil
}

func firstHybridCoderBoundaryDifference(goState rangecoding.BoundaryTraceSnapshot, cState hybridCoderBoundaryState) string {
	fields := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"storage", goState.Storage, cState.Storage},
		{"offs", goState.Offs, cState.Offs},
		{"end_offs", goState.EndOffs, cState.EndOffs},
		{"end_window", goState.EndWindow, cState.EndWindow},
		{"range", goState.Range, cState.Range},
		{"val", goState.Value, cState.Value},
		{"ext", goState.Extension, cState.Extension},
		{"nbits_total", uint32(goState.NBitsTotal), uint32(cState.NBitsTotal)},
		{"nend_bits", uint32(goState.NEndBits), uint32(cState.NEndBits)},
		{"rem", uint32(goState.Remainder), uint32(cState.Remainder)},
		{"error", uint32(goState.Error), uint32(cState.Error)},
		{"tell", uint32(goState.Tell), uint32(cState.Tell)},
		{"tell_frac", uint32(goState.TellFrac), uint32(cState.TellFrac)},
	}
	for _, field := range fields {
		if field.got != field.want {
			return fmt.Sprintf("%s Go=0x%08x C=0x%08x", field.name, field.got, field.want)
		}
	}
	if goState.ForwardLen != uint32(len(cState.Forward)) {
		return fmt.Sprintf("forward length Go=%d C=%d", goState.ForwardLen, len(cState.Forward))
	}
	forward := goState.Forward[:goState.ForwardLen]
	if !bytes.Equal(forward, cState.Forward) {
		index := firstHybridCoderTraceByteDifference(forward, cState.Forward)
		return fmt.Sprintf("forward byte[%d] Go=0x%02x C=0x%02x", index, forward[index], cState.Forward[index])
	}
	if goState.BackwardLen != uint32(len(cState.Backward)) {
		return fmt.Sprintf("backward length Go=%d C=%d", goState.BackwardLen, len(cState.Backward))
	}
	backward := goState.Backward[:goState.BackwardLen]
	if !bytes.Equal(backward, cState.Backward) {
		index := firstHybridCoderTraceByteDifference(backward, cState.Backward)
		return fmt.Sprintf("backward byte[%d] Go=0x%02x C=0x%02x", index, backward[index], cState.Backward[index])
	}
	return ""
}

func firstHybridCoderTraceByteDifference(a, b []byte) int {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

func hybridCoderBoundaryStageName(stage uint32) string {
	switch stage {
	case hybridCoderBoundarySILKExit:
		return "SILK exit"
	case hybridCoderBoundaryCELTEntry:
		return "CELT entry"
	case hybridCoderBoundaryCELTExit:
		return "CELT exit"
	default:
		return fmt.Sprintf("stage %d", stage)
	}
}
