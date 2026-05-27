package audio

import (
	"bytes"
	"log"
	"sync"
	"time"
	"github.com/jcgregorio/myjarvis/internal/voice/vad"
)

// Session accumulates raw PCM chunks for a single utterance from a device.
type Session struct {
	Device    string
	StartedAt time.Time
	buf       bytes.Buffer
	mu        sync.Mutex
}

// Append adds a PCM chunk to the session buffer.
func (s *Session) Append(data []byte) {
	s.mu.Lock()
	s.buf.Write(data)
	s.mu.Unlock()
}

// Bytes returns the accumulated audio data.
func (s *Session) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Bytes()
}

// Snapshot returns a copy of the accumulated audio data so far.
func (s *Session) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make([]byte, s.buf.Len())
	copy(snap, s.buf.Bytes())
	return snap
}

// Router manages per-device audio sessions.
// When a session completes (via VAD or audio_stop), the OnComplete callback is called.
// OnSpeechEnd is called when VAD detects end of speech, allowing the caller to
// signal the device to stop streaming.
// OnPause is called when VAD detects a short pause in speech, delivering a snapshot
// of audio so far for speculative processing.
type Router struct {
	mu          sync.Mutex
	sessions    map[string]*Session // device name → active session
	vad         *vad.Processor
	OnComplete  func(device string, audio []byte)
	OnSpeechEnd func(device string)
	OnPause     func(device string, audio []byte)
}

func NewRouter(vad *vad.Processor) *Router {
	return &Router{
		sessions: make(map[string]*Session),
		vad:      vad,
	}
}

// StartSession begins buffering audio for a device.
func (r *Router) StartSession(device string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[device]; exists {
		log.Printf("[audio] %s: replacing existing session", device)
	}
	r.sessions[device] = &Session{
		Device:    device,
		StartedAt: time.Now(),
	}
	if r.vad != nil {
		r.vad.Reset()
	}
	log.Printf("[audio] %s: session started", device)
}

// AppendAudio adds a chunk to the active session for a device.
// If VAD detects a pause, it fires OnPause with a snapshot.
// If VAD detects end of speech, it stops the session.
func (r *Router) AppendAudio(device string, data []byte) {
	r.mu.Lock()
	s := r.sessions[device]
	r.mu.Unlock()
	if s == nil {
		return // no active session — drop the chunk
	}
	s.Append(data)

	// Run VAD on the chunk
	if r.vad != nil {
		switch r.vad.Append(data) {
		case vad.Pause:
			log.Printf("[audio] %s: VAD detected pause in speech", device)
			if r.OnPause != nil {
				r.OnPause(device, s.Snapshot())
			}
		case vad.Done:
			log.Printf("[audio] %s: VAD detected end of speech", device)
			if r.OnSpeechEnd != nil {
				r.OnSpeechEnd(device)
			}
			r.completeSession(device)
		}
	}
}

// StopSession ends the session for a device and invokes OnComplete.
// Called by the firmware's audio_stop (safety timeout).
func (r *Router) StopSession(device string) {
	r.completeSession(device)
}

func (r *Router) completeSession(device string) {
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
