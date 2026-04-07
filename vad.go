package main

import (
	"encoding/binary"
	"log"
	"math"
	"sync"

	speech "github.com/streamer45/silero-vad-go/speech"
)

// VADProcessor wraps Silero VAD for streaming audio detection.
// It accumulates float32 PCM samples and periodically runs detection
// on a sliding window to find speech end events.
type VADProcessor struct {
	detector *speech.Detector
	mu       sync.Mutex
	pcm      []float32 // accumulated mono float32 samples
	spoken   bool      // true once speech has been detected in any window
}

// NewVADProcessor creates a new Silero VAD processor.
func NewVADProcessor(modelPath string) (*VADProcessor, error) {
	det, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:            modelPath,
		SampleRate:           16000,
		Threshold:            0.5,
		MinSilenceDurationMs: 800,
		SpeechPadMs:          100,
		LogLevel:             speech.LogLevelWarn,
	})
	if err != nil {
		return nil, err
	}
	return &VADProcessor{detector: det}, nil
}

// Reset clears accumulated samples and resets the detector state.
func (v *VADProcessor) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pcm = v.pcm[:0]
	v.spoken = false
	v.detector.Reset()
}

// Append converts a 32-bit stereo PCM chunk to mono float32,
// appends it, and checks for speech end. Returns true if speech
// was detected and then silence of 800ms+ followed.
func (v *VADProcessor) Append(chunk []byte) bool {
	mono := pcm32StereoToFloat32Mono(chunk)

	v.mu.Lock()
	v.pcm = append(v.pcm, mono...)
	totalSamples := len(v.pcm)
	v.mu.Unlock()

	// Only run detection every ~250ms worth of new audio (4096 samples at 16kHz)
	// and need at least 1024 samples
	if totalSamples < 1024 || totalSamples%4096 > len(mono) {
		return false
	}

	v.mu.Lock()
	// Use a sliding window: last 3 seconds of audio
	const windowSamples = 16000 * 3 // 3 seconds at 16kHz
	start := 0
	if len(v.pcm) > windowSamples {
		start = len(v.pcm) - windowSamples
	}
	buf := make([]float32, len(v.pcm)-start)
	copy(buf, v.pcm[start:])
	v.mu.Unlock()

	v.detector.Reset()
	segments, err := v.detector.Detect(buf)
	if err != nil {
		log.Printf("[vad] detect error: %v", err)
		return false
	}

	for _, seg := range segments {
		if seg.SpeechStartAt >= 0 {
			v.mu.Lock()
			v.spoken = true
			v.mu.Unlock()
		}
		if seg.SpeechEndAt > 0 {
			v.mu.Lock()
			spoken := v.spoken
			v.mu.Unlock()
			if spoken {
				log.Printf("[vad] speech ended at %.2fs (window)", seg.SpeechEndAt)
				return true
			}
		}
	}

	return false
}

// Destroy releases the detector resources.
func (v *VADProcessor) Destroy() {
	if v.detector != nil {
		v.detector.Destroy()
	}
}

// pcm32StereoToFloat32Mono converts 32-bit stereo interleaved PCM (little-endian)
// to mono float32 samples normalized to [-1.0, 1.0]. Extracts left channel only.
func pcm32StereoToFloat32Mono(raw []byte) []float32 {
	frameSize := 8 // 2 channels × 4 bytes
	numFrames := len(raw) / frameSize
	samples := make([]float32, numFrames)
	for i := range numFrames {
		offset := i * frameSize
		sample32 := int32(binary.LittleEndian.Uint32(raw[offset : offset+4]))
		samples[i] = float32(sample32) / float32(math.MaxInt32)
	}
	return samples
}
