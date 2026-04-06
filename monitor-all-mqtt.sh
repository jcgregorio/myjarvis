#!/bin/bash
# Monitor ALL MQTT topics to see if any device is publishing anything.
# Usage: ./monitor-all-mqtt.sh [timeout_seconds]

set -e

TIMEOUT=${1:-30}

docker exec mosquitto mosquitto_sub \
  -h localhost -p 1883 \
  -t '#' \
  -v \
  -W "$TIMEOUT"
