#!/bin/bash
# Monitor MQTT diagnostic topics from the living-room-voice device.
# Usage: ./monitor-mqtt.sh [timeout_seconds]
#   default timeout: 60s

set -e

TIMEOUT=${1:-60}

docker exec mosquitto mosquitto_sub \
  -h localhost -p 1883 \
  -t 'jarvis/living-room-voice/audio_peak' \
  -t 'jarvis/living-room-voice/vnr' \
  -t 'jarvis/living-room-voice/xmos_stages' \
  -t 'jarvis/living-room-voice/xmos_version' \
  -v \
  -W "$TIMEOUT"
