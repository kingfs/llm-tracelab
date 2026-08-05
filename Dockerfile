FROM golang:1.25-alpine AS builder

WORKDIR /src

ENV CGO_ENABLED=0

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ARG http_proxy
ARG https_proxy
ARG no_proxy
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
ARG BRANCH=unknown

COPY go.mod go.sum ./
RUN go env -w GOPROXY="${GOPROXY}" GOSUMDB="${GOSUMDB}" && \
	go mod download

COPY . .

RUN go build -trimpath \
	-ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.Date=${BUILD_DATE} -X main.Branch=${BRANCH}" \
	-o /out/llm-tracelab ./cmd/server

FROM alpine:3.22 AS runtime

ENV APP_HOME=/app \
	LLM_TRACELAB_CONFIG=/app/config/config.yaml

ENV TZ=UTC \
	LLM_TRACELAB_OUTPUT_DIR=/app/data/traces \
	LLM_TRACELAB_TRACE_OUTPUT_DIR=/app/data/traces

RUN mkdir -p /app/bin /app/config /app/data/traces

WORKDIR /app

COPY --from=builder /out/llm-tracelab /app/bin/llm-tracelab
COPY config/config.yaml /app/config/config.yaml

VOLUME ["/app/config", "/app/data"]

EXPOSE 8080 8081

ENTRYPOINT ["/app/bin/llm-tracelab"]
CMD ["serve", "-c", "/app/config/config.yaml"]
