package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
)

// TTSClient speaks to a Wyoming-protocol Piper TTS server.
type TTSClient struct {
	addr string
}

func NewTTSClient(addr string) *TTSClient {
	return &TTSClient{addr: addr}
}

// wyomingEvent is a single Wyoming protocol event.
// The wire format is:
//
//	JSON header line (with type, data_length, payload_length) + '\n'
//	data_length bytes of JSON metadata
//	payload_length bytes of binary payload
type wyomingEvent struct {
	Type    string
	Data    map[string]any // parsed from the data segment
	Payload []byte         // binary payload (e.g. PCM audio)
}

// Synthesize sends text to Piper and returns WAV audio.
func (t *TTSClient) Synthesize(text string) ([]byte, error) {
	conn, err := net.Dial("tcp", t.addr)
	if err != nil {
		return nil, fmt.Errorf("connect to piper: %w", err)
	}
	defer conn.Close()

	// Send synthesize event
	if err := writeEvent(conn, wyomingEvent{
		Type: "synthesize",
		Data: map[string]any{"text": text},
	}); err != nil {
		return nil, fmt.Errorf("send synthesize: %w", err)
	}

	// Read events until audio-stop
	var pcmBuf bytes.Buffer
	var sampleRate, sampleWidth, channels int

	for {
		evt, err := readEvent(conn)
		if err != nil {
			return nil, fmt.Errorf("read event: %w", err)
		}

		switch evt.Type {
		case "audio-start":
			if r, ok := evt.Data["rate"].(float64); ok {
				sampleRate = int(r)
			}
			if w, ok := evt.Data["width"].(float64); ok {
				sampleWidth = int(w)
			}
			if c, ok := evt.Data["channels"].(float64); ok {
				channels = int(c)
			}
			log.Printf("[tts] audio-start: rate=%d width=%d channels=%d", sampleRate, sampleWidth, channels)

		case "audio-chunk":
			pcmBuf.Write(evt.Payload)

		case "audio-stop":
			log.Printf("[tts] audio-stop: %d bytes PCM", pcmBuf.Len())
			if sampleRate == 0 {
				sampleRate = 22050
			}
			if sampleWidth == 0 {
				sampleWidth = 2
			}
			if channels == 0 {
				channels = 1
			}
			return pcmToWAV(pcmBuf.Bytes(), sampleRate, sampleWidth, channels), nil

		case "error":
			msg, _ := evt.Data["text"].(string)
			return nil, fmt.Errorf("piper error: %s", msg)
		}
	}
}

// writeEvent writes a Wyoming event to a connection.
func writeEvent(w io.Writer, evt wyomingEvent) error {
	var dataBytes []byte
	if evt.Data != nil {
		var err error
		dataBytes, err = json.Marshal(evt.Data)
		if err != nil {
			return err
		}
	}

	header := map[string]any{
		"type":           evt.Type,
		"version":        "1.8.0",
		"data_length":    len(dataBytes),
		"payload_length": len(evt.Payload),
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return err
	}
	headerBytes = append(headerBytes, '\n')

	if _, err := w.Write(headerBytes); err != nil {
		return err
	}
	if len(dataBytes) > 0 {
		if _, err := w.Write(dataBytes); err != nil {
			return err
		}
	}
	if len(evt.Payload) > 0 {
		if _, err := w.Write(evt.Payload); err != nil {
			return err
		}
	}
	return nil
}

// readEvent reads a Wyoming event from a connection.
func readEvent(r io.Reader) (wyomingEvent, error) {
	// Read JSON header line (terminated by newline)
	var lineBuf bytes.Buffer
	oneByte := make([]byte, 1)
	for {
		if _, err := r.Read(oneByte); err != nil {
			return wyomingEvent{}, err
		}
		if oneByte[0] == '\n' {
			break
		}
		lineBuf.WriteByte(oneByte[0])
	}

	var header struct {
		Type          string `json:"type"`
		DataLength    int    `json:"data_length"`
		PayloadLength int    `json:"payload_length"`
	}
	if err := json.Unmarshal(lineBuf.Bytes(), &header); err != nil {
		return wyomingEvent{}, fmt.Errorf("parse header: %w", err)
	}

	// Read data segment (JSON metadata)
	var data map[string]any
	if header.DataLength > 0 {
		dataBuf := make([]byte, header.DataLength)
		if _, err := io.ReadFull(r, dataBuf); err != nil {
			return wyomingEvent{}, fmt.Errorf("read data: %w", err)
		}
		if err := json.Unmarshal(dataBuf, &data); err != nil {
			return wyomingEvent{}, fmt.Errorf("parse data: %w", err)
		}
	}

	// Read binary payload
	var payload []byte
	if header.PayloadLength > 0 {
		payload = make([]byte, header.PayloadLength)
		if _, err := io.ReadFull(r, payload); err != nil {
			return wyomingEvent{}, fmt.Errorf("read payload: %w", err)
		}
	}

	return wyomingEvent{
		Type:    header.Type,
		Data:    data,
		Payload: payload,
	}, nil
}

// pcmToWAV wraps raw PCM data in a WAV header.
func pcmToWAV(pcm []byte, sampleRate, sampleWidth, channels int) []byte {
	dataLen := len(pcm)
	byteRate := sampleRate * channels * sampleWidth
	blockAlign := channels * sampleWidth

	var buf bytes.Buffer
	buf.Grow(44 + dataLen)

	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(sampleWidth*8))

	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(pcm)

	return buf.Bytes()
}
