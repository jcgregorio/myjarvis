#!/bin/bash
#
# Install the search-server systemd user unit on goldmine-prime.
# Requires linger to be enabled so the service runs without an active session:
#   sudo loginctl enable-linger jcgregorio

set -e

UNIT_SRC="/home/jcgregorio/myjarvis/goldmine-prime/scripts/myjarvis-search.service"
UNIT_DST="$HOME/.config/systemd/user/myjarvis-search.service"

mkdir -p "$(dirname "$UNIT_DST")"
cp "$UNIT_SRC" "$UNIT_DST"

systemctl --user daemon-reload
systemctl --user enable --now myjarvis-search.service

echo "Installed and started:"
systemctl --user --no-pager status myjarvis-search.service | head -10
