package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// STTClient sends audio to a faster-whisper server and returns the transcript.
type STTClient struct {
	baseURL string
	http    *http.Client
}

func NewSTTClient(baseURL string) *STTClient {
	return &STTClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Transcribe converts raw 32-bit stereo PCM (16kHz) to 16-bit mono WAV,
// sends it to the Whisper API, and returns the transcript text.
func (s *STTClient) Transcribe(rawPCM []byte) (string, error) {
	wav := pcm32StereoToWAV16Mono(rawPCM, 16000)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(wav); err != nil {
		return "", fmt.Errorf("write wav: %w", err)
	}
	if err := w.WriteField("model", "base"); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequest("POST", s.baseURL+"/v1/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return result.Text, nil
}

// pcm32StereoToWAV16Mono converts 32-bit stereo interleaved PCM to a 16-bit
// mono WAV file. It extracts the left channel (ch0) and takes the upper 16
// bits of each 32-bit sample.
func pcm32StereoToWAV16Mono(raw []byte, sampleRate int) []byte {
	// Each stereo frame = 8 bytes (2 × 4-byte samples: left, right)
	frameSize := 8
	numFrames := len(raw) / frameSize

	// Extract left channel, convert 32-bit → 16-bit (take upper 16 bits)
	samples := make([]int16, numFrames)
	for i := 0; i < numFrames; i++ {
		offset := i * frameSize
		// Left channel is first 4 bytes of each frame (little-endian int32)
		sample32 := int32(binary.LittleEndian.Uint32(raw[offset : offset+4]))
		samples[i] = int16(sample32 >> 16)
	}

	// Build WAV file
	dataSize := numFrames * 2 // 16-bit = 2 bytes per sample
	var buf bytes.Buffer
	buf.Grow(44 + dataSize)

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))    // chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))     // PCM format
	binary.Write(&buf, binary.LittleEndian, uint16(1))     // mono
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(2))     // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))    // bits per sample

	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	binary.Write(&buf, binary.LittleEndian, samples)

	return buf.Bytes()
}
