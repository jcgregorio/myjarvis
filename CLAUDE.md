# myjarvis

Go-based voice assistant pipeline for Home Assistant. ESP32-S3-BOX-3B devices capture wake word + audio via MQTT, server runs VAD + STT + LLM tool calling, then executes HA actions via REST API. Also supports interactive CLI mode for typed input.

## Build

Requires CGO enabled (onnxruntime for Silero VAD):

```bash
make build    # builds ./myjarvis binary
make install  # builds and installs to $GOPATH/bin
```

## Run

```bash
# Full voice pipeline (MQTT + CLI)
make run

# CLI only, no HA execution
make dry-run

# List controllable HA entities
make list

# Show generated tool schemas
make tools
```

## Environment Variables

| Variable | Purpose | Default |
|---|---|---|
| `HA_URL` | Home Assistant base URL | `http://homeassistant.local:8123` |
| `HA_TOKEN` | Long-lived access token for HA REST API | |
| `MQTT_BROKER` | MQTT broker URL | `mqtt://localhost:1883` |
| `OLLAMA_URL` | OpenAI-compatible endpoint for LLM | `http://localhost:11434/v1` |
| `MODEL` | Ollama model name | `gemma4:e2b` |
| `WHISPER_URL` | faster-whisper server URL | `http://localhost:8000` |
| `LISTS_DIR` | Obsidian vault lists directory | `/home/jcgregorio/obsidian/Lists` |
| `RAG_URL` | Hybrid-search sidecar on goldmine-prime | `http://192.168.1.145:8011` |

## Architecture

```
ESP32-S3-BOX-3B (microWakeWord) --MQTT--> AudioRouter --VAD--> STT --> LLM --> HA REST API
                                                                           --> Obsidian Lists (git)
                                                                           --> RAG sidecar --> Qdrant
                                                                                 (obsidian_vault, WIKIPEDIA_ENGLISH)
```

- **mqtt.go** — MQTT v5 client (autopaho). Subscribes to `jarvis/+/{audio_start,audio,audio_stop,wake_detected}`. Publishes LED states, stop_streaming, TTS URLs.
- **audio.go** — AudioRouter manages per-device AudioSessions with dedicated VAD processors. Buffers PCM audio, triggers callbacks on speech end and session complete.
- **vad.go** — Silero VAD v5 via ONNX. Dual-detector: 500ms pause (triggers speculative processing), 2000ms silence (triggers end of speech). Sliding 5-second window, runs every ~250ms.
- **stt.go** — Sends audio to faster-whisper. Handles both 16-bit stereo (S3-BOX-3B) and 32-bit stereo (legacy) PCM. Multipart POST to `/v1/audio/transcriptions`.
- **llm.go** — OpenAI-compatible client (openai-go SDK) pointed at local Ollama (gemma4:e2b).
- **tools.go** — Dynamically builds OpenAI tool schemas from HA entities. Tools: `set_state`, `trigger_automation`, `set_timer`, `check_list`, `check_off_item`, `uncheck_item`, `clean_lists`, `add_to_list`, `search_notes`, `search_wikipedia`.
- **ha.go** — HA REST API client. Fetches controllable entities (light, switch, input_boolean, fan, cover, media_player, climate, script, automation). Maintains friendly-name-to-entity-ID map. Dispatches tool calls to domain-specific service handlers.
- **rag.go** — HTTP client for the goldmine-prime search sidecar plus `RAGSearcher` that runs hybrid Qdrant retrieval (SPLADE + BM25, DBSF fusion) over `obsidian_vault` or `WIKIPEDIA_ENGLISH`.
- **lists.go** — Appends items to Obsidian markdown checklists in `LISTS_DIR`. Does git pull/add/commit/push for sync.
- **main.go** — Entry point. Loads config from env vars. Refreshes HA entities every 5 minutes. Runs MQTT subscriber + interactive CLI loop. Subcommands: `list`, `tools`. Flag: `--dry-run`.

## ESPHome Firmware

`esphome/` contains YAML configs for three ESP32-S3-BOX-3B voice devices:
- `myjarvis-kitchen.yaml` — Kitchen
- `myjarvis-master-bedroom.yaml` — Master Bedroom
- `myjarvis-office.yaml` — Office

All use `voice-pe-base-esp32s3box.yaml` as the shared base. Uses local `hey_jarvis.json` manifest with patched `tensor_arena_size` to work around an upstream bug. Audio format: 16-bit stereo PCM at 16kHz.

New devices require factory flash via USB + web.esphome.io (`make build-kitchen` etc.). Subsequent updates use `make ota`.

## Infrastructure

All services run on a single machine (joelegion) via Docker Compose:
- **LLM**: local Ollama container (`ollama/ollama:latest`) running `gemma4:e2b` on RTX 3060 Ti (8GB VRAM, ~105ms P50)
- **STT**: faster-whisper container (CUDA, `small` model)
- **TTS**: piper container (`en_US-hfc_male-medium`)
- **MQTT**: mosquitto container
- **Home Assistant**: homeassistant container (`network_mode: host`)
- **RAG sidecar**: goldmine-prime (192.168.0.154) — only used for `search_notes` / `search_wikipedia`

## Conventions

- Entity names in tool schemas use lowercased HA friendly names
- S3-BOX-3B audio: 16-bit stereo PCM → register in `DeviceFormats` as `PCM16BitStereo`
- Module path: `github.com/jcgregorio/myjarvis`
- Go 1.25.4
