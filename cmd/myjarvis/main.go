package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/jcgregorio/myjarvis/internal/config"
	"github.com/jcgregorio/myjarvis/internal/ha"
	"github.com/jcgregorio/myjarvis/internal/obsidian"
	"github.com/jcgregorio/myjarvis/internal/obsidian/lists"
	"github.com/jcgregorio/myjarvis/internal/obsidian/property"
	"github.com/jcgregorio/myjarvis/internal/llm"
	"github.com/jcgregorio/myjarvis/internal/rag"
	"github.com/jcgregorio/myjarvis/internal/agent"
	"github.com/jcgregorio/myjarvis/internal/tts"
	"github.com/jcgregorio/myjarvis/internal/voice/stt"
	"github.com/jcgregorio/myjarvis/internal/voice/vad"
	"github.com/jcgregorio/myjarvis/internal/voice/audio"
	"github.com/jcgregorio/myjarvis/internal/voice/mqtt"
)

// applyConfig propagates any cross-package side effects of the loaded
// config (currently: pointing the obsidian-vault package vars at a
// non-default location).
func applyConfig(cfg config.Config) {
	if cfg.ObsidianRepo != "" {
		obsidian.SetRepo(cfg.ObsidianRepo)
		lists.SetDir(cfg.ObsidianRepo + "/Lists")
		property.SetDir(cfg.ObsidianRepo + "/Properties")
	}
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "list":
			runList()
			return
		case "tools":
			runTools()
			return
		}
	}

	dryRun := flag.Bool("dry-run", false, "Print tool calls without executing them against Home Assistant")
	flag.Parse()

	cfg := config.FromEnv()
	applyConfig(cfg)

	hc := ha.NewClient(cfg.HAURL, cfg.HAToken)

	fmt.Println("Fetching Home Assistant entities...")
	entities, err := hc.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}
	fmt.Printf("Found %d controllable entities.\n", len(entities))

	lc := llm.NewClient(cfg.OllamaURL, cfg.Model)

	rs := rag.NewSearcher(rag.NewClient(cfg.RAGURL), lc)
	dispatcher := agent.New(hc, rs)

	listNames, err := lists.Fetch()
	if err != nil {
		log.Printf("Failed to fetch lists (continuing without): %v", err)
	}
	fmt.Printf("Found %d lists.\n", len(listNames))

	propertyNames, err := property.Fetch()
	if err != nil {
		log.Printf("Failed to fetch properties (continuing without): %v", err)
	}
	fmt.Printf("Found %d properties.\n", len(propertyNames))

	var toolsMu sync.RWMutex
	tools := llm.BuildTools(entities, listNames, propertyNames)

	sttc := stt.NewClient(cfg.WhisperURL)

	ttsc := tts.NewClient(cfg.PiperAddr)

	audioSrv, err := tts.NewAudioServer(cfg.AudioPort)
	if err != nil {
		log.Fatalf("Failed to start audio server: %v", err)
	}

	// Initialize Silero VAD
	vadProc, err := vad.NewProcessor(cfg.VADModelPath)
	if err != nil {
		log.Fatalf("Failed to initialize VAD: %v", err)
	}
	defer vadProc.Destroy()

	// Start voice MQTT subscriber
	router := audio.NewRouter(vadProc)
	voiceMQTT, err := mqtt.NewClient(context.Background(), cfg.MQTTBroker, router)
	if err != nil {
		log.Printf("MQTT connection failed (voice input disabled): %v", err)
	} else {
		fmt.Println("Voice MQTT subscriber started.")
	}
	// processVoice handles the full STT → LLM → execute pipeline for a device.
	// It respects context cancellation at each stage.
	type voiceResult struct {
		transcript string
		toolCalls  []ha.ToolCall
		reply      string
		err        error
	}

	processVoice := func(ctx context.Context, device string, audio []byte, speculative bool) *voiceResult {
		label := "[voice]"
		if speculative {
			label = "[voice/spec]"
		}

		start := time.Now()
		transcript, err := sttc.Transcribe(audio)
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

		toolsMu.RLock()
		currentTools := tools
		toolsMu.RUnlock()

		toolCalls, reply, err := lc.Chat(ctx, transcript, currentTools)
		if err != nil {
			return &voiceResult{transcript: transcript, err: fmt.Errorf("LLM error: %w", err)}
		}

		return &voiceResult{transcript: transcript, toolCalls: toolCalls, reply: reply}
	}

	// executeResult handles tool execution and TTS for a voice result.
	executeResult := func(device string, vr *voiceResult) {
		if vr.err != nil {
			log.Printf("[voice] %s: %v", device, vr.err)
			voiceMQTT.SignalError(device)
			return
		}
		if vr.transcript == "" {
			voiceMQTT.PublishLED(device, "off")
			return
		}
		if isStopCommand(vr.transcript) {
			log.Printf("[voice] %s: stop command received", device)
			voiceMQTT.PublishStopPlayback(device)
			voiceMQTT.PublishLED(device, "off")
			return
		}
		if len(vr.toolCalls) == 0 {
			log.Printf("[voice] %s: LLM reply (no tool call): %s", device, vr.reply)
			speakToDevice(device, "Sorry, I don't know how to do that.", ttsc, audioSrv, voiceMQTT)
			return
		}

		hadError := false
		for _, tc := range vr.toolCalls {
			log.Printf("[voice] %s: → %s(%s)", device, tc.Name, tc.Args)
			result, err := dispatcher.Execute(context.Background(), tc)
			if err != nil {
				log.Printf("[voice] %s:   error: %v", device, err)
				hadError = true
			} else if result != "" {
				log.Printf("[voice] %s:   result: %s", device, result)
				speakToDevice(device, result, ttsc, audioSrv, voiceMQTT)
			} else {
				log.Printf("[voice] %s:   done", device)
			}
		}
		if hadError {
			voiceMQTT.SignalError(device)
		} else {
			voiceMQTT.PublishLED(device, "off")
		}
	}

	// Speculative processing state per device
	var specMu sync.Mutex
	type specState struct {
		cancel context.CancelFunc
		result chan *voiceResult
	}
	speculative := make(map[string]*specState)

	router.OnSpeechEnd = func(device string) {
		voiceMQTT.PublishStopStreaming(device)
	}

	router.OnPause = func(device string, audio []byte) {
		specMu.Lock()
		// Cancel any prior speculative work for this device
		if s, ok := speculative[device]; ok {
			s.cancel()
			delete(speculative, device)
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan *voiceResult, 1)
		speculative[device] = &specState{cancel: cancel, result: ch}
		specMu.Unlock()

		log.Printf("[voice] %s: pause detected, starting speculative processing (%d bytes)", device, len(audio))
		voiceMQTT.PublishLED(device, "thinking")

		go func() {
			vr := processVoice(ctx, device, audio, true)
			select {
			case ch <- vr:
			default:
			}
		}()
	}

	router.OnComplete = func(device string, audio []byte) {
		// Check if speculative processing already completed
		specMu.Lock()
		s := speculative[device]
		delete(speculative, device)
		specMu.Unlock()

		if s != nil {
			// Wait briefly for speculative result — it may already be done
			select {
			case vr := <-s.result:
				s.cancel()
				if vr.err == nil && vr.transcript != "" {
					log.Printf("[voice] %s: using speculative result", device)
					executeResult(device, vr)
					return
				}
				log.Printf("[voice] %s: speculative result unusable, reprocessing full audio", device)
			default:
				// Speculative work still in progress — cancel it
				s.cancel()
				log.Printf("[voice] %s: speculative processing cancelled, using full audio", device)
			}
		}

		// Full processing with complete audio
		log.Printf("[voice] %s: received %d bytes of audio, transcribing...", device, len(audio))
		voiceMQTT.PublishLED(device, "thinking")
		vr := processVoice(context.Background(), device, audio, false)
		executeResult(device, vr)
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			updated, err := hc.FetchControllableEntities(context.Background())
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
			toolsMu.Lock()
			tools = llm.BuildTools(updated, refreshedLists, refreshedProperties)
			toolsMu.Unlock()
			log.Printf("refreshed %d entities, %d lists", len(updated), len(refreshedLists))
		}
	}()

	if *dryRun {
		fmt.Println("(dry-run mode: tool calls will be printed but not executed)")
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		// Interactive CLI mode
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Printf("\nJarvis ready (model: %s). Type a command (Ctrl+D to quit):\n", cfg.Model)
		for {
			fmt.Print("> ")
			if !scanner.Scan() {
				break
			}
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}

			toolsMu.RLock()
			currentTools := tools
			toolsMu.RUnlock()

			start := time.Now()
			toolCalls, reply, err := lc.Chat(context.Background(), input, currentTools)
			elapsed := time.Since(start)
			if err != nil {
				fmt.Printf("LLM error: %v\n", err)
				continue
			}
			fmt.Printf("  [%dms]\n", elapsed.Milliseconds())

			if len(toolCalls) == 0 {
				fmt.Printf("Jarvis: %s\n", reply)
				continue
			}

			for _, tc := range toolCalls {
				fmt.Printf("→ %s(%s)\n", tc.Name, tc.Args)
				if *dryRun {
					fmt.Println("  (skipped — dry-run)")
					continue
				}
				result, err := dispatcher.Execute(context.Background(), tc)
				if err != nil {
					fmt.Printf("  error: %v\n", err)
				} else if result != "" {
					fmt.Printf("Jarvis: %s\n", result)
				} else {
					fmt.Printf("  done.\n")
				}
			}
		}
	} else {
		// Service mode — no stdin, block until signal
		fmt.Printf("Jarvis running as service (model: %s). Voice pipeline active.\n", cfg.Model)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Println("Shutting down.")
	}
}

