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
)

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

	cfg := configFromEnv()

	ha := NewHAClient(cfg.HAURL, cfg.HAToken)

	fmt.Println("Fetching Home Assistant entities...")
	entities, err := ha.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}
	fmt.Printf("Found %d controllable entities.\n", len(entities))

	llm := NewLLMClient(cfg.OllamaURL, cfg.Model)

	rag := NewRAGSearcher(NewRAGClient(cfg.RAGURL), llm)
	ha.SetRAG(rag)

	listNames, err := FetchLists()
	if err != nil {
		log.Printf("Failed to fetch lists (continuing without): %v", err)
	}
	fmt.Printf("Found %d lists.\n", len(listNames))

	var toolsMu sync.RWMutex
	tools := BuildTools(entities, listNames)

	stt := NewSTTClient(cfg.WhisperURL)

	tts := NewTTSClient(cfg.PiperAddr)

	audioSrv, err := NewAudioServer(cfg.AudioPort)
	if err != nil {
		log.Fatalf("Failed to start audio server: %v", err)
	}

	// Initialize Silero VAD
	vad, err := NewVADProcessor(cfg.VADModelPath)
	if err != nil {
		log.Fatalf("Failed to initialize VAD: %v", err)
	}
	defer vad.Destroy()

	// Start voice MQTT subscriber
	router := NewAudioRouter(vad)
	voiceMQTT, err := NewVoiceMQTTClient(context.Background(), cfg.MQTTBroker, router)
	if err != nil {
		log.Printf("MQTT connection failed (voice input disabled): %v", err)
	} else {
		fmt.Println("Voice MQTT subscriber started.")
	}
	// processVoice handles the full STT → LLM → execute pipeline for a device.
	// It respects context cancellation at each stage.
	type voiceResult struct {
		transcript string
		toolCalls  []ToolCall
		reply      string
		err        error
	}

	processVoice := func(ctx context.Context, device string, audio []byte, speculative bool) *voiceResult {
		label := "[voice]"
		if speculative {
			label = "[voice/spec]"
		}

		start := time.Now()
		transcript, err := stt.Transcribe(audio)
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

		toolCalls, reply, err := llm.Chat(ctx, transcript, currentTools)
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
			voiceMQTT.SignalError(device)
			return
		}

		hadError := false
		for _, tc := range vr.toolCalls {
			log.Printf("[voice] %s: → %s(%s)", device, tc.Name, tc.Args)
			result, err := ha.ExecuteToolCall(context.Background(), tc)
			if err != nil {
				log.Printf("[voice] %s:   error: %v", device, err)
				hadError = true
			} else if result != "" {
				log.Printf("[voice] %s:   result: %s", device, result)
				speakToDevice(device, result, tts, audioSrv, voiceMQTT)
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
			updated, err := ha.FetchControllableEntities(context.Background())
			if err != nil {
				log.Printf("entity refresh failed: %v", err)
				continue
			}
			refreshedLists, err := FetchLists()
			if err != nil {
				log.Printf("list refresh failed: %v", err)
			}
			toolsMu.Lock()
			tools = BuildTools(updated, refreshedLists)
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
			toolCalls, reply, err := llm.Chat(context.Background(), input, currentTools)
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
				result, err := ha.ExecuteToolCall(context.Background(), tc)
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
	cfg := configFromEnv()
	ha := NewHAClient(cfg.HAURL, cfg.HAToken)

	entities, err := ha.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}

	listNames, _ := FetchLists()
	tools := BuildTools(entities, listNames)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tools); err != nil {
		log.Fatalf("Failed to encode tools: %v", err)
	}
}

func runList() {
	cfg := configFromEnv()
	ha := NewHAClient(cfg.HAURL, cfg.HAToken)

	entities, err := ha.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}

	// Group by domain
	byDomain := make(map[string][]HAEntity)
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

type Config struct {
	HAURL        string
	HAToken      string
	OllamaURL    string
	Model        string
	MQTTBroker   string
	WhisperURL   string
	VADModelPath string
	PiperAddr    string
	AudioPort    int
	RAGURL       string
}

func configFromEnv() Config {
	audioPort := 0
	if v := os.Getenv("AUDIO_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &audioPort)
	}
	cfg := Config{
		HAURL:        os.Getenv("HA_URL"),
		HAToken:      os.Getenv("HA_TOKEN"),
		OllamaURL:    os.Getenv("OLLAMA_URL"),
		Model:        os.Getenv("MODEL"),
		MQTTBroker:   os.Getenv("MQTT_BROKER"),
		WhisperURL:   os.Getenv("WHISPER_URL"),
		VADModelPath: os.Getenv("VAD_MODEL_PATH"),
		PiperAddr:    os.Getenv("PIPER_ADDR"),
		AudioPort:    audioPort,
		RAGURL:       os.Getenv("RAG_URL"),
	}
	if cfg.HAURL == "" {
		log.Fatal("HA_URL environment variable is required (e.g. http://homeassistant.local:8123)")
	}
	if cfg.HAToken == "" {
		log.Fatal("HA_TOKEN environment variable is required (long-lived access token from HA profile)")
	}
	if cfg.OllamaURL == "" {
		cfg.OllamaURL = "http://192.168.1.145:11434/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "qwen2.5:7b"
	}
	if cfg.MQTTBroker == "" {
		cfg.MQTTBroker = "mqtt://localhost:1883"
	}
	if cfg.WhisperURL == "" {
		cfg.WhisperURL = "http://localhost:8000"
	}
	if cfg.VADModelPath == "" {
		cfg.VADModelPath = "silero_vad.onnx"
	}
	if cfg.PiperAddr == "" {
		cfg.PiperAddr = "localhost:10200"
	}
	if cfg.AudioPort == 0 {
		cfg.AudioPort = 8085
	}
	if cfg.RAGURL == "" {
		cfg.RAGURL = "http://192.168.1.145:8011"
	}
	if v := os.Getenv("OBSIDIAN_REPO"); v != "" {
		obsidianRepo = v
		listsDir = v + "/Lists"
	}
	return cfg
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
func speakToDevice(device, text string, tts *TTSClient, audioSrv *AudioServer, mqtt *VoiceMQTTClient) {
	wav, err := tts.Synthesize(text)
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
	if err := mqtt.PublishTTSURL(device, url); err != nil {
		log.Printf("[tts] publish error: %v", err)
	}
}
