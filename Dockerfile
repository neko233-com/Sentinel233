# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/sentinel233-server ./cmd/sentinel233-server
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/sentinel233-agent ./cmd/sentinel233-agent

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata wget
COPY --from=build /bin/sentinel233-server /usr/local/bin/sentinel233-server
COPY --from=build /bin/sentinel233-agent /usr/local/bin/sentinel233-agent
COPY configs/sentinel233.yaml.example /etc/sentinel233/sentinel233.yaml
EXPOSE 23390 23391
VOLUME /data
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://127.0.0.1:23390/healthz || exit 1
ENTRYPOINT ["sentinel233-server"]
CMD ["-addr", ":23390", "-data", "/data"]
