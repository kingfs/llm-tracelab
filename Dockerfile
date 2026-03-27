FROM golang:1.25-alpine AS builder

WORKDIR /src

ENV CGO_ENABLED=0

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags="-s -w" -o /out/llm-tracelab ./cmd/server

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tzdata

ENV TZ=UTC \
	LLM_TRACELAB_OUTPUT_DIR=/var/lib/llm-tracelab/traces

RUN addgroup -S llmtracelab \
	&& adduser -S llmtracelab -G llmtracelab -h /home/llmtracelab \
	&& mkdir -p /etc/llm-tracelab /var/lib/llm-tracelab/traces \
	&& chown -R llmtracelab:llmtracelab /etc/llm-tracelab /var/lib/llm-tracelab

WORKDIR /home/llmtracelab

COPY --from=builder /out/llm-tracelab /usr/local/bin/llm-tracelab
COPY config/config.docker.yaml /etc/llm-tracelab/config.yaml

USER llmtracelab

VOLUME ["/etc/llm-tracelab", "/var/lib/llm-tracelab"]

EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/llm-tracelab"]
CMD ["serve", "-c", "/etc/llm-tracelab/config.yaml"]
