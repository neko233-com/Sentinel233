FROM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/sentinel233 ./cmd/sentinel233
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/sentinel233-agent ./cmd/sentinel233-agent

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget
COPY --from=build /bin/sentinel233 /usr/local/bin/sentinel233
COPY --from=build /bin/sentinel233-agent /usr/local/bin/sentinel233-agent
COPY configs/sentinel233.yaml.example /etc/sentinel233/sentinel233.yaml
EXPOSE 23390 23391
VOLUME /data
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://127.0.0.1:23390/healthz || exit 1
ENTRYPOINT ["sentinel233"]
CMD ["-addr", ":23390", "-data", "/data"]
