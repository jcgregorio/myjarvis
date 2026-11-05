# ESPHome Firmware

Custom firmware for three ESP32-S3-BOX-3B voice devices. Replaces the stock HA voice
pipeline with a standalone MQTT-based pipeline driven by myjarvis.

## Devices

All three devices run identical firmware from `voice-pe-base-esp32s3box.yaml`:

| Config file | Device name | Location |
|---|---|---|
| `myjarvis-kitchen.yaml` | `myjarvis-kitchen` | Kitchen |
| `myjarvis-master-bedroom.yaml` | `myjarvis-master-bedroom` | Master Bedroom |
| `myjarvis-office.yaml` | `myjarvis-office` | Office |

## Hardware: ESP32-S3-BOX-3B

- ES7210 4-mic array (I2S, 16-bit stereo, 16kHz) — audio input
- ES8311 codec (I2S, 16-bit, 48kHz) — audio output / TTS playback
- ILI9342C 320×240 display — shows Listening / Thinking / Error / idle states
- GT911 touchscreen
- 16MB flash, 16MB octal PSRAM

Audio format sent to myjarvis: **16-bit stereo PCM, 16kHz** (64KB/s).

## What this firmware does

- `hey_jarvis` wake word runs on-chip via TFLite Micro — nothing leaves the device until detected
- On wake: disables mWW model, flushes 1.5s pre-buffer, streams 16-bit PCM chunks to `jarvis/<device>/audio`
- Server sends `stop_streaming` when VAD detects end of speech; device re-enables mWW
- TTS playback: myjarvis sends HTTP URL to `jarvis/<device>/tts_url` → media_player fetches and plays
- Display shows Listening (blue) / Thinking (amber) / Error (red) / idle (off) states
- Center button also triggers push-to-talk

## MQTT topic schema

| Topic | Direction | Content |
|---|---|---|
| `jarvis/<device>/audio_start` | device → server | Begin audio stream |
| `jarvis/<device>/audio` | device → server | Raw 16-bit stereo PCM chunks, 16kHz |
| `jarvis/<device>/audio_stop` | device → server | End of utterance (30s safety timeout) |
| `jarvis/<device>/wake_detected` | device → server | Wake word fired |
| `jarvis/<device>/stop_streaming` | server → device | Stop audio stream, re-enable mWW |
| `jarvis/<device>/tts_url` | server → device | HTTP URL of TTS audio to play |
| `jarvis/<device>/led` | server → device | Display state: `listening`, `thinking`, `error`, `off` |
| `jarvis/<device>/text` | server → device | Text to show on display |
| `jarvis/<device>/status` | device → server | `online` / `offline` (birth/will, retained) |

## Flashing

New devices must be factory-flashed via USB (OTA requires existing firmware):

```bash
# Build factory binary
make build-kitchen        # → /tmp/myjarvis-kitchen-factory.bin
make build-master-bedroom # → /tmp/myjarvis-master-bedroom-factory.bin
make build-office         # → /tmp/myjarvis-office-factory.bin
```

Go to [web.esphome.io](https://web.esphome.io/) in Chrome, plug in via USB, select the binary.

After the first factory flash, future updates work over WiFi:

```bash
make ota                  # update all three devices
make ota-kitchen          # update one device
```

## Wake word model

`hey_jarvis.json` is a local manifest pointing to `hey_jarvis.tflite`. The manifest patches
`tensor_arena_size` to work around an upstream bug. The `.tflite` file is a symlink into the
ESPHome cache directory.

## Cloning on a new machine

```bash
git clone --recurse-submodules https://github.com/jcgregorio/myjarvis.git
```

WiFi credentials live in the `esphome/secret/` git submodule (private repo).
