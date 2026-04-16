package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
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

	vault := NewVaultSearcher(obsidianRepo, llm)
	ha.SetVault(vault)

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
	router.OnSpeechEnd = func(device string) {
		voiceMQTT.PublishStopStreaming(device)
	}
	router.OnComplete = func(device string, audio []byte) {
		log.Printf("[voice] %s: received %d bytes of audio, transcribing...", device, len(audio))
		voiceMQTT.PublishLED(device, "thinking")

		start := time.Now()
		transcript, err := stt.Transcribe(audio)
		if err != nil {
			log.Printf("[voice] %s: STT error: %v", device, err)
			voiceMQTT.PublishLED(device, "off")
			return
		}
		log.Printf("[voice] %s: \"%s\" (%dms)", device, transcript, time.Since(start).Milliseconds())

		if transcript == "" {
			voiceMQTT.PublishLED(device, "off")
			return
		}

		if isStopCommand(transcript) {
			log.Printf("[voice] %s: stop command received", device)
			voiceMQTT.PublishStopPlayback(device)
			voiceMQTT.PublishLED(device, "off")
			return
		}

		toolsMu.RLock()
		currentTools := tools
		toolsMu.RUnlock()

		toolCalls, reply, err := llm.Chat(context.Background(), transcript, currentTools)
		if err != nil {
			log.Printf("[voice] %s: LLM error: %v", device, err)
			voiceMQTT.SignalError(device)
			return
		}

		if len(toolCalls) == 0 {
			log.Printf("[voice] %s: LLM reply (no tool call): %s", device, reply)
			voiceMQTT.SignalError(device)
			return
		}

		hadError := false
		for _, tc := range toolCalls {
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
	}
	if cfg.HAURL == "" {
		log.Fatal("HA_URL environment variable is required (e.g. http://homeassistant.local:8123)")
	}
	if cfg.HAToken == "" {
		log.Fatal("HA_TOKEN environment variable is required (long-lived access token from HA profile)")
	}
	if cfg.OllamaURL == "" {
		cfg.OllamaURL = "http://localhost:11434/v1"
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
