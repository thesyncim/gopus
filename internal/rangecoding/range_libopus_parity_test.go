package rangecoding

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusRangeInputMagic  = "GRCI"
	libopusRangeOutputMagic = "GRCO"

	rangeOpEncode     = uint32(0)
	rangeOpEncodeBin  = uint32(1)
	rangeOpEncodeBit  = uint32(2)
	rangeOpEncodeICDF = uint32(3)
	rangeOpEncodeIC16 = uint32(4)
	rangeOpEncodeUint = uint32(5)
	rangeOpEncodeBits = uint32(6)
	rangeOpPatch      = uint32(7)
	rangeOpShrink     = uint32(8)
	rangeOpDone       = uint32(9)
	rangeOpThetaRDO   = uint32(10)
)

var (
	libopusRangeHelper libopustest.HelperCache

	rangeICDF8Tables = [][]uint8{
		{128, 0},
		{220, 170, 100, 20, 0},
		{250, 240, 200, 150, 80, 20, 0},
	}
	rangeICDF16Tables = [][]uint16{
		{500, 300, 100, 0},
		{65000, 60000, 45000, 20000, 1000, 0},
	}
)

type rangeOracleOp struct {
	kind uint32
	a    uint32
	b    uint32
	c    uint32
	d    uint32
}

type rangeOracleTrace struct {
	tell       uint32
	tellFrac   uint32
	rangeBytes uint32
	rng        uint32
	val        uint32
	rem        uint32
	ext        uint32
	err        uint32
}

type rangeOracleResult struct {
	traces []rangeOracleTrace
	packet []byte
}

func buildLibopusRangeHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "range",
		OutputBase:  "gopus_libopus_range_coder",
		SourceFile:  "libopus_range_coder_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		RefIncludes: []string{"celt"},
	})
}

func getLibopusRangeHelperPath() (string, error) {
	return libopusRangeHelper.Path(buildLibopusRangeHelper)
}

