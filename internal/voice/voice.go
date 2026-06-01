// Package voice is the runtime that wires the voice command pipeline
// together: MQTT audio in → STT → LLM routing → agent dispatch → TTS
// out. Construct a Runner with all dependencies, then call Run(ctx) to
// block until the context is cancelled.
//
// Speculative processing: when the VAD reports a pause (short silence),
// processing starts on the partial audio in the background. If a full
// utterance arrives soon after, the speculative result is used; if the
// speech continued, the speculative work is cancelled.
package voice

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/openai/openai-go"

	"github.com/jcgregorio/myjarvis/internal/agent"
	"github.com/jcgregorio/myjarvis/internal/ha"
	"github.com/jcgregorio/myjarvis/internal/llm"
	"github.com/jcgregorio/myjarvis/internal/obsidian/lists"
	"github.com/jcgregorio/myjarvis/internal/obsidian/property"
	"github.com/jcgregorio/myjarvis/internal/tts"
	"github.com/jcgregorio/myjarvis/internal/voice/audio"
	"github.com/jcgregorio/myjarvis/internal/voice/mqtt"
	"github.com/jcgregorio/myjarvis/internal/voice/stt"
)

// Deps bundles the dependencies voice.Runner drives. Concrete types
// today; introduce interfaces only when a unit test needs to fake one
// out (the extraction itself unlocks that — none of this could be
// unit-tested when it lived inline in main()).
type Deps struct {
	HA           *ha.Client
	LLM          *llm.Client
	Dispatcher   *agent.Dispatcher
	STT          *stt.Client
	TTS          *tts.Client
	AudioServer  *tts.AudioServer
	Router       *audio.Router
	MQTT         *mqtt.Client
	InitialTools []openai.ChatCompletionToolParam
	// RefreshInterval, if non-zero, runs a goroutine that periodically
	// refetches HA entities + lists + properties and rebuilds the tool
	// set under lock.
	RefreshInterval time.Duration
}

// Runner is the voice pipeline runtime. Hold the current tool set under
// a RWMutex so the refresh loop and request handlers can race safely.
type Runner struct {
	deps    Deps
	toolsMu sync.RWMutex
	tools   []openai.ChatCompletionToolParam
	specMu  sync.Mutex
	spec    map[string]*specState
}

// New constructs a Runner; doesn't start anything until Run is called.
func New(deps Deps) *Runner {
	return &Runner{
		deps:  deps,
		tools: deps.InitialTools,
		spec:  make(map[string]*specState),
	}
}

// Tools returns a snapshot of the current tool set under read lock.
// Used by the CLI loop in cmd/myjarvis to route typed input.
func (r *Runner) Tools() []openai.ChatCompletionToolParam {
	r.toolsMu.RLock()
	defer r.toolsMu.RUnlock()
	return r.tools
}

// Run wires the audio router callbacks, starts the refresh ticker if
// configured, and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if r.deps.Router != nil && r.deps.MQTT != nil {
		r.deps.Router.OnSpeechEnd = func(device string) {
			r.deps.MQTT.PublishStopStreaming(device)
		}
		r.deps.Router.OnPause = r.handlePause
		r.deps.Router.OnComplete = r.handleComplete
	}
	if r.deps.RefreshInterval > 0 {
		go r.refreshLoop(ctx)
	}
	<-ctx.Done()
}

type voiceResult struct {
	transcript string
	toolCalls  []ha.ToolCall
	reply      string
	err        error
}

type specState struct {
	cancel context.CancelFunc
	result chan *voiceResult
}

func (r *Runner) processVoice(ctx context.Context, device string, audioBytes []byte, speculative bool) *voiceResult {
	label := "[voice]"
	if speculative {
		label = "[voice/spec]"
	}

	start := time.Now()
	transcript, err := r.deps.STT.Transcribe(audioBytes)
	if err != nil {
		return &voiceResult{err: fmt.Errorf("STT error: %w", err)}
	}
	if ctx.Err() != nil {
		log.Printf("%s %s: cancelled after STT", label, device)
		return &voiceResult{err: ctx.Err()}
	}
	log.Printf("%s %s: \"%s\" (%dms)", label, device, transcript, time.Since(start).Milliseconds())

	if transcript == "" {
		return &voiceResult{}
	}

	var hasWakeWord bool = false
	transcript, hasWakeWord = StartsWithWakeWord(transcript)
	if !hasWakeWord {
		log.Printf("[voice/no-wake-word-in-transcript] %s: %q", device, transcript)
		return &voiceResult{}
	}

	toolCalls, reply, err := r.deps.LLM.Chat(ctx, transcript, r.Tools())
	if err != nil {
		return &voiceResult{transcript: transcript, err: fmt.Errorf("LLM error: %w", err)}
	}
	return &voiceResult{transcript: transcript, toolCalls: toolCalls, reply: reply}
}

