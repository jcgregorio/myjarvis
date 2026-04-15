# myjarvis

Go-based voice assistant pipeline for Home Assistant. ESP32 devices capture wake word + audio via MQTT, server runs VAD + STT + LLM tool calling, then executes HA actions via REST API. Also supports interactive CLI mode for typed input.

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

| Variable | Purpose | Example |
|---|---|---|
| `HA_URL` | Home Assistant base URL | `http://homeassistant.local:8123` |
| `HA_TOKEN` | Long-lived access token for HA REST API | |
| `MQTT_URL` | MQTT broker URL | `mqtt://192.168.1.x:1883` |
| `OLLAMA_URL` | OpenAI-compatible endpoint for LLM | `http://192.168.1.145:11434/v1` |
| `MODEL` | Ollama model name | `qwen3:14b-64k` (default in Makefile: `qwen2.5:7b`) |
| `STT_URL` | faster-whisper server URL | `http://localhost:8000` |
| `LISTS_DIR` | Obsidian vault lists directory | `/home/jcgregorio/obsidian/Lists` |

## Architecture

```
ESP32 (microWakeWord) --MQTT--> AudioRouter --VAD--> STT --> LLM --> HA REST API
                                                                 --> Obsidian Lists (git)
```

- **mqtt.go** — MQTT v5 client (autopaho). Subscribes to `jarvis/+/{audio_start,audio,audio_stop,wake_detected}`. Publishes LED states, stop_streaming, TTS URLs.
- **audio.go** — AudioRouter manages per-device AudioSessions. Buffers PCM audio, triggers callbacks on speech end and session complete.
- **vad.go** — Silero VAD v5 via ONNX. 16kHz, threshold 0.5, 800ms min silence. Converts 32-bit stereo PCM to float32 mono. Runs detection every ~250ms on sliding 3-second window.
- **stt.go** — Sends audio to faster-whisper. Converts 32-bit stereo PCM to 16-bit mono WAV. Multipart POST to `/v1/audio/transcriptions`.
- **llm.go** — OpenAI-compatible client (openai-go SDK) pointed at Ollama. System prompt for home assistant voice control. Has regex fallback for qwen2.5 text-format tool calls `(((tool_name {"arg": "val"})))`.
- **tools.go** — Dynamically builds OpenAI tool schemas from HA entities. Tools: `set_state`, `trigger_automation`, `set_timer`, `add_to_list`.
- **ha.go** — HA REST API client. Fetches controllable entities (light, switch, input_boolean, fan, cover, media_player, climate, script, automation). Maintains friendly-name-to-entity-ID map. Dispatches tool calls to domain-specific service handlers.
- **lists.go** — Appends items to Obsidian markdown checklists in `LISTS_DIR`. Does git pull/add/commit/push for sync.
- **main.go** — Entry point. Loads config from env vars. Refreshes HA entities every 5 minutes. Runs MQTT subscriber + interactive CLI loop. Subcommands: `list`, `tools`. Flag: `--dry-run`.

## ESPHome Firmware

`esphome/` contains YAML configs for ESP32-S3 voice devices (kitchen-voice, living-room-voice). Uses local hey_jarvis wake word model with patched manifest to work around upstream tensor_arena_size bug.

## Infrastructure

- **LLM server**: goldmine-prime (192.168.1.145) running Ollama with `qwen3:14b-64k` on RTX 5000 (16GB VRAM)
- **STT server**: faster-whisper instance
- **MQTT broker**: local network broker
- **Home Assistant**: existing install with REST API enabled

## Conventions

- Entity names in tool schemas use lowercased HA friendly names
- Audio format from ESP32: 32-bit stereo PCM, converted server-side to 16-bit mono
- No test files currently exist
- Module path: `github.com/jcgregorio/myjarvis`
- Go 1.25.4
