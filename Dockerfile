# syntax=docker/dockerfile:1

# --- build stage ---
# go-fitz (PDF parsing) bundles a prebuilt MuPDF static library that references
# C23 glibc symbols (__isoc23_strtol, ...), so the toolchain needs glibc >= 2.38.
# Debian trixie (glibc 2.41) satisfies this; bookworm (glibc 2.36) does not.
FROM golang:1.24-trixie AS build

WORKDIR /src
ENV CGO_ENABLED=1

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -o /out/server ./cmd/server

# --- runtime stage ---
# Use a glibc-based image matching the builder (trixie) and add ffmpeg, which
# the service shells out to for audio/frame extraction.
FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/server /app/server

# Configuration is supplied via environment variables (see README / .env.example).
# Mount the Google credentials JSON and point GOOGLE_APPLICATION_CREDENTIALS at it.
EXPOSE 8080
ENTRYPOINT ["/app/server"]
