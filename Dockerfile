# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/raven .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata su-exec \
    && adduser -D -H -u 10001 -s /sbin/nologin raven \
    && mkdir -p /data \
    && chown raven:raven /data

WORKDIR /app
COPY --from=build /out/raven /app/raven
COPY --from=build /src/prompts /app/prompts
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV MEMORY_DIR=/data
ENV PORT=8080

EXPOSE 8080

# Starts as root only to fix volume ownership, then drops to uid 10001.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