func (r *Runner) executeResult(device string, vr *voiceResult) {
	if vr.err != nil {
		log.Printf("[voice] %s: %v", device, vr.err)
		r.deps.MQTT.SignalError(device)
		return
	}
	if vr.transcript == "" {
		r.deps.MQTT.PublishLED(device, "off")
		return
	}
	log.Printf("[voice]: About to check for stop command in %s", vr.transcript)
	if IsStopCommand(vr.transcript) {
		log.Printf("[voice] %s: stop command received", device)
		r.deps.MQTT.PublishStopPlayback(device)
		r.deps.MQTT.PublishLED(device, "off")
		return
	}
	if len(vr.toolCalls) == 0 {
		log.Printf("[voice] %s: LLM reply (no tool call): %s", device, vr.reply)
		r.speak(device, "Sorry, I don't know how to do that.")
		return
	}

	hadError := false
	for _, tc := range vr.toolCalls {
		log.Printf("[voice] %s: → %s(%s)", device, tc.Name, tc.Args)
		result, err := r.deps.Dispatcher.Execute(context.Background(), tc)
		if err != nil {
			log.Printf("[voice] %s:   error: %v", device, err)
			hadError = true
		} else if result != "" {
			log.Printf("[voice] %s:   result: %s", device, result)
			r.speak(device, result)
		} else {
			log.Printf("[voice] %s:   done", device)
		}
	}
	if hadError {
		r.deps.MQTT.SignalError(device)
	} else {
		r.deps.MQTT.PublishLED(device, "off")
	}
}

func (r *Runner) handlePause(device string, audioBytes []byte) {
	r.specMu.Lock()
	// Cancel any prior speculative work for this device.
	if s, ok := r.spec[device]; ok {
		s.cancel()
		delete(r.spec, device)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan *voiceResult, 1)
	r.spec[device] = &specState{cancel: cancel, result: ch}
	r.specMu.Unlock()

	log.Printf("[voice] %s: pause detected, starting speculative processing (%d bytes)", device, len(audioBytes))
	r.deps.MQTT.PublishLED(device, "thinking")

	go func() {
		vr := r.processVoice(ctx, device, audioBytes, true)
		select {
		case ch <- vr:
		default:
		}
	}()
}

func (r *Runner) handleComplete(device string, audioBytes []byte) {
	r.specMu.Lock()
	s := r.spec[device]
	delete(r.spec, device)
	r.specMu.Unlock()

	if s != nil {
		select {
		case vr := <-s.result:
			s.cancel()
			if vr.err == nil && vr.transcript != "" {
				log.Printf("[voice] %s: using speculative result", device)
				r.executeResult(device, vr)
				return
			}
			log.Printf("[voice] %s: speculative result unusable, reprocessing full audio", device)
		default:
			s.cancel()
			log.Printf("[voice] %s: speculative processing cancelled, using full audio", device)
		}
	}

	log.Printf("[voice] %s: received %d bytes of audio, transcribing...", device, len(audioBytes))
	r.deps.MQTT.PublishLED(device, "thinking")
	vr := r.processVoice(context.Background(), device, audioBytes, false)
	r.executeResult(device, vr)
}

func (r *Runner) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(r.deps.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updated, err := r.deps.HA.FetchControllableEntities(ctx)
			if err != nil {
				log.Printf("entity refresh failed: %v", err)
				continue
			}
			refreshedLists, err := lists.Fetch()
			if err != nil {
				log.Printf("list refresh failed: %v", err)
			}
			refreshedProperties, err := property.Fetch()
			if err != nil {
				log.Printf("property refresh failed: %v", err)
			}
			r.toolsMu.Lock()
			r.tools = llm.BuildTools(updated, refreshedLists, refreshedProperties)
			r.toolsMu.Unlock()
			log.Printf("refreshed %d entities, %d lists, %d properties",
				len(updated), len(refreshedLists), len(refreshedProperties))
		}
	}
}

func (r *Runner) speak(device, text string) {
	wav, err := r.deps.TTS.Synthesize(text)
	if err != nil {
		log.Printf("[tts] synthesize error: %v", err)
		return
	}
	url, err := r.deps.AudioServer.Store(wav)
	if err != nil {
		log.Printf("[tts] store error: %v", err)
		return
	}
	log.Printf("[tts] %s: playing %s", device, url)
	if err := r.deps.MQTT.PublishTTSURL(device, url); err != nil {
		log.Printf("[tts] publish error: %v", err)
	}
}

var heyJarvisRE = regexp.MustCompile(`(?i)^(h(?:ey|i),?\sjarvis)`)

// StartsWithWakeWord returns true if the transcript contains "hey, jarvis"
// (case-insensitive). Used as a server-side check that microWakeWord
// didn't fire on noise — the device prepends ~1.5 s of pre-wake-word
// audio, so a real wake event produces a transcript with the word in it.
func StartsWithWakeWord(transcript string) (string, bool) {
	match := heyJarvisRE.FindString(transcript)
	if match != "" {
		return transcript[len(match):], true
	}
	return transcript, false
}

var stopCmdRE = regexp.MustCompile(`(?i)^[\s.,]*(?:stop|cancel|shut up|be quiet|quiet|nevermind|never mind)[\s.,]*$`)

// IsStopCommand returns true if the transcript is a stop/cancel command.
// Exported so cmd/myjarvis's interactive CLI can use the same check.
func IsStopCommand(transcript string) bool {
	return stopCmdRE.MatchString(transcript)
}
