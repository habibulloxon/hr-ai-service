# hr-ai-service — HR AI Microservice

A Go microservice that scores job candidates. It:

- transcribes interview videos (FFmpeg → Google Cloud Speech-to-Text),
- parses candidate CVs (PDF / DOCX / DOC / RTF / TXT),
- and uses an OpenAI chat model to grade tech-task answers and interview responses.

It is designed to run **internally, behind an API gateway** — the gateway is
expected to handle authentication, rate limiting, TLS, and CORS.

## Architecture

```
cmd/server          process entry point: config, logging, wiring, graceful shutdown
internal/config     environment-driven configuration (loaded & validated once)
internal/model      shared request/response data types
internal/service    AIClient (OpenAI), Transcriber (GCP), CVFetcher, MediaProcessor (ffmpeg)
internal/handler    thin HTTP handlers, dependencies injected for testability
internal/middleware structured request logging
```

The service layer is defined behind interfaces (`AIClient`, `Transcriber`,
`CVFetcher`, `MediaProcessor`) so handlers can be unit-tested with fakes.

## Endpoints

| Method | Path         | Purpose |
|--------|--------------|---------|
| GET    | `/ping`      | Liveness check (returns `pong`) |
| POST   | `/task`      | Score a written technical-task answer |
| POST   | `/transcribe`| Transcribe + evaluate an interview video, or produce a final evaluation |

### `POST /task`

Form fields: `response`, `question`, `jobData` (JSON).

```bash
curl -X POST http://localhost:8080/task \
  -F 'response=I would use a worker pool...' \
  -F 'question=How would you parallelise this?' \
  -F 'jobData={"title":"Backend Engineer","required_skills":["go","postgres"]}'
# => {"ai_response":"...","ai_score":82}
```

### `POST /transcribe`

Multipart form. Two modes:

- **Per-response** — send a `video` file (plus `question`, `jobData`, optional
  `cvLink`, `language`). Returns `{transcript, ai_response, additional_question}`.
- **Final evaluation** — send `allFeedbacks` (and `jobData`, optional `cvLink`).
  Skips video processing. Returns `{ai_response, ai_score}`.

`language` accepts `uz-UZ`, `ru-RU`, or `en-US` (default `en-US`).

```bash
curl -X POST http://localhost:8080/transcribe \
  -F 'video=@interview.mp4' \
  -F 'question=Tell me about a hard bug you fixed' \
  -F 'jobData={"title":"Backend Engineer","required_skills":["go"]}' \
  -F 'language=en-US'
```

## Configuration

All configuration comes from environment variables (a local `.env` file is
loaded if present). See `.env.example`.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OPENAI_API_KEY` | **yes** | – | OpenAI API key |
| `PORT` | no | `8080` | HTTP listen port |
| `OPENAI_MODEL` | no | `gpt-4.1-mini` | Chat model |
| `OPENAI_MAX_TOKENS_TECH` | no | `500` | Max tokens, tech-task evaluation |
| `OPENAI_MAX_TOKENS_INTERVIEW` | no | `600` | Max tokens, interview evaluation |
| `OPENAI_MAX_TOKENS_FINAL` | no | `500` | Max tokens, final evaluation |
| `GCS_BUCKET` | no | `hrdev3` | GCS bucket for audio + transcripts |
| `GCP_RECOGNIZER` | no | `projects/ewd-ai-dev/locations/us-central1/recognizers/_` | Speech-to-Text recognizer |
| `SPEECH_ENDPOINT` | no | `us-central1-speech.googleapis.com:443` | Speech-to-Text endpoint |
| `CV_MAX_BYTES` | no | `20971520` | Max CV download size (bytes) |
| `CV_DOWNLOAD_TIMEOUT` | no | `30s` | CV download timeout |
| `TRANSCRIBE_TIMEOUT` | no | `5m` | End-to-end transcription deadline |
| `TRANSCRIBE_POLL_INTERVAL` | no | `5s` | Speech operation poll interval |
| `GOOGLE_APPLICATION_CREDENTIALS` | yes\* | – | Path to GCP service-account JSON (\*required for `/transcribe`) |

## Prerequisites

- Go 1.24+
- `ffmpeg` and `ffprobe` on `PATH`
- A Google Cloud service-account JSON (for Speech-to-Text + Storage)
- An OpenAI API key

## Running

```bash
make run                 # go run ./cmd/server
make build && ./bin/server
```

With Docker (ffmpeg is bundled in the image):

```bash
make docker              # docker build -t hr-microservice .
docker run --rm -p 8080:8080 \
  -e OPENAI_API_KEY=sk-... \
  -e GOOGLE_APPLICATION_CREDENTIALS=/secrets/gcp.json \
  -v /path/to/gcp.json:/secrets/gcp.json:ro \
  hr-microservice
```

## Development

```bash
make fmt      # gofmt
make vet      # go vet
make lint     # golangci-lint (v2)
make test     # go test ./...
```