func runTools() {
	cfg := config.FromEnv()
	applyConfig(cfg)
	hc := ha.NewClient(cfg.HAURL, cfg.HAToken)

	entities, err := hc.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}

	listNames, _ := lists.Fetch()
	propertyNames, _ := property.Fetch()
	tools := llm.BuildTools(entities, listNames, propertyNames)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tools); err != nil {
		log.Fatalf("Failed to encode tools: %v", err)
	}
}

func runList() {
	cfg := config.FromEnv()
	applyConfig(cfg)
	hc := ha.NewClient(cfg.HAURL, cfg.HAToken)

	entities, err := hc.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}

	// Group by domain
	byDomain := make(map[string][]ha.Entity)
	var domains []string
	for _, e := range entities {
		domain, _, _ := strings.Cut(e.EntityID, ".")
		if _, seen := byDomain[domain]; !seen {
			domains = append(domains, domain)
		}
		byDomain[domain] = append(byDomain[domain], e)
	}

	fmt.Printf("%-40s  %-20s  %s\n", "ENTITY ID", "FRIENDLY NAME", "STATE")
	fmt.Println(strings.Repeat("-", 80))
	for _, domain := range domains {
		for _, e := range byDomain[domain] {
			name := e.FriendlyName()
			fmt.Printf("%-40s  %-20s  %s\n", e.EntityID, name, e.State)
		}
	}
	fmt.Printf("\n%d controllable entities.\n", len(entities))
}


// isStopCommand returns true if the transcript is a stop/cancel command.
func isStopCommand(transcript string) bool {
	t := strings.ToLower(strings.TrimSpace(transcript))
	switch t {
	case "stop", "stop.", "cancel", "cancel.", "shut up", "shut up.",
		"be quiet", "be quiet.", "quiet", "quiet.",
		"never mind", "never mind.", "nevermind", "nevermind.":
		return true
	}
	return false
}

// speakToDevice synthesizes text via Piper TTS and sends the audio URL to the device.
func speakToDevice(device, text string, ttsc *tts.Client, audioSrv *tts.AudioServer, mc *mqtt.Client) {
	wav, err := ttsc.Synthesize(text)
	if err != nil {
		log.Printf("[tts] synthesize error: %v", err)
		return
	}
	url, err := audioSrv.Store(wav)
	if err != nil {
		log.Printf("[tts] store error: %v", err)
		return
	}
	log.Printf("[tts] %s: playing %s", device, url)
	if err := mc.PublishTTSURL(device, url); err != nil {
		log.Printf("[tts] publish error: %v", err)
	}
}
