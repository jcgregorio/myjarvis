# myjarvis

A Go CLI that connects a local LLM (via Ollama) to Home Assistant. Type a natural language command and it executes the corresponding HA action.

## Prerequisites

- [Ollama](https://ollama.com) running locally (or in Docker — see notes below)
- A Home Assistant instance with a long-lived access token
- A tool-calling capable model pulled in Ollama (e.g. `qwen2.5:7b`)

```bash
ollama pull qwen2.5:7b
```

## Configuration

Set environment variables before running:

| Variable | Required | Default | Description |
|---|---|---|---|
| `HA_URL` | Yes | — | Home Assistant base URL, e.g. `http://homeassistant.local:8123` |
| `HA_TOKEN` | Yes | — | Long-lived access token (HA Profile → Security → Long-Lived Access Tokens) |
| `OLLAMA_URL` | No | `http://localhost:11434/v1` | Ollama OpenAI-compatible endpoint |
| `MODEL` | No | `qwen2.5:7b` | Ollama model name |

## Build & Run

```bash
go build -o jarvis .
HA_URL=http://homeassistant.local:8123 HA_TOKEN=your_token_here ./jarvis
```

Or run directly:

```bash
HA_URL=http://homeassistant.local:8123 HA_TOKEN=your_token_here go run .
```

## Usage

```
Fetching Home Assistant entities...
Found 42 controllable entities.

Jarvis ready (model: qwen2.5:7b). Type a command (Ctrl+D to quit):
> turn off the kitchen lights
→ set_state({"entity_id":"light.kitchen","state":"off"})
  done.
> set a 10 minute timer for pasta
→ set_timer({"name":"pasta","duration_seconds":600})
  done.
> add milk to the shopping list
→ add_shopping_item({"item":"milk"})
  done.
```

## Supported Tools

| Tool | What it does | HA service called |
|---|---|---|
| `set_state` | Turn any controllable entity on or off | `<domain>.turn_on` / `turn_off` |
| `set_timer` | Start a countdown timer | `timer.start` |
| `add_shopping_item` | Add to the HA shopping list | `shopping_list.add_item` |
| `add_todo` | Add a task to a to-do list | `todo.add_item` |

Controllable entity domains: `light`, `switch`, `input_boolean`, `fan`, `cover`, `media_player`, `climate`, `script`, `automation`.

## Ollama in Docker

If Ollama is running via the project's docker-compose, it will be at `http://localhost:11434` by default. See the Obsidian notes for the full docker-compose setup.

## Next Steps

- Wire in the voice pipeline (Wyoming protocol / ESPHome native API)
- Add scene support (`scene.turn_on`)
- Add entity aliases so the LLM sees friendly names alongside entity IDs
