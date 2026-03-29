MODEL     ?= qwen2.5:7b
HA_URL    ?= http://homeassistant.local:8123
HA_TOKEN  ?= $(error HA_TOKEN is required — export HA_TOKEN=<your-long-lived-access-token>)
OLLAMA_URL ?= http://localhost:11434/v1

.PHONY: run dry-run ollama test build

build:
	go build -o jarvis .

run: build
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) ./jarvis

dry-run: build
	HA_URL=$(HA_URL) HA_TOKEN=$(HA_TOKEN) OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) ./jarvis -dry-run

ollama:
	docker compose -f /home/jcgregorio/jcgregorio/homeassistant/docker-compose.yaml up -d ollama
	docker exec ollama ollama pull $(MODEL)

test:
	go test ./...
