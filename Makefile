MODEL     ?= qwen2.5:7b
HA_URL    ?= http://homeassistant.local:8123
HA_TOKEN  ?= $(error HA_TOKEN is required — export HA_TOKEN=<your-long-lived-access-token>)
OLLAMA_URL ?= http://localhost:11434/v1

GOBIN := $(shell go env GOPATH)/bin
ESPHOME := $(HOME)/.venv/esphome/bin/esphome

.PHONY: run dry-run list tools ollama test install flash-kitchen flash-living-room flash-kitchen-test

install:
	CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include/onnxruntime" CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" go install .

run: install
	bash -c 'HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu $(GOBIN)/myjarvis 2> >(grep -v "^Schema error:" >&2)'

dry-run: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu $(GOBIN)/myjarvis -dry-run

list: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu $(GOBIN)/myjarvis list

tools: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu $(GOBIN)/myjarvis tools

flash-kitchen:
	$(ESPHOME) run esphome/kitchen-voice.yaml --device /dev/ttyACM1

flash-living-room:
	$(ESPHOME) run esphome/living-room-voice.yaml --device /dev/ttyACM1

flash-kitchen-test:
	$(ESPHOME) run esphome/test-official-kitchen.yaml --device /dev/ttyACM1 --device /dev/ttyACM1

ollama:
	docker compose -f /home/jcgregorio/jcgregorio/homeassistant/docker-compose.yaml up -d ollama
	docker exec ollama ollama pull $(MODEL)

test:
	go test ./...
