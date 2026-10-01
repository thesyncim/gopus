//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

type analysisGetInfoTrace struct {
	frame, frames, fs, channels, frameSize, lsbDepth uint32
	driverHash                                       string
	meta                                             [analysisGetInfoTraceMetaLen]uint32
	linkedReturn, replayReturn                       analysisOracleInfo
	info                                             []analysisOracleInfo
}

func parseAnalysisGetInfoTrace(data []byte) (analysisGetInfoTrace, error) {
	const headerU32s = 8
	const infoBytes = 12*4 + 20
	var out analysisGetInfoTrace
	if len(data) < 4+headerU32s*4 || string(data[:4]) != "GGET" {
		return out, fmt.Errorf("invalid GGET header")
	}
	pos := 4
	read := func(label string) (uint32, error) {
		if len(data)-pos < 4 {
			return 0, fmt.Errorf("truncated GGET %s", label)
		}
		v := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4
		return v, nil
	}
	var err error
	version, err := read("version")
	if err != nil {
		return out, err
	}
	if version != analysisGetInfoTraceVersion {
		return out, fmt.Errorf("GGET version=%d want %d", version, analysisGetInfoTraceVersion)
	}
	fields := []*uint32{&out.frame, &out.frames, &out.fs, &out.channels, &out.frameSize, &out.lsbDepth}
	for i, dst := range fields {
		*dst, err = read(fmt.Sprintf("header field %d", i))
		if err != nil {
			return out, err
		}
	}
	hashLen, err := read("driver hash length")
	if err != nil {
		return out, err
	}
	if out.frame >= out.frames || hashLen != 64 {
		return out, fmt.Errorf("invalid GGET fixture metadata frame=%d frames=%d fs=%d channels=%d frame_size=%d lsb=%d hash_len=%d",
			out.frame, out.frames, out.fs, out.channels, out.frameSize, out.lsbDepth, hashLen)
	}
	if len(data)-pos < int(hashLen) {
		return out, fmt.Errorf("truncated GGET driver source hash")
	}
	hashBytes := data[pos : pos+int(hashLen)]
	if _, err := hex.DecodeString(string(hashBytes)); err != nil || strings.ToLower(string(hashBytes)) != string(hashBytes) {
		return out, fmt.Errorf("invalid GGET driver source hash")
	}
	out.driverHash = string(hashBytes)
	pos += int(hashLen)
	metaCount, err := read("metadata count")
	if err != nil {
		return out, err
	}
	if metaCount != analysisGetInfoTraceMetaLen {
		return out, fmt.Errorf("GGET metadata count=%d want %d", metaCount, analysisGetInfoTraceMetaLen)
	}
	for i := range out.meta {
		out.meta[i], err = read(fmt.Sprintf("metadata[%d]", i))
		if err != nil {
			return out, err
		}
	}
	if len(data)-pos < 2*infoBytes+4 {
		return out, fmt.Errorf("truncated GGET getter return records")
	}
	if out.linkedReturn, err = parseAnalysisGetInfoInfo(data[pos : pos+infoBytes]); err != nil {
		return out, fmt.Errorf("GGET original linked return: %w", err)
	}
	pos += infoBytes
	if out.replayReturn, err = parseAnalysisGetInfoInfo(data[pos : pos+infoBytes]); err != nil {
		return out, fmt.Errorf("GGET restored-cursor replay return: %w", err)
	}
	pos += infoBytes
	infoCount, err := read("Info ring count")
	if err != nil {
		return out, err
	}
	if infoCount != DetectSize {
		return out, fmt.Errorf("GGET Info ring count=%d want %d", infoCount, DetectSize)
	}
	if len(data)-pos < int(infoCount)*infoBytes {
		return out, fmt.Errorf("truncated GGET Info ring")
	}
	out.info = make([]analysisOracleInfo, infoCount)
	for i := range out.info {
		if out.info[i], err = parseAnalysisGetInfoInfo(data[pos : pos+infoBytes]); err != nil {
			return out, fmt.Errorf("GGET Info[%d]: %w", i, err)
		}
		pos += infoBytes
	}
	if pos != len(data) {
		return out, fmt.Errorf("GGET has %d trailing bytes", len(data)-pos)
	}
	if err := validateAnalysisGetInfoTrace(out); err != nil {
		return out, err
	}
	return out, nil
}

