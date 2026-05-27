package vad

import (
	"encoding/binary"
	"log"
	"math"
	"sync"

	speech "github.com/streamer45/silero-vad-go/speech"
)

// Result indicates the outcome of a VAD Append call.
type Result int

const (
	None  Result = iota // no state change
	Pause                  // short pause detected — speculative processing can start
	Done                   // final end of speech — commit to processing
)

// Processor wraps two Silero VAD detectors for streaming audio detection.
// A "pause" detector (short silence threshold) fires early to enable speculative
// processing, while the "done" detector (longer silence threshold) signals the
// definitive end of speech.
//
// After Done, the buffer is cleared so the next speech cycle starts fresh.
type Processor struct {
	pauseDet *speech.Detector // short silence (500ms) — fires Pause
	doneDet  *speech.Detector // long silence (1500ms) — fires Done
	mu       sync.Mutex
	pcm      []float32 // sliding window of mono float32 samples
	spoken   bool      // true once speech has been detected
	paused   bool      // true after Pause fired (suppresses duplicate pause events)
}

// NewProcessor creates a new Silero VAD processor with two detectors.
func NewProcessor(modelPath string) (*Processor, error) {
	pauseDet, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:            modelPath,
		SampleRate:           16000,
		Threshold:            0.5,
		MinSilenceDurationMs: 500,
		SpeechPadMs:          100,
		LogLevel:             speech.LogLevelWarn,
	})
	if err != nil {
		return nil, err
	}

	doneDet, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:            modelPath,
		SampleRate:           16000,
		Threshold:            0.5,
		MinSilenceDurationMs: 1500,
		SpeechPadMs:          100,
		LogLevel:             speech.LogLevelWarn,
	})
	if err != nil {
		pauseDet.Destroy()
		return nil, err
	}

	return &Processor{pauseDet: pauseDet, doneDet: doneDet}, nil
}

// Reset clears accumulated samples and resets detector state.
func (v *Processor) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pcm = v.pcm[:0]
	v.spoken = false
	v.paused = false
	v.pauseDet.Reset()
	v.doneDet.Reset()
}

// Append converts a 32-bit stereo PCM chunk to mono float32, appends it, and
// checks for speech pauses and end. Returns Pause on the first short pause
// after speech, Done on final end of speech, or None otherwise.
//
// Detection runs on a sliding window (last 5 seconds) each cycle. Both
// detectors are reset before each call so they process the window from
// scratch — necessary because silence duration is measured within a single
// Detect call.
func (v *Processor) Append(chunk []byte) Result {
	mono := pcm32StereoToFloat32Mono(chunk)

	v.mu.Lock()
	v.pcm = append(v.pcm, mono...)
	totalSamples := len(v.pcm)
	v.mu.Unlock()

	// Only run detection every ~250ms worth of new audio (4096 samples at 16kHz)
	// and need at least 1024 samples
	if totalSamples < 1024 || totalSamples%4096 > len(mono) {
		return None
	}

	v.mu.Lock()
	// Sliding window: keep last 5 seconds (80000 samples at 16kHz).
	const windowSamples = 80000
	start := 0
	if len(v.pcm) > windowSamples {
		start = len(v.pcm) - windowSamples
	}
	buf := make([]float32, len(v.pcm)-start)
	copy(buf, v.pcm[start:])
	v.mu.Unlock()

	// Run both detectors on the same window
	v.pauseDet.Reset()
	pauseSegs, pauseErr := v.pauseDet.Detect(buf)
	if pauseErr != nil {
		log.Printf("[vad] pause detect error: %v", pauseErr)
	}

	v.doneDet.Reset()
	doneSegs, doneErr := v.doneDet.Detect(buf)
	if doneErr != nil {
		log.Printf("[vad] done detect error: %v", doneErr)
	}

	// Track speech start from either detector
	for _, seg := range pauseSegs {
		if seg.SpeechStartAt >= 0 {
			v.mu.Lock()
			v.spoken = true
			v.mu.Unlock()
		}
	}
	for _, seg := range doneSegs {
		if seg.SpeechStartAt >= 0 {
			v.mu.Lock()
			v.spoken = true
			v.mu.Unlock()
		}
	}

	v.mu.Lock()
	spoken := v.spoken
	paused := v.paused
	v.mu.Unlock()

	if !spoken {
		return None
	}

	// Check done detector first (takes priority over pause)
	for _, seg := range doneSegs {
		if seg.SpeechEndAt > 0 {
			log.Printf("[vad] speech ended at %.2fs", seg.SpeechEndAt)
			v.mu.Lock()
			v.pcm = v.pcm[:0]
			v.spoken = false
			v.paused = false
			v.mu.Unlock()
			return Done
		}
	}

	// Check pause detector — only fire once per speech segment
	if !paused {
		for _, seg := range pauseSegs {
			if seg.SpeechEndAt > 0 {
				log.Printf("[vad] pause detected at %.2fs", seg.SpeechEndAt)
				v.mu.Lock()
				v.paused = true
				v.mu.Unlock()
				return Pause
			}
		}
	}

	return None
}

// Destroy releases the detector resources.
func (v *Processor) Destroy() {
	if v.pauseDet != nil {
		v.pauseDet.Destroy()
	}
	if v.doneDet != nil {
		v.doneDet.Destroy()
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
