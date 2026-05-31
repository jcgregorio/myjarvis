// Package config loads and validates myjarvis runtime configuration from
// environment variables. All defaults live here; the caller (cmd/myjarvis)
// is responsible for applying any cross-package side effects (e.g.
// pointing the obsidian package at a non-default vault path).
package config

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
)

type ServerFlags struct {
	Port        string
	PromPort    string
	PprofPort   string
	HealthzPort string
	CheckoutDir string

	PatPath string
	Owner   string
	Repo    string
	Branch  string

	RestateURL string
}

// Flagset constructs a flag.FlagSet for the App.
func (s *ServerFlags) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("ci-workflow", flag.ExitOnError)
	fs.StringVar(&s.Port, "port", ":8000", "Main UI address (e.g., ':8000').")
	fs.StringVar(&s.PromPort, "prom_port", ":20000", "Metrics service address (e.g., ':20000').")
	fs.StringVar(&s.PprofPort, "pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
	fs.StringVar(&s.HealthzPort, "healthz_port", ":10000", "The port for health checks.")
	fs.StringVar(&s.CheckoutDir, "checkout_dir", "", "The file location of the git checkout.")

	fs.StringVar(&s.PatPath, "pat_path", "", "The file location of the git auth token in a file.")
	fs.StringVar(&s.Owner, "owner", "goldmine-build", "GitHub user or organization.")
	fs.StringVar(&s.Repo, "repo", "goldmine", "GitHub repo.")
	fs.StringVar(&s.Branch, "branch", "main", "GitHub repo branch.")

	fs.StringVar(&s.RestateURL, "restate_url", "https://restate-server.tail433733.ts.net", "The URL of the Restate UI.")

	return fs
}

// Config is the loaded environment-driven configuration for myjarvis.
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
	// ObsidianRepo is the on-disk path to the obsidian vault git repo.
	// Empty means "leave defaults set by the obsidian package alone".
	ObsidianRepo string
	DryRun       bool
}

// env gets the env value, or falls back to the defaultValue provided.
func env(key string, defaultValue string) string {
	ret := os.Getenv(key)
	if ret != "" {
		return ret
	}
	return defaultValue
}

func (c *Config) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("myjarvis", flag.ExitOnError)
	fs.StringVar(&c.HAURL, "ha-url", env("HA_URL", ":8000"), "URL of HomeAssistant API")
	fs.StringVar(&c.HAToken, "ha-token", env("HA_TOKEN", ""), "Home Assistant Authorization token used for HA API.")
	fs.StringVar(&c.OllamaURL, "ollama-url", env("OLLAMA_URL", "http://192.168.1.145:11434/v1"), "Ollama URL")
	fs.StringVar(&c.Model, "model", env("MODEL", "granite4:latest"), "The name of the model to use")
	fs.StringVar(&c.MQTTBroker, "mqtt-broker", env("MQTT_BROKER", "mqtt://localhost:1883"), "URL of MQTT broker")
	fs.StringVar(&c.WhisperURL, "whisper-url", env("WHISPER_URL", "http://localhost:8000"), "URL of whisper server")
	fs.StringVar(&c.VADModelPath, "vad-model-path", env("VAD_MODEL_PATH", "silero_vad.onnx"), "File path to VAD model")
	fs.StringVar(&c.PiperAddr, "piper-url", env("PIPER_ADDR", "localhost:10200"), "Piper address")
	fs.StringVar(&c.RAGURL, "rag-url", env("RAG_URL", "http://192.168.1.145:8011"), "URL of RAG server")
	fs.StringVar(&c.ObsidianRepo, "obsidian-repo", env("OBSIDIAN_REPO", ""), "Obsidian repo")
	audioPort := 8085
	var err error
	if v := os.Getenv("AUDIO_PORT"); v != "" {
		audioPort, err = strconv.Atoi(v)
		if err != nil {
			audioPort = 8085
		}
	}
	fs.IntVar(&c.AudioPort, "audio-port", audioPort, "Audio Port")
	fs.BoolVar(&c.DryRun, "dry-run", false, "Print tool calls without executing them against Home Assistant")

	return fs
}

// FromEnv builds a Config from environment variables, applying defaults
// and aborting the process on missing required values (HA_URL, HA_TOKEN).
func FromEnv() Config {
	audioPort := 0
	if v := os.Getenv("AUDIO_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &audioPort)
	}
	cfg := Config{
		HAURL: os.Getenv("HA_URL"),

		AudioPort: audioPort,
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
	return cfg
}
