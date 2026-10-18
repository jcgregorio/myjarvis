MODEL     ?= gemma4:e2b
HA_URL    ?= http://homeassistant.local:8123
HA_TOKEN  ?= $(error HA_TOKEN is required — export HA_TOKEN=<your-long-lived-access-token>)
OLLAMA_URL ?= http://goldmine-prime:11434/v1

GOBIN := $(shell go env GOPATH)/bin
ESPHOME := $(HOME)/.venv/esphome/bin/esphome

.PHONY: run dry-run list tools ollama test test-routing install flash-kitchen flash-master-bedroom flash-office build-kitchen build-master-bedroom build-office ota ota-kitchen ota-master-bedroom ota-office logs logs-office deploy restart

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
	$(ESPHOME) run esphome/myjarvis-kitchen.yaml --device /dev/ttyACM1

flash-master-bedroom:
	$(ESPHOME) run esphome/myjarvis-master-bedroom.yaml --device /dev/ttyACM1

flash-office:
	$(ESPHOME) run esphome/myjarvis-office.yaml --device /dev/ttyACM1

build-kitchen:
	$(ESPHOME) compile esphome/myjarvis-kitchen.yaml
	cp esphome/.esphome/build/myjarvis-kitchen/.pioenvs/myjarvis-kitchen/firmware.factory.bin /tmp/myjarvis-kitchen-factory.bin
	@echo "Factory binary: /tmp/myjarvis-kitchen-factory.bin"

build-master-bedroom:
	$(ESPHOME) compile esphome/myjarvis-master-bedroom.yaml
	cp esphome/.esphome/build/myjarvis-master-bedroom/.pioenvs/myjarvis-master-bedroom/firmware.factory.bin /tmp/myjarvis-master-bedroom-factory.bin
	@echo "Factory binary: /tmp/myjarvis-master-bedroom-factory.bin"

build-office:
	$(ESPHOME) compile esphome/myjarvis-office.yaml
	cp esphome/.esphome/build/myjarvis-office/.pioenvs/myjarvis-office/firmware.factory.bin /tmp/myjarvis-office-factory.bin
	@echo "Factory binary: /tmp/myjarvis-office-factory.bin"

ota: ota-kitchen ota-master-bedroom ota-office

ota-kitchen:
	$(ESPHOME) compile esphome/myjarvis-kitchen.yaml
	$(ESPHOME) upload esphome/myjarvis-kitchen.yaml --device myjarvis-kitchen.local

ota-master-bedroom:
	$(ESPHOME) compile esphome/myjarvis-master-bedroom.yaml
	$(ESPHOME) upload esphome/myjarvis-master-bedroom.yaml --device myjarvis-master-bedroom.local

ota-office:
	$(ESPHOME) compile esphome/myjarvis-office.yaml
	$(ESPHOME) upload esphome/myjarvis-office.yaml --device myjarvis-office.local

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


deploy:
	docker compose build myjarvis && docker compose up -d myjarvis

# restart does NOT pick up code changes — use deploy for that.
restart:
	docker compose restart myjarvis

logs:
	docker logs -f myjarvis 2>&1 | grep -v "^Schema error:"

mqtt:
	docker exec mosquitto mosquitto_sub -t jarvis/#  -F "%t %l"

serial-logs:
	$(ESPHOME) logs esphome/myjarvis-kitchen.yaml --device myjarvis-kitchen.local

logs-office:
	$(ESPHOME) logs esphome/myjarvis-office.yaml --device myjarvis-office.local