# OpenCode

## Commands
- `make build`: builds the project
- `make install`: installs the project

## Code Style
- Use Go conventions
- Keep functions concise
- Follow existing patterns

## Project Info
- Go module: `github.com/jcgregorio/myjarvis`
- Go version: 1.25.4
- Build requires CGO enabled

## Tools
- `make run`: starts the voice pipeline
- `make dry-run`: CLI only mode
- `make list`: lists HA entities
- `make tools`: shows tool schemas

## Environment Variables
- `HA_URL`: Home Assistant base URL
- `HA_TOKEN`: Long-lived access token
- `MQTT_URL`: MQTT broker URL
- `OLLAMA_URL`: Ollama endpoint
- `MODEL`: Ollama model name
- `STT_URL`: faster-whisper server URL
- `LISTS_DIR`: Obsidian lists directory

## Architecture
ESP32 --> MQTT --> AudioRouter --> VAD --> STT --> LLM --> HA REST API

## Files
- `mqtt.go`: MQTT client
- `audio.go`: Audio routing
- `vad.go`: Silero VAD
- `stt.go`: Speech to text
- `llm.go`: LLM tool calling
- `tools.go`: HA tool schemas
- `ha.go`: HA REST client
- `lists.go`: Obsidian integration
- `main.go`: Entry point

