// Package config loads and validates myjarvis runtime configuration from
// environment variables. All defaults live here; the caller (cmd/myjarvis)
// is responsible for applying any cross-package side effects (e.g.
// pointing the obsidian package at a non-default vault path).
package config

import (
	"fmt"
	"log"
	"os"
)

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
}

// FromEnv builds a Config from environment variables, applying defaults
// and aborting the process on missing required values (HA_URL, HA_TOKEN).
func FromEnv() Config {
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
		ObsidianRepo: os.Getenv("OBSIDIAN_REPO"),
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
