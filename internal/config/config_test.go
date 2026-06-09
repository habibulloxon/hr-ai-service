package config

import (
	"testing"
	"time"
)

func TestLoad_MissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when OPENAI_API_KEY is unset, got nil")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	// Clear optional vars so defaults apply.
	for _, k := range []string{"PORT", "OPENAI_MODEL", "GCS_BUCKET", "CV_MAX_BYTES", "TRANSCRIBE_TIMEOUT"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.OpenAIModel != "gpt-4.1-mini" {
		t.Errorf("OpenAIModel = %q, want gpt-4.1-mini", cfg.OpenAIModel)
	}
	if cfg.GCSBucket != "hrdev3" {
		t.Errorf("GCSBucket = %q, want hrdev3", cfg.GCSBucket)
	}
	if cfg.CVMaxBytes != 20<<20 {
		t.Errorf("CVMaxBytes = %d, want %d", cfg.CVMaxBytes, 20<<20)
	}
	if cfg.TranscribeTimeout != 5*time.Minute {
		t.Errorf("TranscribeTimeout = %v, want 5m", cfg.TranscribeTimeout)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("PORT", "9000")
	t.Setenv("OPENAI_MODEL", "gpt-4o")
	t.Setenv("CV_MAX_BYTES", "1048576")
	t.Setenv("TRANSCRIBE_TIMEOUT", "90s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "9000" {
		t.Errorf("Port = %q, want 9000", cfg.Port)
	}
	if cfg.OpenAIModel != "gpt-4o" {
		t.Errorf("OpenAIModel = %q, want gpt-4o", cfg.OpenAIModel)
	}
	if cfg.CVMaxBytes != 1048576 {
		t.Errorf("CVMaxBytes = %d, want 1048576", cfg.CVMaxBytes)
	}
	if cfg.TranscribeTimeout != 90*time.Second {
		t.Errorf("TranscribeTimeout = %v, want 90s", cfg.TranscribeTimeout)
	}
}
