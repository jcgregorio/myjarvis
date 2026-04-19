FROM golang:1.25-bookworm AS builder

# Add trixie repo for onnxruntime
RUN echo "deb http://deb.debian.org/debian trixie main" >> /etc/apt/sources.list.d/trixie.list \
    && apt-get update && apt-get install -y --no-install-recommends \
    libonnxruntime-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=1 \
    CGO_CFLAGS="-I/usr/include/onnxruntime" \
    CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime" \
    go build -o /myjarvis .

FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libonnxruntime1.21 \
    git \
    gh \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /myjarvis /usr/local/bin/myjarvis
COPY silero_vad.onnx /data/silero_vad.onnx

ENV VAD_MODEL_PATH=/data/silero_vad.onnx
ENV LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu

ENTRYPOINT ["myjarvis"]
