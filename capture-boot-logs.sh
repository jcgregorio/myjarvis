#!/bin/bash
# Subscribe to debug topic, flash firmware (triggers reboot), capture boot messages.
# Run from the myjarvis directory.

set -ex

cd /home/jcgregorio/myjarvis

echo "Subscribing to living-room-voice/debug for 60s..."
docker exec mosquitto mosquitto_sub -h localhost -p 1883 -t 'living-room-voice/debug' -W 60 2>&1 \
  | sed 's/\x1b\[[0-9;]*m//g' > /tmp/debug_msgs.txt &
SUBPID=$!

sleep 2

echo "Flashing firmware (will trigger reboot)..."
~/.venv/esphome/bin/esphome run esphome/test-voice.yaml --device living-room-voice.local --no-logs 2>&1 | tail -3

echo "Waiting for debug messages..."
wait $SUBPID 2>/dev/null

echo ""
echo "=== Boot messages ==="
grep -i "voice_kit\|xmos\|i2s\|error\|fail\|version\|DFU\|pipeline\|firmware\|wake\|micro\|boot" /tmp/debug_msgs.txt | head -40

echo ""
echo "=== Full log saved to /tmp/debug_msgs.txt ==="
wc -l /tmp/debug_msgs.txt
