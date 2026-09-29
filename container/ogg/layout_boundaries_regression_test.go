package ogg

import (
	"bytes"
	"testing"
)

func layoutBoundaryOpusHead(channels, family, streams, coupled uint8, mapping []byte) []byte {
	head := make([]byte, 21)
	copy(head, opusHeadMagic)
	head[8] = opusHeadVersion
	head[9] = channels
	head[18] = family
	head[19] = streams
	head[20] = coupled
	if family == MappingFamilyProjection {
		return append(head, make([]byte, expectedDemixingMatrixSize(channels, streams, coupled))...)
	}
	return append(head, mapping...)
}

func layoutBoundaryReader(head []byte) *bytes.Reader {
	page := &Page{
		HeaderType: PageFlagBOS,
		Segments:   BuildSegmentTable(len(head)),
		Payload:    head,
	}
	return bytes.NewReader(page.Encode())
}

func TestParseOpusHeadDecodedChannelBudget(t *testing.T) {
	tests := []struct {
		name    string
		family  uint8
		streams uint8
		coupled uint8
		mapping byte
		wantErr bool
	}{
		{name: "total 255 last index", family: MappingFamilyDiscrete, streams: 200, coupled: 55, mapping: 254},
		{name: "total 255 ignored channel", family: MappingFamilyDiscrete, streams: 200, coupled: 55, mapping: 255},
		{name: "total 256 ignored channel", family: MappingFamilyVorbis, streams: 200, coupled: 56, mapping: 255, wantErr: true},
		{name: "total 300 family 1", family: MappingFamilyVorbis, streams: 200, coupled: 100, mapping: 0, wantErr: true},
		{name: "total 300 family 2", family: MappingFamilyAmbisonics, streams: 200, coupled: 100, mapping: 0, wantErr: true},
		{name: "total 300 family 3", family: MappingFamilyProjection, streams: 200, coupled: 100, wantErr: true},
		{name: "total 300 family 255", family: MappingFamilyDiscrete, streams: 200, coupled: 100, mapping: 0, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			head := layoutBoundaryOpusHead(1, tc.family, tc.streams, tc.coupled, []byte{tc.mapping})
			parsed, err := ParseOpusHead(head)
			if tc.wantErr {
				if err != ErrInvalidHeader {
					t.Fatalf("ParseOpusHead error = %v, want %v", err, ErrInvalidHeader)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOpusHead: %v", err)
			}
			if tc.mapping == 255 && parsed.ChannelMapping[0] != 255 {
				t.Fatalf("ignored mapping = %d, want 255", parsed.ChannelMapping[0])
			}
		})
	}
}

func TestNewReaderRejectsDecodedChannelBudgetOverflow(t *testing.T) {
	head := layoutBoundaryOpusHead(1, MappingFamilyVorbis, 200, 100, []byte{0})
	if _, err := NewReader(layoutBoundaryReader(head)); err != ErrInvalidHeader {
		t.Fatalf("NewReader error = %v, want %v", err, ErrInvalidHeader)
	}
}

func TestNewWriterWithConfigDecodedChannelBudget(t *testing.T) {
	tests := []struct {
		name    string
		family  uint8
		streams uint8
		coupled uint8
		mapping []byte
		matrix  []byte
		wantErr bool
	}{
		{name: "total 255 last index", family: MappingFamilyDiscrete, streams: 200, coupled: 55, mapping: []byte{254}},
		{name: "total 255 ignored channel", family: MappingFamilyDiscrete, streams: 200, coupled: 55, mapping: []byte{255}},
		{name: "total 256 ignored channel", family: MappingFamilyVorbis, streams: 200, coupled: 56, mapping: []byte{255}, wantErr: true},
		{name: "total 300 family 1", family: MappingFamilyVorbis, streams: 200, coupled: 100, mapping: []byte{0}, wantErr: true},
		{name: "total 300 family 2", family: MappingFamilyAmbisonics, streams: 200, coupled: 100, mapping: []byte{0}, wantErr: true},
		{name: "total 300 family 3", family: MappingFamilyProjection, streams: 200, coupled: 100, wantErr: true},
		{name: "total 300 family 255", family: MappingFamilyDiscrete, streams: 200, coupled: 100, mapping: []byte{0}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			_, err := NewWriterWithConfig(&output, WriterConfig{
				SampleRate:     48000,
				Channels:       1,
				MappingFamily:  tc.family,
				StreamCount:    tc.streams,
				CoupledCount:   tc.coupled,
				ChannelMapping: tc.mapping,
				DemixingMatrix: tc.matrix,
			})
			if tc.wantErr {
				if err != ErrInvalidHeader {
					t.Fatalf("NewWriterWithConfig error = %v, want %v", err, ErrInvalidHeader)
				}
				if output.Len() != 0 {
					t.Fatalf("writer emitted %d bytes for invalid layout", output.Len())
				}
				return
			}
			if err != nil {
				t.Fatalf("NewWriterWithConfig: %v", err)
			}
			reader, err := NewReader(bytes.NewReader(output.Bytes()))
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			if len(tc.mapping) > 0 && tc.mapping[0] == 255 && reader.Header.ChannelMapping[0] != 255 {
				t.Fatalf("ignored mapping = %d, want 255", reader.Header.ChannelMapping[0])
			}
		})
	}
}

func TestNewWriterWithConfigKeepsValidProjectionMatrix(t *testing.T) {
	matrix := make([]byte, expectedDemixingMatrixSize(4, 2, 2))
	for i := range matrix {
		matrix[i] = byte(i)
	}
	var output bytes.Buffer
	_, err := NewWriterWithConfig(&output, WriterConfig{
		SampleRate:     48000,
		Channels:       4,
		MappingFamily:  MappingFamilyProjection,
		StreamCount:    2,
		CoupledCount:   2,
		DemixingMatrix: matrix,
	})
	if err != nil {
		t.Fatalf("NewWriterWithConfig: %v", err)
	}
	reader, err := NewReader(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if !bytes.Equal(reader.Header.DemixingMatrix, matrix) {
		t.Fatal("projection demixing matrix changed in the Ogg header")
	}
}
