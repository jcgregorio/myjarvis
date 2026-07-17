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
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/jcgregorio/myjarvis/internal/agent"
	"github.com/jcgregorio/myjarvis/internal/config"
	"github.com/jcgregorio/myjarvis/internal/ha"
	"github.com/jcgregorio/myjarvis/internal/llm"
	"github.com/jcgregorio/myjarvis/internal/obsidian"
	"github.com/jcgregorio/myjarvis/internal/obsidian/lists"
	"github.com/jcgregorio/myjarvis/internal/obsidian/property"
	"github.com/jcgregorio/myjarvis/internal/rag"
	"github.com/jcgregorio/myjarvis/internal/tts"
	"github.com/jcgregorio/myjarvis/internal/voice"
	"github.com/jcgregorio/myjarvis/internal/voice/audio"
	"github.com/jcgregorio/myjarvis/internal/voice/mqtt"
	"github.com/jcgregorio/myjarvis/internal/voice/stt"
	"github.com/jcgregorio/myjarvis/internal/voice/vad"
)

// applyConfig propagates cross-package side effects of the loaded
// config (currently: pointing the obsidian-vault package vars at a
// non-default location).
func applyConfig(cfg *config.Config) {
	if cfg.ObsidianRepo != "" {
		obsidian.SetRepo(cfg.ObsidianRepo)
		lists.SetDir(cfg.ObsidianRepo + "/Lists")
		property.SetDir(cfg.ObsidianRepo + "/Properties")
	}
}

func main() {
	action := "main"
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "list":
			os.Args = slices.Delete(os.Args, 1, 2)
			action = "list"
		case "tools":
			os.Args = slices.Delete(os.Args, 1, 2)
			action = "tools"
		}
	}

	cfg := &config.Config{}
	if err := cfg.Flagset().Parse(os.Args); err != nil {
		log.Fatalf("Failed parse flags: %s", err)
	}
	flag.Parse()
	applyConfig(cfg)

	switch action {
	case "list":
		runList(cfg)
	case "tools":
		runTools(cfg)
	case "main":
		mainAction(cfg)
	}
}

func mainAction(cfg *config.Config) {
	// --- construct deps -------------------------------------------------
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

	sttc := stt.NewClient(cfg.WhisperURL)
	ttsc := tts.NewClient(cfg.PiperAddr)

	audioSrv, err := tts.NewAudioServer(cfg.AudioPort)
	if err != nil {
		log.Fatalf("Failed to start audio server: %v", err)
	}

	vadProc, err := vad.NewProcessor(cfg.VADModelPath)
	if err != nil {
		log.Fatalf("Failed to initialize VAD: %v", err)
	}
	defer vadProc.Destroy()

	router := audio.NewRouter(vadProc)
	voiceMQTT, err := mqtt.NewClient(context.Background(), cfg.MQTTBroker, router)
	if err != nil {
		log.Printf("MQTT connection failed (voice input disabled): %v", err)
	} else {
		fmt.Println("Voice MQTT subscriber started.")
	}

	// --- run the voice pipeline ----------------------------------------
	runner := voice.New(voice.Deps{
		HA:              hc,
		LLM:             lc,
		Dispatcher:      dispatcher,
		STT:             sttc,
		TTS:             ttsc,
		AudioServer:     audioSrv,
		Router:          router,
		MQTT:            voiceMQTT,
		InitialTools:    llm.BuildTools(entities, listNames, propertyNames),
		RefreshInterval: 5 * time.Minute,
		DebugAudioDir:   cfg.DebugAudioDir,
		DeviceFormats: map[string]stt.PCMFormat{
			"development-voice": stt.PCM16BitStereo,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)

	if cfg.DryRun {
		fmt.Println("(dry-run mode: tool calls will be printed but not executed)")
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		runCLI(runner, lc, dispatcher, cfg.Model, cfg.DryRun)
		return
	}

	// Service mode: no stdin, block until signal.
	fmt.Printf("Jarvis running as service (model: %s). Voice pipeline active.\n", cfg.Model)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("Shutting down.")
}

// runCLI is the typed-input loop for interactive use. Reads from stdin
// and routes through the same LLM + dispatcher as the voice path; the
// voice runner keeps running in the background via its own goroutine.
func runCLI(runner *voice.Runner, lc *llm.Client, dispatcher *agent.Dispatcher, model string, dryRun bool) {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("\nJarvis ready (model: %s). Type a command (Ctrl+D to quit):\n", model)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		start := time.Now()
		toolCalls, reply, err := lc.Chat(context.Background(), input, runner.Tools())
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
			if dryRun {
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
}

func runTools(cfg *config.Config) {
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

func runList(cfg *config.Config) {
	hc := ha.NewClient(cfg.HAURL, cfg.HAToken)

	entities, err := hc.FetchControllableEntities(context.Background())
	if err != nil {
		log.Fatalf("Failed to fetch HA entities: %v", err)
	}

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
