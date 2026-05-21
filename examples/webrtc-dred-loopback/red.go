package main

import "errors"

const (
	redMimeType        = "audio/red"
	redPayloadType     = 63
	redOpusPayloadType = 111
	maxREDDepth        = 5
)

type redHistoryFrame struct {
	timestamp uint32
	payload   []byte
}

type redBlock struct {
	payloadType     byte
	timestampOffset int
	payload         []byte
}

func rememberREDFrame(history []redHistoryFrame, payload []byte, timestamp uint32) []redHistoryFrame {
	if len(payload) == 0 {
		return history
	}
	frame := redHistoryFrame{
		timestamp: timestamp,
		payload:   append([]byte(nil), payload...),
	}
	history = append([]redHistoryFrame{frame}, history...)
	if len(history) > maxREDDepth {
		history = history[:maxREDDepth]
	}
	return history
}

func buildREDPayload(primary []byte, primaryTimestamp uint32, history []redHistoryFrame, depth int, frameSamples int) ([]byte, int) {
	if len(primary) == 0 {
		return append([]byte(nil), primary...), 0
	}
	if depth <= 0 {
		return append([]byte(nil), primary...), 0
	}
	if frameSamples <= 0 {
		return append([]byte{redOpusPayloadType}, primary...), 0
	}
	if depth > maxREDDepth {
		depth = maxREDDepth
	}
	blocks := make([]redBlock, 0, depth)
	for _, frame := range history {
		if len(blocks) == depth {
			break
		}
		offset := int(primaryTimestamp - frame.timestamp)
		if offset <= 0 || offset > 0x3fff || len(frame.payload) > 0x3ff {
			continue
		}
		if offset%frameSamples != 0 {
			continue
		}
		blocks = append(blocks, redBlock{
			payloadType:     redOpusPayloadType,
			timestampOffset: offset,
			payload:         frame.payload,
		})
	}
	if len(blocks) == 0 {
		return append([]byte{redOpusPayloadType}, primary...), 0
	}

	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
	}
	headerLen := len(blocks)*4 + 1
	payloadLen := len(primary)
	for _, block := range blocks {
		payloadLen += len(block.payload)
	}
	out := make([]byte, headerLen+payloadLen)
	pos := 0
	redundantBytes := 0
	for _, block := range blocks {
		blockLen := len(block.payload)
		offset := block.timestampOffset
		out[pos] = 0x80 | (block.payloadType & 0x7f)
		out[pos+1] = byte(offset >> 6)
		out[pos+2] = byte((offset&0x3f)<<2) | byte(blockLen>>8)
		out[pos+3] = byte(blockLen)
		pos += 4
		redundantBytes += blockLen
	}
	out[pos] = redOpusPayloadType
	pos++
	for _, block := range blocks {
		copy(out[pos:], block.payload)
		pos += len(block.payload)
	}
	copy(out[pos:], primary)
	return out, redundantBytes
}

func parseREDPayload(payload []byte) (primary []byte, blocks []redBlock, err error) {
	if len(payload) == 0 {
		return nil, nil, errors.New("empty RED payload")
	}
	type blockHeader struct {
		payloadType     byte
		timestampOffset int
		length          int
	}
	headers := make([]blockHeader, 0, maxREDDepth)
	pos := 0
	for {
		if pos >= len(payload) {
			return nil, nil, errors.New("truncated RED header")
		}
		b := payload[pos]
		if b&0x80 == 0 {
			if b&0x7f != redOpusPayloadType {
				return nil, nil, errors.New("unexpected RED primary payload type")
			}
			pos++
			break
		}
		if pos+4 > len(payload) {
			return nil, nil, errors.New("truncated RED redundant header")
		}
		offset := int(payload[pos+1])<<6 | int(payload[pos+2]>>2)
		length := (int(payload[pos+2]&0x03) << 8) | int(payload[pos+3])
		if offset <= 0 || length <= 0 {
			return nil, nil, errors.New("invalid RED redundant block")
		}
		payloadType := b & 0x7f
		if payloadType != redOpusPayloadType {
			return nil, nil, errors.New("unexpected RED redundant payload type")
		}
		if len(headers) == maxREDDepth {
			return nil, nil, errors.New("too many RED redundant blocks")
		}
		headers = append(headers, blockHeader{
			payloadType:     payloadType,
			timestampOffset: offset,
			length:          length,
		})
		pos += 4
	}
	blocks = make([]redBlock, 0, len(headers))
	for _, header := range headers {
		if pos+header.length > len(payload) {
			return nil, nil, errors.New("truncated RED redundant payload")
		}
		blocks = append(blocks, redBlock{
			payloadType:     header.payloadType,
			timestampOffset: header.timestampOffset,
			payload:         payload[pos : pos+header.length],
		})
		pos += header.length
	}
	if pos >= len(payload) {
		return nil, nil, errors.New("missing RED primary payload")
	}
	return payload[pos:], blocks, nil
}

func findREDRecoveryBlock(blocks []redBlock, lostAgo, frameSamples int, currentTimestamp, missingTimestamp uint32) []byte {
	if lostAgo <= 0 || frameSamples <= 0 {
		return nil
	}
	wantOffset := lostAgo * frameSamples
	if int(currentTimestamp-missingTimestamp) != wantOffset {
		return nil
	}
	for _, block := range blocks {
		if block.payloadType == redOpusPayloadType && block.timestampOffset == wantOffset {
			return block.payload
		}
	}
	return nil
}
