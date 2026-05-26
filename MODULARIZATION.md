# myjarvis Modularization Plan

**Status:** plan only. No code moves here — this is the blueprint to execute in
small, mechanically-verifiable steps (`git mv` per file → package rename → fix
imports → `go build && go test ./...` after each step).

**Why:** 22 `.go` files / ~4,400 lines, *all* in `package main`. Coverage stalls
around 27% because most logic — the entire voice loop, the cross-cutting tool
dispatch, the `Config` struct — sits in `main.go` or other places where it
can't be unit-tested. Splitting unlocks coverage; it doesn't add it.

---

## Proposed layout

Standard Go (`cmd/` + `internal/`). Tests live next to their production code.
Live-LLM integration tests get a build tag so they're out of the default
`go test ./...`.

```
cmd/myjarvis/                        # tiny: flag parse, wire deps, dispatch
internal/
  config/                            # Config struct + env loading
  ha/                                # HA REST client + entity types
  llm/                               # LLMClient + system prompt + tool schemas
  rag/                               # RAG sidecar client + Searcher
  voice/                             # audio pipeline orchestration
    mqtt/                            # MQTT v5 transport
    audio/                           # per-device session/buffering
    vad/                             # Silero VAD (CGO — onnxruntime)
    stt/                             # faster-whisper client
  tts/                               # Wyoming/Piper client + HTTP audio server
    normalize/                       # number/unit/sci-notation text rewriter
  obsidian/                          # vault writers + shared git helpers
    lists/                           # list pages (check/add/clean)
    property/                        # property activity log
  agent/                             # tool DISPATCH (depends on ha, rag, obsidian)
tests/integration/                   # //go:build integration — routing/synth/retrieval
```

---

## Data flow through the proposed modules

