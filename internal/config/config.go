// Package config loads and validates runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the service. Values are read once
// at startup via Load and then passed explicitly to the components that need
// them, so nothing reaches for os.Getenv at request time.
type Config struct {
	// HTTP server
	Port string

	// OpenAI
	OpenAIAPIKey       string
	OpenAIModel        string
	MaxTokensTech      int64
	MaxTokensInterview int64
	MaxTokensFinal     int64

	// Google Cloud Speech-to-Text
	GCSBucket      string
	GCPRecognizer  string
	SpeechEndpoint string

	// Limits / timeouts
	CVMaxBytes        int64
	CVDownloadTimeout time.Duration
	TranscribeTimeout time.Duration
	PollInterval      time.Duration
}

// Load reads configuration from the environment, applying defaults for optional
// values and returning an error listing any required values that are missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:               getEnv("PORT", "8080"),
		OpenAIAPIKey:       os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:        getEnv("OPENAI_MODEL", "gpt-4.1-mini"),
		MaxTokensTech:      getEnvInt("OPENAI_MAX_TOKENS_TECH", 500),
		MaxTokensInterview: getEnvInt("OPENAI_MAX_TOKENS_INTERVIEW", 600),
		MaxTokensFinal:     getEnvInt("OPENAI_MAX_TOKENS_FINAL", 500),
		GCSBucket:          getEnv("GCS_BUCKET", "hrdev3"),
		GCPRecognizer:      getEnv("GCP_RECOGNIZER", "projects/ewd-ai-dev/locations/us-central1/recognizers/_"),
		SpeechEndpoint:     getEnv("SPEECH_ENDPOINT", "us-central1-speech.googleapis.com:443"),
		CVMaxBytes:         getEnvInt("CV_MAX_BYTES", 20<<20), // 20 MiB
		CVDownloadTimeout:  getEnvDuration("CV_DOWNLOAD_TIMEOUT", 30*time.Second),
		TranscribeTimeout:  getEnvDuration("TRANSCRIBE_TIMEOUT", 5*time.Minute),
		PollInterval:       getEnvDuration("TRANSCRIBE_POLL_INTERVAL", 5*time.Second),
	}

	var missing []string
	if cfg.OpenAIAPIKey == "" {
		missing = append(missing, "OPENAI_API_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
