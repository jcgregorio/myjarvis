MODEL     ?= granite4:latest
HA_URL    ?= http://homeassistant.local:8123
HA_TOKEN  ?= $(error HA_TOKEN is required — export HA_TOKEN=<your-long-lived-access-token>)
OLLAMA_URL ?= http://192.168.1.145:11434/v1

GOBIN := $(shell go env GOPATH)/bin
ESPHOME := $(HOME)/.venv/esphome/bin/esphome

.PHONY: run dry-run list tools ollama test test-routing test-synth test-retrieval install flash-kitchen flash-living-room flash-kitchen-test ota ota-kitchen ota-living-room logs deploy restart

install:
	CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include/onnxruntime" CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" go install ./cmd/myjarvis

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

ota: ota-kitchen ota-living-room

ota-kitchen:
	$(ESPHOME) compile esphome/kitchen-voice.yaml
	$(ESPHOME) upload esphome/kitchen-voice.yaml --device kitchen-voice.local

ota-living-room:
	$(ESPHOME) compile esphome/living-room-voice.yaml
	$(ESPHOME) upload esphome/living-room-voice.yaml --device living-room-voice.local

ollama:
	docker compose -f /home/jcgregorio/jcgregorio/homeassistant/docker-compose.yaml up -d ollama
	docker exec ollama ollama pull $(MODEL)

test:
	CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include/onnxruntime" CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" go test ./...

# Live routing + latency suite. Hits the real LLM; skipped by `make test`.
test-routing:
	CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include/onnxruntime" CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" \
	OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu \
	go test -tags integration -run TestRouting -v -timeout 30m ./tests/integration

# Live RAG answer-synthesis quality suite. Hits the LLM + RAG sidecar.
test-synth:
	CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include/onnxruntime" CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" \
	OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu \
	go test -tags integration -run TestSynthesis -v -timeout 30m ./tests/integration

# RAG retrieval-ranking probe: keyword query vs raw question vs both.
test-retrieval:
	CGO_ENABLED=1 CGO_CFLAGS="-I/usr/include/onnxruntime" CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" \
	OLLAMA_URL=$(OLLAMA_URL) MODEL=$(MODEL) LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu \
	go test -tags integration -run TestRetrievalProbe -v -timeout 30m ./tests/integration

deploy:
	docker compose build myjarvis && docker compose up -d myjarvis

restart:
	docker compose restart myjarvis

logs:
	docker logs -f myjarvis 2>&1 | grep -v "^Schema error:"

mqtt:
	docker exec mosquitto mosquitto_sub -t jarvis/#  -F "%t %l"

serial-logs:
	~/.venv/esphome/bin/esphome  logs esphome/kitchen-voice.yaml --device kitchen-voice.local