func probeLibopusRangeEncoder(storage uint32, ops []rangeOracleOp) (rangeOracleResult, error) {
	binPath, err := getLibopusRangeHelperPath()
	if err != nil {
		return rangeOracleResult{}, err
	}
	payload := libopustest.NewOraclePayload(libopusRangeInputMagic, storage, uint32(len(ops)))
	for _, op := range ops {
		payload.U32s(op.kind, op.a, op.b, op.c, op.d)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return rangeOracleResult{}, fmt.Errorf("run range helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("range", libopusRangeOutputMagic, data)
	if err != nil {
		return rangeOracleResult{}, err
	}
	traceCount := reader.Count(len(ops))
	packetLen := int(reader.U32())
	reader.ExpectRemaining(traceCount*32 + packetLen)
	result := rangeOracleResult{traces: make([]rangeOracleTrace, traceCount)}
	for i := range result.traces {
		result.traces[i] = rangeOracleTrace{
			tell:       reader.U32(),
			tellFrac:   reader.U32(),
			rangeBytes: reader.U32(),
			rng:        reader.U32(),
			val:        reader.U32(),
			rem:        reader.U32(),
			ext:        reader.U32(),
			err:        reader.U32(),
		}
	}
	result.packet = append([]byte(nil), reader.Bytes(packetLen)...)
	if err := reader.ExpectConsumed(); err != nil {
		return rangeOracleResult{}, err
	}
	return result, nil
}

func applyThetaRDOTrial1Wins(enc *Encoder) {
	var ecSave EncoderState
	enc.SaveStateInto(&ecSave)

	enc.EncodeRawBits(0x5a, 8)
	enc.EncodeRawBits(0xc3, 8)
	enc.EncodeRawBits(0x7e, 8)
	enc.EncodeRawBits(0x18, 8)
	enc.EncodeRawBits(0xa5, 8)

	start := int(ecSave.offs)
	end := int(ecSave.storage)
	bytesSave := append([]byte(nil), enc.buf[start:end]...)

	enc.RestoreStateShallow(&ecSave)
	enc.EncodeUniform(3, 17)

	_ = bytesSave
}

func compactRangePacketForTest(enc *Encoder) []byte {
	if enc.shrunk || enc.err != 0 {
		return append([]byte(nil), enc.buf[:enc.storage]...)
	}
	partial := 0
	if enc.nendBits&7 != 0 && enc.endOffs < enc.storage {
		partial = 1
	}
	packetLen := int(enc.offs) + partial + int(enc.endOffs)
	packet := make([]byte, packetLen)
	copy(packet, enc.buf[:enc.offs])
	if partial != 0 {
		packet[enc.offs] = enc.buf[enc.storage-enc.endOffs-1]
	}
	if enc.endOffs > 0 {
		copy(packet[int(enc.offs)+partial:], enc.buf[enc.storage-enc.endOffs:enc.storage])
	}
	return packet
}

func encodeRangeOpsWithGo(storage uint32, ops []rangeOracleOp) rangeOracleResult {
	buf := make([]byte, storage)
	var enc Encoder
	enc.Init(buf)
	result := rangeOracleResult{traces: make([]rangeOracleTrace, len(ops))}
	for i, op := range ops {
		switch op.kind {
		case rangeOpEncode:
			enc.Encode(op.a, op.b, op.c)
		case rangeOpEncodeBin:
			enc.EncodeBin(op.a, op.b, uint(op.c))
		case rangeOpEncodeBit:
			enc.EncodeBit(int(op.a), uint(op.b))
		case rangeOpEncodeICDF:
			enc.EncodeICDF(int(op.a), rangeICDF8Tables[op.c], uint(op.b))
		case rangeOpEncodeIC16:
			enc.EncodeICDF16(int(op.a), rangeICDF16Tables[op.c], uint(op.b))
		case rangeOpEncodeUint:
			enc.EncodeUniform(op.a, op.b)
		case rangeOpEncodeBits:
			enc.EncodeRawBits(op.a, uint(op.b))
		case rangeOpPatch:
			enc.PatchInitialBits(op.a, uint(op.b))
		case rangeOpShrink:
			enc.Shrink(op.a)
		case rangeOpDone:
			result.packet = append([]byte(nil), enc.Done()...)
		case rangeOpThetaRDO:
			applyThetaRDOTrial1Wins(&enc)
		}
		result.traces[i] = rangeOracleTrace{
			tell:       uint32(enc.Tell()),
			tellFrac:   uint32(enc.TellFrac()),
			rangeBytes: uint32(enc.RangeBytes()),
			rng:        enc.Range(),
			val:        enc.Val(),
			rem:        uint32(int32(enc.Rem())),
			ext:        enc.Ext(),
			err:        uint32(int32(enc.Error())),
		}
	}
	if len(result.packet) == 0 {
		result.packet = compactRangePacketForTest(&enc)
	}
	return result
}

func verifyRangeDecodeOps(t *testing.T, packet []byte, ops []rangeOracleOp) {
	t.Helper()
	var dec Decoder
	dec.Init(packet)
	for i, op := range ops {
		switch op.kind {
		case rangeOpEncode:
			fs := dec.Decode(op.c)
			if fs < op.a || fs >= op.b {
				t.Fatalf("op %d Decode(%d)=%d, want in [%d,%d)", i, op.c, fs, op.a, op.b)
			}
			dec.Update(op.a, op.b, op.c)
		case rangeOpEncodeBin:
			fs := dec.DecodeBin(uint(op.c))
			if fs < op.a || fs >= op.b {
				t.Fatalf("op %d DecodeBin(%d)=%d, want in [%d,%d)", i, op.c, fs, op.a, op.b)
			}
			dec.Update(op.a, op.b, 1<<op.c)
		case rangeOpEncodeBit:
			if got := dec.DecodeBit(uint(op.b)); got != int(op.a) {
				t.Fatalf("op %d DecodeBit(%d)=%d want %d", i, op.b, got, op.a)
			}
		case rangeOpEncodeICDF:
			if got := dec.DecodeICDF(rangeICDF8Tables[op.c], uint(op.b)); got != int(op.a) {
				t.Fatalf("op %d DecodeICDF=%d want %d", i, got, op.a)
			}
		case rangeOpEncodeIC16:
			if got := dec.DecodeICDF16(rangeICDF16Tables[op.c], uint(op.b)); got != int(op.a) {
				t.Fatalf("op %d DecodeICDF16=%d want %d", i, got, op.a)
			}
		case rangeOpEncodeUint:
			if got := dec.DecodeUniform(op.b); got != op.a {
				t.Fatalf("op %d DecodeUniform(%d)=%d want %d", i, op.b, got, op.a)
			}
		case rangeOpEncodeBits:
			if got := dec.DecodeRawBits(uint(op.b)); got != op.a {
				t.Fatalf("op %d DecodeRawBits(%d)=%#x want %#x", i, op.b, got, op.a)
			}
		case rangeOpDone:
			return
		}
	}
}

func TestRangeCoderMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name         string
		storage      uint32
		ops          []rangeOracleOp
		verifyDecode bool
	}{
		{
			name:    "mixed",
			storage: 1024,
			ops: []rangeOracleOp{
				{kind: rangeOpEncodeUint, a: 0, b: 2},
				{kind: rangeOpEncodeUint, a: 7, b: 19},
				{kind: rangeOpEncodeBits, a: 0x15, b: 5},
				{kind: rangeOpEncode, a: 1, b: 3, c: 7},
				{kind: rangeOpEncodeBin, a: 4, b: 7, c: 3},
				{kind: rangeOpEncodeBit, a: 1, b: 4},
				{kind: rangeOpEncodeBit, a: 0, b: 3},
				{kind: rangeOpEncodeICDF, a: 2, b: 8, c: 1},
				{kind: rangeOpEncodeICDF, a: 5, b: 8, c: 2},
				{kind: rangeOpEncodeIC16, a: 1, b: 9, c: 0},
				{kind: rangeOpEncodeUint, a: 300, b: 511},
				{kind: rangeOpEncodeBits, a: 0x2aa, b: 10},
				{kind: rangeOpDone},
			},
			verifyDecode: true,
		},
		{
			name:    "patch_initial_bits",
			storage: 64,
			ops: []rangeOracleOp{
				{kind: rangeOpEncodeBin, a: 0, b: 1, c: 1},
				{kind: rangeOpPatch, a: 1, b: 1},
				{kind: rangeOpEncodeUint, a: 5, b: 17},
				{kind: rangeOpEncodeICDF, a: 3, b: 8, c: 1},
				{kind: rangeOpDone},
			},
		},
		{
			name:    "shrink",
			storage: 19,
			ops: []rangeOracleOp{
				{kind: rangeOpEncodeUint, a: 1, b: 3},
				{kind: rangeOpEncodeBits, a: 0x1ff, b: 9},
				{kind: rangeOpEncodeUint, a: 23, b: 257},
				{kind: rangeOpEncodeBit, a: 0, b: 5},
				{kind: rangeOpShrink, a: 11},
				{kind: rangeOpDone},
			},
			verifyDecode: true,
		},
		{
			name:    "buffer_bust_prefers_range_data",
			storage: 2,
			ops: []rangeOracleOp{
				{kind: rangeOpEncodeBits, a: 0x55, b: 7},
				{kind: rangeOpEncodeUint, a: 1, b: 2},
				{kind: rangeOpEncodeUint, a: 1, b: 3},
				{kind: rangeOpEncodeUint, a: 1, b: 4},
				{kind: rangeOpEncodeUint, a: 1, b: 5},
				{kind: rangeOpEncodeUint, a: 2, b: 6},
				{kind: rangeOpEncodeUint, a: 6, b: 7},
				{kind: rangeOpDone},
			},
		},
		{
			name:    "theta_rdo_trial1_preserves_trial0_dirty_gap",
			storage: 12,
			ops: []rangeOracleOp{
				{kind: rangeOpShrink, a: 12},
				{kind: rangeOpThetaRDO},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusRangeEncoder(tc.storage, tc.ops)
			if err != nil {
				libopustest.HelperUnavailable(t, "range", err)
			}
			got := encodeRangeOpsWithGo(tc.storage, tc.ops)
			if len(got.traces) != len(want.traces) {
				t.Fatalf("trace count=%d want %d", len(got.traces), len(want.traces))
			}
			for i := range got.traces {
				if got.traces[i] != want.traces[i] {
					t.Fatalf("trace %d=%+v want %+v", i, got.traces[i], want.traces[i])
				}
			}
			if !bytes.Equal(got.packet, want.packet) {
				t.Fatalf("packet=%x want %x", got.packet, want.packet)
			}
			if tc.verifyDecode {
				verifyRangeDecodeOps(t, want.packet, tc.ops)
			}
		})
	}
}
