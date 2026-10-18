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
//
// Each device gets its own VAD processor (created lazily, reused across sessions)
// so simultaneous utterances from different devices do not interfere.
//
// DeviceToMono maps device names to a PCM→mono-float32 converter. Devices not
// listed use the default 32-bit stereo path (vad.Append). Set this before
// calling AppendAudio to support devices with non-standard audio formats.
type Router struct {
	mu           sync.Mutex
	sessions     map[string]*Session        // device name → active session
	vadProcs     map[string]*vad.Processor  // per-device VAD processors
	newVAD       func() (*vad.Processor, error)
	DeviceToMono map[string]func([]byte) []float32
	OnComplete   func(device string, audio []byte)
	OnSpeechEnd  func(device string)
	OnPause      func(device string, audio []byte)
}

// NewRouter creates a Router. newVAD is called once per device to create its
// dedicated VAD processor; it is typically vad.NewProcessor(modelPath).
func NewRouter(newVAD func() (*vad.Processor, error)) *Router {
	return &Router{
		sessions: make(map[string]*Session),
		vadProcs: make(map[string]*vad.Processor),
		newVAD:   newVAD,
	}
}

// Destroy releases all per-device VAD processors. Call when shutting down.
func (r *Router) Destroy() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, proc := range r.vadProcs {
		proc.Destroy()
	}
	r.vadProcs = make(map[string]*vad.Processor)
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
	// Reset (or lazily create) this device's dedicated VAD processor.
	if r.newVAD != nil {
		proc := r.vadProcs[device]
		if proc == nil {
			var err error
			proc, err = r.newVAD()
			if err != nil {
				log.Printf("[audio] %s: failed to create VAD: %v", device, err)
			} else {
				r.vadProcs[device] = proc
			}
		} else {
			proc.Reset()
		}
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

	// Run VAD on the chunk using this device's dedicated processor.
	r.mu.Lock()
	proc := r.vadProcs[device]
	r.mu.Unlock()
	if proc != nil {
		var vadResult vad.Result
		if fn, ok := r.DeviceToMono[device]; ok {
			vadResult = proc.AppendMono(fn(data))
		} else {
			vadResult = proc.Append(data)
		}
		switch vadResult {
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
