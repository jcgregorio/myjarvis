package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
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
	tools := BuildTools(entities)

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

		start := time.Now()
		toolCalls, reply, err := llm.Chat(context.Background(), input, tools)
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
			if err := ha.ExecuteToolCall(context.Background(), tc); err != nil {
				fmt.Printf("  error: %v\n", err)
			} else {
				fmt.Printf("  done.\n")
			}
		}
	}
}

type Config struct {
	HAURL     string
	HAToken   string
	OllamaURL string
	Model     string
}

func configFromEnv() Config {
	cfg := Config{
		HAURL:     os.Getenv("HA_URL"),
		HAToken:   os.Getenv("HA_TOKEN"),
		OllamaURL: os.Getenv("OLLAMA_URL"),
		Model:     os.Getenv("MODEL"),
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
	return cfg
}
