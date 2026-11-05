# myjarvis

Go-based voice assistant pipeline for Home Assistant. ESP32-S3-BOX-3B devices capture wake word + audio via MQTT, the server runs VAD + STT + LLM tool calling, then executes HA actions via REST API. Also supports interactive CLI mode for typed input.

## Architecture

```
ESP32-S3-BOX-3B (hey_jarvis mWW) --MQTT--> AudioRouter --VAD--> STT --> LLM --> HA REST API
                                                                             --> Obsidian Lists (git)
                                                                             --> RAG sidecar (goldmine-prime)
```

All services run on the same machine via Docker Compose:

| Service | Image | Role |
|---|---|---|
| `myjarvis` | local build | Voice pipeline, LLM orchestration, HA dispatch |
| `ollama` | `ollama/ollama:latest` | LLM serving (gemma4:e2b on RTX 3060 Ti) |
| `jarvis-whisper` | `faster-whisper-server` | Speech-to-text (CUDA) |
| `jarvis-piper` | `wyoming-piper` | Text-to-speech |
| `mosquitto` | `eclipse-mosquitto:2` | MQTT broker |
| `homeassistant` | `home-assistant` | Home automation hub |

## Voice Devices

Three ESP32-S3-BOX-3B devices, all running identical firmware from `esphome/voice-pe-base-esp32s3box.yaml`:

| Device name | Location |
|---|---|
| `myjarvis-kitchen` | Kitchen |
| `myjarvis-master-bedroom` | Master Bedroom |
| `myjarvis-office` | Office |

## Build & Run

Requires CGO enabled (onnxruntime for Silero VAD):

```bash
make build    # builds ./myjarvis binary
make install  # builds and installs to $GOPATH/bin
```

```bash
make run      # full voice pipeline (MQTT + CLI)
make dry-run  # CLI only, no HA execution
make list     # list controllable HA entities
make tools    # show generated tool schemas
```

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `HA_URL` | `http://homeassistant.local:8123` | Home Assistant base URL |
| `HA_TOKEN` | — | Long-lived access token for HA REST API |
| `MQTT_BROKER` | `mqtt://localhost:1883` | MQTT broker URL |
| `OLLAMA_URL` | `http://localhost:11434/v1` | OpenAI-compatible LLM endpoint |
| `MODEL` | `gemma4:e2b` | Ollama model name |
| `WHISPER_URL` | `http://localhost:8000` | faster-whisper server URL |
| `LISTS_DIR` | — | Obsidian vault lists directory |
| `RAG_URL` | — | Hybrid-search sidecar on goldmine-prime |

## Deploy

```bash
make deploy   # docker compose build + up (picks up code changes)
make restart  # restart without rebuilding (no code changes)
make logs     # follow myjarvis container logs
```

## ESPHome Firmware

```bash
make build-kitchen        # → /tmp/myjarvis-kitchen-factory.bin
make build-master-bedroom # → /tmp/myjarvis-master-bedroom-factory.bin
make build-office         # → /tmp/myjarvis-office-factory.bin
```

Factory flash new devices via [web.esphome.io](https://web.esphome.io/) in Chrome. After first flash, OTA works:

```bash
make ota           # OTA all three devices
make ota-kitchen   # OTA individual device
```

## Code Structure

- **`cmd/myjarvis/main.go`** — Entry point. Subcommands: `list`, `tools`. Flag: `--dry-run`.
- **`internal/voice/audio/audio.go`** — Per-device audio session management and VAD routing.
- **`internal/voice/vad/vad.go`** — Silero VAD v5 via ONNX. Dual-detector (500ms pause, 2000ms done).
- **`internal/voice/stt/stt.go`** — Sends audio to faster-whisper. Handles 16-bit and 32-bit stereo PCM.
- **`internal/voice/mqtt/mqtt.go`** — MQTT v5 client (autopaho). Subscribes to device audio topics.
- **`internal/llm/llm.go`** — OpenAI-compatible client pointed at local Ollama.
- **`internal/llm/tools.go`** — Dynamically builds OpenAI tool schemas from HA entities.
- **`internal/ha/ha.go`** — HA REST API client. Fetches and dispatches to controllable entities.
- **`internal/agent/agent.go`** — Tool call dispatcher.
- **`internal/lists/lists.go`** — Appends items to Obsidian markdown checklists with git sync.
- **`internal/rag/rag.go`** — HTTP client for goldmine-prime search sidecar (Qdrant hybrid search).
