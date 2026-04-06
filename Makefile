MODEL     ?= qwen2.5:7b
HA_URL    ?= http://homeassistant.local:8123
HA_TOKEN  ?= $(error HA_TOKEN is required — export HA_TOKEN=<your-long-lived-access-token>)
OLLAMA_URL ?= http://localhost:11434/v1

GOBIN := $(shell go env GOPATH)/bin
ESPHOME := $(HOME)/.venv/esphome/bin/esphome

.PHONY: run dry-run list tools ollama test install flash-kitchen

install:
	go install .

run: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) $(GOBIN)/myjarvis

dry-run: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) $(GOBIN)/myjarvis -dry-run

list: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) $(GOBIN)/myjarvis list

tools: install
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) $(GOBIN)/myjarvis tools

flash-kitchen:
	$(ESPHOME) run esphome/kitchen-voice.yaml --device /dev/ttyACM1

ollama:
	docker compose -f /home/jcgregorio/jcgregorio/homeassistant/docker-compose.yaml up -d ollama
	docker exec ollama ollama pull $(MODEL)

test:
	go test ./...