func parseAnalysisGetInfoInfo(data []byte) (analysisOracleInfo, error) {
	if len(data) != 12*4+20 {
		return analysisOracleInfo{}, fmt.Errorf("AnalysisInfo bytes=%d want %d", len(data), 12*4+20)
	}
	if binary.LittleEndian.Uint32(data[44:48]) != 0 || data[67] != 0 {
		return analysisOracleInfo{}, fmt.Errorf("nonzero reserved field or leak_boost padding")
	}
	info := readAnalysisGetInfoInfo(data)
	for _, bits := range [...]uint32{
		info.tonality, info.tonalitySlope, info.noisiness, info.activity,
		info.musicProb, info.musicMin, info.musicMax, info.activityProb, info.maxPitchRatio,
	} {
		if bits&0x7f800000 == 0x7f800000 {
			return analysisOracleInfo{}, fmt.Errorf("non-finite AnalysisInfo float %08x", bits)
		}
	}
	return info, nil
}

func validateAnalysisGetInfoTrace(trace analysisGetInfoTrace) error {
	meta := trace.meta
	if !analysisGetInfoTraceFixtureShape(trace) {
		return fmt.Errorf("unsupported strict GGET fixture/frame fs=%d channels=%d frame_size=%d frames=%d lsb=%d frame=%d",
			trace.fs, trace.channels, trace.frameSize, trace.frames, trace.lsbDepth, trace.frame)
	}
	if meta[0] >= DetectSize || meta[1] >= 8 || meta[4] != meta[0] || meta[5] != meta[1] ||
		meta[2] >= DetectSize || meta[3] >= 8 || meta[6] != meta[2] || meta[7] != meta[3] {
		return fmt.Errorf("GGET cursor restore/replay transition is inconsistent: %v", meta[:8])
	}
	if meta[8] >= DetectSize || meta[9] == 0 || meta[12] != 1 || meta[13] != 1 || meta[14] != 1 || meta[15] != 1 {
		return fmt.Errorf("GGET write/count/call/match/source-shape metadata is invalid: %v", meta[8:])
	}
	if len(trace.info) != DetectSize {
		return fmt.Errorf("GGET ring entries=%d want %d", len(trace.info), DetectSize)
	}
	validCount := 0
	for _, entry := range trace.info {
		if entry.valid != 0 {
			validCount++
		}
	}
	if validCount == 0 || validCount > int(meta[9]) {
		return fmt.Errorf("GGET valid Info ring entries=%d inconsistent with count=%d", validCount, meta[9])
	}
	for i, entry := range trace.info {
		if entry.valid > 1 {
			return fmt.Errorf("GGET Info[%d] validity flag=%d", i, entry.valid)
		}
	}
	if diff := diffAnalysisInfo(trace.linkedReturn, trace.replayReturn); diff != "" {
		return fmt.Errorf("GGET linked/replay return mismatch: %s", diff)
	}
	selected := int(meta[0])
	if trace.frameSize > trace.fs/50 && selected != int(meta[8]) {
		selected++
		if selected == DetectSize {
			selected = 0
		}
	}
	if selected == int(meta[8]) {
		selected--
	}
	if selected < 0 {
		selected = DetectSize - 1
	}
	if diff := diffAnalysisGetInfoRingHead(trace.linkedReturn, trace.info[selected]); diff != "" {
		return fmt.Errorf("GGET linked return/Info[%d] raw-field mismatch outside music postprocessing: %s", selected, diff)
	}
	return nil
}

func analysisGetInfoTraceFixtureShape(trace analysisGetInfoTrace) bool {
	switch {
	case trace.fs == 48000 && trace.channels == 1 && trace.frameSize == 2880 &&
		trace.frames == analysisGetInfoTraceFrames && trace.lsbDepth == 24:
		return trace.frame == 0
	case trace.fs == 16000 && trace.channels == 2 && trace.frameSize == 320 &&
		trace.frames == 60 && trace.lsbDepth == 24:
		return trace.frame == 0 || trace.frame == 2
	default:
		return false
	}
}

