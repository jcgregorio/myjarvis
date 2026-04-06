# ESPHome Firmware

Custom firmware for the Seeed Studio / HA Voice Preview Edition, replacing the stock
HA voice pipeline with a standalone MQTT-based pipeline driven by myjarvis.

## Files

- `kitchen-voice.yaml` — firmware for the kitchen device (template for additional devices)
- `secrets.yaml` — symlink to `secret/secrets.yaml` (wifi credentials, not in this repo)
- `secret/` — git submodule: [jcgregorio/myjarvis-secret](https://github.com/jcgregorio/myjarvis-secret)

## What this firmware does differently from stock

- Replaces `voice_assistant` (HA WebSocket pipeline) with direct MQTT streaming
- `hey_jarvis` wake word runs on-chip via TFLite Micro — nothing leaves the device until detected
- On wake: stops mWW, starts mic, streams 32-bit PCM chunks to `jarvis/<device>/audio`
- Center button also triggers push-to-talk
- TTS playback: myjarvis sends an HTTP URL to `jarvis/<device>/tts_url` → media_player fetches and plays it
- LED ring shows listening (blue) / thinking (amber sweep) / replying (green) states
- Hardware mute switch and software mute both work
- Volume dial controls playback volume

## MQTT topic schema

| Topic | Direction | Content |
|---|---|---|
| `jarvis/<device>/audio_start` | device → myjarvis | Begin audio stream |
| `jarvis/<device>/audio` | device → myjarvis | Raw 32-bit PCM chunks, 16kHz stereo |
| `jarvis/<device>/audio_stop` | device → myjarvis | End of utterance |
| `jarvis/<device>/tts_url` | myjarvis → device | HTTP URL of TTS audio file to play |
| `jarvis/<device>/led` | myjarvis → device | LED state: `listening`, `thinking`, `off` |
| `jarvis/<device>/muted` | device → myjarvis | `true` / `false` |
| `jarvis/<device>/status` | device → myjarvis | `online` / `offline` (birth/will) |

## Audio format note

The audio arriving at myjarvis is **32-bit PCM, 16kHz, stereo**. myjarvis must convert
this to 16-bit mono before sending to faster-whisper. This conversion is the next piece
of work on the Go side.

## Flashing

```bash
# Install toolchain (one-time)
python3 -m venv ~/.venv/esphome
source ~/.venv/esphome/bin/activate
pip install esphome esptool

# Add user to dialout group for USB access (re-login after)
sudo usermod -aG dialout $USER

# Fill in real credentials (already set for this machine)
$EDITOR secret/secrets.yaml

# Compile and flash over USB
esphome run kitchen-voice.yaml
```

To add another device, copy `kitchen-voice.yaml` and update the `device_name` and
`friendly_name` substitutions at the top.

## Cloning on a new machine

```bash
git clone --recurse-submodules https://github.com/jcgregorio/myjarvis.git
```
