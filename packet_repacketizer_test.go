package gopus

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type repacketizerLibopusFixture struct {
	PacketA   string `json:"packet_a"`
	PacketB   string `json:"packet_b"`
	PacketC   string `json:"packet_c"`
	ABOut     string `json:"ab_out"`
	ACOut     string `json:"ac_out"`
	ABCOut    string `json:"abc_out"`
	ABC13     string `json:"abc_1_3"`
	PadNewLen int    `json:"pad_new_len"`
	Pad12     string `json:"pad_12"`
	UnpadLen  int    `json:"unpad_len"`
	Unpad     string `json:"unpad"`
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q error: %v", s, err)
	}
	return b
}

func loadRepacketizerFixture(t *testing.T) repacketizerLibopusFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/repacketizer_libopus_fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f repacketizerLibopusFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return f
}

func TestRepacketizerParityWithLibopusFixture(t *testing.T) {
	f := loadRepacketizerFixture(t)

	a := mustDecodeHex(t, f.PacketA)
	b := mustDecodeHex(t, f.PacketB)
	c := mustDecodeHex(t, f.PacketC)

	rp := NewRepacketizer()
	out := make([]byte, 256)

	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(b); err != nil {
		t.Fatalf("cat(b): %v", err)
	}
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("out(ab): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABOut; got != want {
		t.Fatalf("out(ab)=%s want=%s", got, want)
	}

	rp.Reset()
	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(c); err != nil {
		t.Fatalf("cat(c): %v", err)
	}
	n, err = rp.Out(out)
	if err != nil {
		t.Fatalf("out(ac): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ACOut; got != want {
		t.Fatalf("out(ac)=%s want=%s", got, want)
	}

	rp.Reset()
	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(b); err != nil {
		t.Fatalf("cat(b): %v", err)
	}
	if err := rp.Cat(c); err != nil {
		t.Fatalf("cat(c): %v", err)
	}
	n, err = rp.Out(out)
	if err != nil {
		t.Fatalf("out(abc): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABCOut; got != want {
		t.Fatalf("out(abc)=%s want=%s", got, want)
	}
	n, err = rp.OutRange(1, 3, out)
	if err != nil {
		t.Fatalf("out_range(1,3): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABC13; got != want {
		t.Fatalf("out_range(1,3)=%s want=%s", got, want)
	}

	buf := make([]byte, f.PadNewLen)
	copy(buf, a)
	if err := PacketPad(buf, len(a), f.PadNewLen); err != nil {
		t.Fatalf("packet_pad: %v", err)
	}
	if got, want := hex.EncodeToString(buf[:f.PadNewLen]), f.Pad12; got != want {
		t.Fatalf("packet_pad=%s want=%s", got, want)
	}
	unpaddedLen, err := PacketUnpad(buf, f.PadNewLen)
	if err != nil {
		t.Fatalf("packet_unpad: %v", err)
	}
	if got, want := unpaddedLen, f.UnpadLen; got != want {
		t.Fatalf("packet_unpad len=%d want=%d", got, want)
	}
	if got, want := hex.EncodeToString(buf[:unpaddedLen]), f.Unpad; got != want {
		t.Fatalf("packet_unpad=%s want=%s", got, want)
	}
}

func TestRepacketizerRejectsTOCMismatch(t *testing.T) {
	rp := NewRepacketizer()
	p1 := []byte{0x48, 0x11, 0x22, 0x33}
	p2 := []byte{0x78, 0x44, 0x55, 0x66}

	if err := rp.Cat(p1); err != nil {
		t.Fatalf("cat(p1): %v", err)
	}
	if err := rp.Cat(p2); err != ErrInvalidPacket {
		t.Fatalf("cat(p2)=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestRepacketizerRejectsDurationOver120ms(t *testing.T) {
	rp := NewRepacketizer()

	packet120ms := append([]byte{GenerateTOC(16, false, 3), 0x30}, make([]byte, 48)...)
	if err := rp.Cat(packet120ms); err != nil {
		t.Fatalf("cat(120ms packet): %v", err)
	}

	oneMore := []byte{GenerateTOC(16, false, 0), 0x7f}
	if err := rp.Cat(oneMore); err != ErrInvalidPacket {
		t.Fatalf("cat(extra frame)=%v want=%v", err, ErrInvalidPacket)
	}
}