func diffAnalysisGetInfoRingHead(returned, ring analysisOracleInfo) string {
	// src/analysis.c:tonality_get_info copies tonal->info[pos] to info_out,
	// then postprocesses tonality, bandwidth, music_prob and its thresholds on
	// that copy. The ring retains the per-chunk values, so compare the rest.
	returned.tonality = ring.tonality
	returned.musicProb = ring.musicProb
	returned.musicMin = ring.musicMin
	returned.musicMax = ring.musicMax
	returned.bandwidth = ring.bandwidth
	return diffAnalysisInfo(returned, ring)
}

func TestParseAnalysisGetInfoTraceRejectsMalformed(t *testing.T) {
	valid := validAnalysisGetInfoTraceWireForTesting()
	if _, err := parseAnalysisGetInfoTrace(valid); err != nil {
		t.Fatalf("valid GGET fixture rejected: %v", err)
	}
	mutations := []struct {
		name string
		edit func([]byte) []byte
	}{
		{name: "short header", edit: func(data []byte) []byte { return data[:3] }},
		{name: "version", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 4, 2); return data }},
		{name: "frame", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 8, 1); return data }},
		{name: "source hash", edit: func(data []byte) []byte { data[36] = 'g'; return data }},
		{name: "metadata count", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 100, analysisGetInfoTraceMetaLen-1); return data }},
		{name: "truncated metadata", edit: func(data []byte) []byte { return data[:len(data)-1] }},
		{name: "restored cursor", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 104+4*4, 1); return data }},
		{name: "call count", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 104+4*12, 2); return data }},
		{name: "Info ring count", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, analysisGetInfoTraceInfoCountOffset(), DetectSize-1)
			return data
		}},
		{name: "invalid ring validity flag", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, analysisGetInfoTraceInfoRingOffset()+68, 2)
			return data
		}},
		{name: "NaN return", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, 104+4*analysisGetInfoTraceMetaLen+4, 0x7fc00000)
			return data
		}},
		{name: "infinite return", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, 104+4*analysisGetInfoTraceMetaLen+4, 0x7f800000)
			return data
		}},
		{name: "trailing byte", edit: func(data []byte) []byte { return append(data, 0) }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			data = tc.edit(data)
			if _, err := parseAnalysisGetInfoTrace(data); err == nil {
				t.Fatal("malformed GGET accepted")
			}
		})
	}
}

func validAnalysisGetInfoTraceWireForTesting() []byte {
	const infoBytes = 12*4 + 20
	data := make([]byte, 0, 4+8*4+64+4+analysisGetInfoTraceMetaLen*4+2*infoBytes+4+DetectSize*infoBytes)
	data = append(data, "GGET"...)
	for _, v := range [...]uint32{1, 0, 20, 48000, 1, 2880, 24, 64} {
		data = appendAnalysisGetInfoU32(data, v)
	}
	data = append(data, []byte(strings.Repeat("a", 64))...)
	data = appendAnalysisGetInfoU32(data, analysisGetInfoTraceMetaLen)
	for _, v := range [...]uint32{0, 0, 3, 0, 0, 0, 3, 0, 3, 3, 0, 0, 1, 1, 1, 1} {
		data = appendAnalysisGetInfoU32(data, v)
	}
	validInfo := make([]byte, infoBytes)
	validInfo[0] = 1
	data = append(data, validInfo...)
	data = append(data, validInfo...)
	data = appendAnalysisGetInfoU32(data, DetectSize)
	data = append(data, validInfo...)
	data = append(data, validInfo...)
	data = append(data, validInfo...)
	data = append(data, make([]byte, (DetectSize-3)*infoBytes)...)
	return data
}

func analysisGetInfoTraceInfoCountOffset() int {
	return 4 + 8*4 + 64 + 4 + analysisGetInfoTraceMetaLen*4 + 2*(12*4+20)
}

func analysisGetInfoTraceInfoRingOffset() int {
	return analysisGetInfoTraceInfoCountOffset() + 4
}

func appendAnalysisGetInfoU32(data []byte, value uint32) []byte {
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], value)
	return append(data, word[:]...)
}

func putAnalysisGetInfoU32(data []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
}
