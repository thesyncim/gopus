//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"encoding/binary"
	"os"
	"reflect"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestSilkPitchLagValues extracts and prints pitch lag values during decode.
func TestSilkPitchLagValues(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadLagPackets(t, bitFile, 5)
	if len(packets) < 2 {
		t.Skip("Could not load enough test packets")
	}

	// Create decoder
	dec := silk.NewDecoder()

	for pktIdx := 0; pktIdx < 2; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("\n=== Packet %d ===", pktIdx)
		t.Logf("TOC=0x%02X, Bandwidth=%d, FrameSize=%d", pkt[0], toc.Bandwidth, toc.FrameSize)

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("Invalid SILK bandwidth")
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		config := silk.GetBandwidthConfig(silkBW)
		t.Logf("Native rate: %d Hz", config.SampleRate)

		// Calculate expected parameters for this mode
		fsKHz := config.SampleRate / 1000
		ltpMemLength := 20 * fsKHz // 20ms in samples
		lpcOrder := 10
		if silkBW == silk.BandwidthWideband {
			lpcOrder = 16
		}
		ltpOrder := 5

		t.Logf("ltpMemLength=%d, lpcOrder=%d, ltpOrder=%d", ltpMemLength, lpcOrder, ltpOrder)

		// Decode the frame
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		_, err := dec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}

		// Extract internal state via reflection
		decVal := reflect.ValueOf(dec).Elem()
		stateField := decVal.FieldByName("state")

		// Get first channel's state
		st0 := reflect.NewAt(stateField.Index(0).Type(), unsafe.Pointer(stateField.Index(0).UnsafeAddr())).Elem()

		// Get indices to check signal type
		indicesField := st0.FieldByName("indices")
		signalType := reflect.NewAt(indicesField.FieldByName("signalType").Type(),
			unsafe.Pointer(indicesField.FieldByName("signalType").UnsafeAddr())).Elem().Int()

		t.Logf("signalType=%d (0=inactive, 1=unvoiced, 2=voiced)", signalType)

		if signalType == 2 { // TYPE_VOICED
			// Get pitch lags
			lagIndex := reflect.NewAt(indicesField.FieldByName("lagIndex").Type(),
				unsafe.Pointer(indicesField.FieldByName("lagIndex").UnsafeAddr())).Elem().Int()
			contourIndex := reflect.NewAt(indicesField.FieldByName("contourIndex").Type(),
				unsafe.Pointer(indicesField.FieldByName("contourIndex").UnsafeAddr())).Elem().Int()

			t.Logf("lagIndex=%d, contourIndex=%d", lagIndex, contourIndex)

			// Calculate pitch lags for each subframe
			// The actual pitch lag calculation is complex, but we can get the stored lags
			lagPrev := st0.FieldByName("lagPrev")
			if lagPrev.IsValid() {
				t.Logf("lagPrev=%d", lagPrev.Int())
			}

			// Calculate startIdx boundary for each subframe
			for k := 0; k < 4; k++ {
				// This is a rough estimate - actual lag is per-subframe
				// For now just show what would happen with lagPrev
				if lagPrev.IsValid() {
					lag := int(lagPrev.Int())
					startIdx := ltpMemLength - lag - lpcOrder - ltpOrder/2
					t.Logf("  k=%d: lag=%d, startIdx=%d (would be <0: %v)", k, lag, startIdx, startIdx < 0)
				}
			}
		}
	}
}

// TestStartIdxBoundary specifically tests the startIdx boundary condition.
func TestStartIdxBoundary(t *testing.T) {
	// Test with different lag values to see when startIdx goes negative

	testCases := []struct {
		name         string
		fsKHz        int
		ltpMemLength int
		lpcOrder     int
		lag          int
	}{
		{"NB low pitch", 8, 160, 10, 80},    // Normal case
		{"NB high pitch", 8, 160, 10, 150},  // Close to limit
		{"NB max pitch", 8, 160, 10, 160},   // At limit
		{"WB low pitch", 16, 320, 16, 100},  // Normal case
		{"WB high pitch", 16, 320, 16, 310}, // Close to limit
	}

	ltpOrder := 5
	for _, tc := range testCases {
		startIdx := tc.ltpMemLength - tc.lag - tc.lpcOrder - ltpOrder/2
		clampedIdx := startIdx
		if clampedIdx < 0 {
			clampedIdx = 0
		}

		// Calculate the difference in LPC analysis filter input position
		// For k=2, the input offset is startIdx + 2*subfrLength
		subfrLength := 5 * tc.fsKHz // 5ms in samples
		filterInputOffset := startIdx + 2*subfrLength

		t.Logf("%s: startIdx=%d (clamped=%d), filter input offset=%d",
			tc.name, startIdx, clampedIdx, filterInputOffset)
	}
}

func loadLagPackets(t *testing.T, bitFile string, maxPackets int) [][]byte {
	t.Helper()

	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Logf("Cannot read %s: %v", bitFile, err)
		return nil
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets
}
