package main

import (
	"bytes"
	"log"
	"sync"
	"time"
)

// AudioSession accumulates raw PCM chunks for a single utterance from a device.
type AudioSession struct {
	Device    string
	StartedAt time.Time
	buf       bytes.Buffer
	mu        sync.Mutex
}

// Append adds a PCM chunk to the session buffer.
func (s *AudioSession) Append(data []byte) {
	s.mu.Lock()
	s.buf.Write(data)
	s.mu.Unlock()
}

// Bytes returns the accumulated audio data.
func (s *AudioSession) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Bytes()
}

// AudioRouter manages per-device audio sessions.
// When a session completes (via VAD or audio_stop), the OnComplete callback is called.
// OnSpeechEnd is called when VAD detects end of speech, allowing the caller to
// signal the device to stop streaming.
type AudioRouter struct {
	mu          sync.Mutex
	sessions    map[string]*AudioSession // device name → active session
	vad         *VADProcessor
	OnComplete  func(device string, audio []byte)
	OnSpeechEnd func(device string)
}

func NewAudioRouter(vad *VADProcessor) *AudioRouter {
	return &AudioRouter{
		sessions: make(map[string]*AudioSession),
		vad:      vad,
	}
}

// StartSession begins buffering audio for a device.
func (r *AudioRouter) StartSession(device string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[device]; exists {
		log.Printf("[audio] %s: replacing existing session", device)
	}
	r.sessions[device] = &AudioSession{
		Device:    device,
		StartedAt: time.Now(),
	}
	if r.vad != nil {
		r.vad.Reset()
	}
	log.Printf("[audio] %s: session started", device)
}

// AppendAudio adds a chunk to the active session for a device.
// If VAD detects end of speech, it automatically stops the session.
func (r *AudioRouter) AppendAudio(device string, data []byte) {
	r.mu.Lock()
	s := r.sessions[device]
	r.mu.Unlock()
	if s == nil {
		return // no active session — drop the chunk
	}
	s.Append(data)

	// Run VAD on the chunk
	if r.vad != nil && r.vad.Append(data) {
		log.Printf("[audio] %s: VAD detected end of speech", device)
		if r.OnSpeechEnd != nil {
			r.OnSpeechEnd(device)
		}
		r.completeSession(device)
	}
}

// StopSession ends the session for a device and invokes OnComplete.
// Called by the firmware's audio_stop (safety timeout).
func (r *AudioRouter) StopSession(device string) {
	r.completeSession(device)
}

func (r *AudioRouter) completeSession(device string) {
	r.mu.Lock()
	s := r.sessions[device]
	delete(r.sessions, device)
	r.mu.Unlock()
	if s == nil {
		return
	}
	audio := s.Bytes()
	elapsed := time.Since(s.StartedAt)
	log.Printf("[audio] %s: session complete — %d bytes, %.1fs", device, len(audio), elapsed.Seconds())
	if r.OnComplete != nil {
		r.OnComplete(device, audio)
	}
}