The diagram below traces one voice command end-to-end. Plain ASCII so it
renders in any markdown viewer (including VS Code's built-in preview).

```text
Legend
  [foo]    external system or device
  +---+    internal package / component
  -->      data flow (request / response)
  <-->     bidirectional (call into external service)
  ..>      side channel (init data, or the RAG -> LLM callback for synthesis)


Startup (one-time, before any voice command):

  +-------------------+        +-----------------------------+
  | internal/config   | ..>    | cmd/myjarvis main()         |
  | Config, fromEnv   |        | flag parse, wire deps, run  |
  +-------------------+        +-----------------------------+


Voice command pipeline (one round-trip per utterance):

  [ESP32 device: mic, LED ring, speaker]
        |                                 ^
        | wake + PCM chunks (MQTT)        | "play TTS URL" (MQTT)
        v                                 |
  +---------------------+                 |
  | voice/mqtt          |-----------------+
  +----------+----------+
             | raw PCM
             v
  +---------------------+
  | voice/audio         |<----------------+
  | AudioRouter         |                 |
  +----------+----------+                 | end-of-speech
             | 3-sec window               |
             v                            |
  +---------------------+                 |
  | voice/vad    (CGO)  |-----------------+
  | Silero VAD / ONNX   |
  +---------------------+
             | (utterance committed by AudioRouter)
             v
  +---------------------+         +-------------------+
  | voice/stt           |<------->| [faster-whisper]  |
  +----------+----------+         +-------------------+
             | transcript
             v
  +-------------------------+     +---------------------+
  | llm                     |<--->| [Ollama granite4]   |
  | LLMClient.Chat          |     +---------------------+
  | + system prompt         |
  | + BuildTools (schemas)  |
  +-----------+-------------+
              | ToolCall { name, args }
              v
  +-------------------------+
  | agent                   |
  | Dispatcher              |
  | ExecuteToolCall         |
  +-+-----+-----+-----+-----+
    |     |     |     |
    |     |     |     +-->  obsidian/property  --+
    |     |     |                                |
    |     |     +------->   obsidian/lists  -----+--> obsidian (git helpers)
    |     |                                              |
    |     |                                              v
    |     |                                  [Obsidian git remote]
    |     |
    |     +----------->     rag.Searcher    <-->  [RAG sidecar -> Qdrant]
    |                            |                 (WIKIPEDIA_ENGLISH,
    |                            :                  obsidian_vault)
    |                            ..>  ChatPlain for synthesis (back to llm)
    |
    +----------------->     ha.HAClient     <-->  [Home Assistant REST]


  Spoken reply text returned by the dispatched tool:

  agent (text)
    |
    v
  +-------------------------+
  | tts/normalize           |
  | NormalizeForTTS:        |
  | dates, units, percent,  |
  | scientific notation     |
  +-----------+-------------+
              | TTS-safe plain text
              v
  +-------------------------+     +---------------------+
  | tts.TTSClient           |<--->| [Piper / Wyoming]   |
  +-----------+-------------+     +---------------------+
              | WAV bytes
              v
  +-------------------------+
  | tts.AudioServer         | <-- ESP32 GET /audio/<id>.wav
  | (HTTP)                  |
  +-----------+-------------+
              | audio URL
              v
  voice/mqtt  --> publish "play TTS URL" --> [ESP32]


Out-of-band: tests/integration (build-tagged `//go:build integration`)
exercises the live LLM + RAG sidecar paths and is not part of the runtime
data flow above.
```

### Walkthrough — one voice command

1. **Wake & ingest.** ESP32 detects the wake word locally, publishes
   `jarvis/<dev>/wake_detected` and PCM chunks on `jarvis/<dev>/audio` →
   `voice/mqtt` receives → `voice/audio` buffers per device.
2. **End-of-speech.** `voice/vad` runs on a sliding 3-second window;
   silence → `voice/audio` commits the buffered utterance.
3. **Transcription.** `voice/stt` converts 32-bit stereo PCM → 16-bit mono
   WAV, multipart-POSTs to faster-whisper, gets text back.
4. **Routing.** `llm.LLMClient.Chat` sends the transcript with `BuildTools`
   schemas + system prompt to Ollama (`granite4:latest`). Returns either a
   structured `tool_calls` array or a plain text reply.
5. **Dispatch.** `agent.ExecuteToolCall` switches on the tool name → calls
   into `ha`, `rag`, `obsidian/lists`, or `obsidian/property`. (This is the
   layer that today lives wrongly inside `ha.go`.)
6. **RAG path (search_notes / search_wikipedia).** `rag.RAGSearcher`
   retrieves via the sidecar, re-ranks (drops disambiguation pages, demotes
   indexy pages), optionally re-queries the LLM once for a better article
   title, then calls back into `llm.ChatPlain` for synthesis.
7. **Vault writes (add_to_list, log_property_event, etc.).** `obsidian/*`
   uses the shared git helpers to pull → write → commit → push.
8. **Spoken reply.** The dispatcher's returned text goes through
   `tts/normalize` (dates, units, scientific notation → spoken words) →
   `tts.TTSClient` to Piper → WAV bytes → `tts.AudioServer`.
9. **Playback.** `voice/mqtt` publishes the audio URL to the device; the
   ESP32 GETs the WAV from `tts.AudioServer` and plays it.

---

## File-by-file

| File | Belongs in exe? | What it does (1 line) | Target package |
|---|---|---|---|
| `main.go` | **Split** | 475 lines: `main()`, `runList`/`runTools` subcommands, `Config`+`configFromEnv`, `speakToDevice`, `isStopCommand`, plus ~310 lines of voice-loop orchestration | `cmd/myjarvis/main.go` (≤50 lines: flags + wire + run) — push `Config` to `internal/config`, voice loop to `internal/voice`, `speakToDevice`+`isStopCommand` to `internal/voice`, subcommands to `cmd/myjarvis/{list,tools}.go` |
| `ha.go` | **Split** | HA REST client + entity types **AND** `ExecuteToolCall` dispatch (calls into lists, property, rag) | Client+types → `internal/ha`; **dispatch → `internal/agent`** (worst coupling smell: `ha` depends on `rag`/`lists`/`property` purely because the switch lives in the wrong file) |
| `ha_test.go` | Yes (test) | Tests for ha client | `internal/ha` |
| `llm.go` | Yes | `LLMClient` (OpenAI-compat), system prompt, `Chat`/`ChatPlain`, `ToolCall` | `internal/llm` |
| `tools.go` | Yes | `BuildTools` — OpenAI tool schemas (set_state, search_*, lists, log_property_event) | `internal/llm` (lives next to the system prompt — the routing contract) |
| `tools_test.go` | Yes (test) | Unit tests for `BuildTools` | `internal/llm` |
| `rag.go` | Yes | RAG sidecar HTTP client + `RAGSearcher` (`AnswerFromNotes`/`Wikipedia`) + re-rank + adversarial re-query | `internal/rag` (consider `client.go` + `searcher.go` within the pkg) |
| `lists.go` | **Split** | Obsidian list ops **AND** the shared `gitCmd`/`gitCommitAndPush` helpers (already used by `property.go`) | List ops → `internal/obsidian/lists`; **git helpers → `internal/obsidian`** so both `lists` and `property` import them properly |
| `property.go` | Yes | Property log writer (`LogPropertyEvent`, `appendUnderLogSection`, date/short-prop helpers, `resolveProperty`) | `internal/obsidian/property` |
| `property_test.go` | Yes (test) | Unit + git-backed tests for property logging | `internal/obsidian/property` |
| `mqtt.go` | Yes | MQTT v5 client (autopaho); subscribes `jarvis/+/audio*`/`wake_detected`, publishes LED/TTS-URL/stop | `internal/voice/mqtt` |
| `audio.go` | Yes | `AudioRouter` — per-device `AudioSession` buffering, end-of-speech/session callbacks | `internal/voice/audio` |
| `vad.go` | Yes | Silero VAD v5 via ONNX (**only CGO-dependent file** — onnxruntime) | `internal/voice/vad` — isolating CGO here is a hygiene win: only this subpkg needs the onnxruntime build env |
| `stt.go` | Yes | faster-whisper client (32-bit stereo PCM → 16-bit mono WAV → multipart POST) | `internal/voice/stt` |
| `tts.go` | Yes | `TTSClient` — Wyoming protocol to Piper | `internal/tts` |
| `tts_test.go` | Yes (test) | Tests for tts | `internal/tts` |
| `audioserver.go` | Yes | Tiny HTTP server that serves synthesized `.wav` files for ESP32 fetch | `internal/tts` (the TTS *delivery* channel — naturally paired) |
| `ttsnorm.go` | Yes | Text rewriter: dates/years/dollars/percent/units/scientific notation for Piper | `internal/tts/normalize` (subpkg — keeps it independently importable and surfaces it as the contract layer it is) |
| `ttsnorm_test.go` | Yes (test) | 30+ table tests for normalizer | `internal/tts/normalize` |
| `routing_test.go` | **Tag** | Live LLM 33-prompt routing accuracy + latency suite | `tests/integration` with `//go:build integration` — out of default test runs |
| `synthesis_test.go` | **Tag** | Live RAG answer-synthesis quality suite | `tests/integration` with `//go:build integration` |
| `retrieval_test.go` | **Tag** | Live retrieval-ranking probe (keyword vs question vs both) | `tests/integration` with `//go:build integration` |

---

## Notable observations from the inventory

1. **Two clear coupling smells.** `ha.go` is `internal/ha` *plus* the tool
   dispatcher; the dispatcher reaches into `rag`, `lists`, `property`.
   `lists.go` defines `gitCmd`/`gitCommitAndPush` that `property.go`
   already imports cross-file. Both are package-`main` artifacts — they
   vanish once `agent` and `obsidian` exist as real packages with a clean
   dependency direction (`agent → {ha, llm, rag, obsidian/...}`).
2. **CGO confined to one place.** `vad.go` is the only file requiring
   onnxruntime headers/libs. Pulling it into `internal/voice/vad` means
   everything else builds without the CGO env — a meaningful test/dev
   experience win, and removes most of the friction behind the
   `.vscode/settings.json` we just had to plumb.
3. **`main.go` is the biggest single test-coverage blocker.** ~310 lines
   of orchestration in `main()` and a `Config` struct that should be a
   unit. After split, `main()` becomes ≤50 lines of wiring (untestable
   but trivial), and the previously-untestable orchestration becomes a
   `voice.Run(deps)` that can be tested with mocks/fakes.
4. **Integration tests with a build tag** keep the live-LLM gating in
   the build system instead of relying on env-var checks at test-runtime.
   `go test ./...` stays fast/offline; `go test -tags integration
   ./tests/integration` exercises the LLM/RAG. The existing env-var skips
   can remain as an extra guard.
5. **Test files move with their production code** (Go convention). After
   the move, coverage per package becomes meaningful — today the 27%
   number is a blended average across a single bag.

---

## What this *doesn't* fix on its own

Modularization unlocks coverage; it doesn't add it. After the split, the
gaps to address with new unit tests are roughly: `voice.Run` orchestration
paths, `ha` REST client/error paths, the new `agent` dispatcher, `mqtt`
topic handling, `rag.RAGSearcher` happy/junk-result paths, and
`tts.TTSClient` Wyoming framing. Each of those becomes straightforward
once it's in its own package with an interface its caller can fake.

---

## Migration approach (when executing)

A sequence of small, mechanically-verifiable steps. Do **not** do this as
one giant churn. Suggested order:

1. `internal/config/` — extract `Config` + `configFromEnv` from `main.go`.
   *Smallest possible first move; proves the rename pattern.*
2. `internal/tts/normalize/` — pure functions, no deps; tests come along.
3. `internal/ha/` — client + entities + dispatch still lives here for now.
4. `internal/llm/` — `LLMClient` + `BuildTools` + system prompt + tests.
5. `internal/obsidian/` — git helpers at package root, then
   `internal/obsidian/lists`, then `internal/obsidian/property` (+ tests).
6. `internal/rag/` — sidecar client + `RAGSearcher`.
7. `internal/agent/` — **move the dispatch out of `ha`**; this is the
   commit that breaks the `ha → rag/lists/property` coupling.
8. `internal/tts/` — `TTSClient` + `AudioServer` + tests.
9. `internal/voice/{mqtt,audio,stt,vad}/` — leaves `vad` as the only
   CGO-tainted subpackage.
10. Extract `voice.Run` from the orchestration currently in `main()`.
11. `cmd/myjarvis/` — final `main.go` reduced to flags + wire + run.
12. `tests/integration/` — move the three live-LLM `*_test.go` files
    behind `//go:build integration`.

After each step: `go build ./... && go test ./...` must be green before
the next move. Each step is one PR / one commit.
