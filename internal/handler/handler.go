// Package handler contains the HTTP handlers for the service. Handlers are
// methods on Handlers so their dependencies can be injected and faked in tests.
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/habibulloxon/hr-ai-service/internal/config"
	"github.com/habibulloxon/hr-ai-service/internal/model"
	"github.com/habibulloxon/hr-ai-service/internal/service"
)

const maxMultipartMemory = 100 << 20 // 100 MiB kept in memory before spilling to disk

// Handlers holds the dependencies shared by every HTTP handler.
type Handlers struct {
	cfg   *config.Config
	log   *slog.Logger
	ai    service.AIClient
	stt   service.Transcriber
	cv    service.CVFetcher
	media service.MediaProcessor
}

func New(cfg *config.Config, log *slog.Logger, ai service.AIClient, stt service.Transcriber, cv service.CVFetcher, media service.MediaProcessor) *Handlers {
	return &Handlers{cfg: cfg, log: log, ai: ai, stt: stt, cv: cv, media: media}
}

// Register wires the handlers onto a mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/task", h.Task)
	mux.HandleFunc("/transcribe", h.Transcribe)
	mux.HandleFunc("/ping", h.Ping)
}

func (h *Handlers) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.log.Error("encode response", "err", err)
	}
}

// normalizeLanguage restricts the language to the supported set, defaulting to
// en-US.
func normalizeLanguage(lang string) string {
	switch lang {
	case "uz-UZ", "ru-RU", "en-US":
		return lang
	default:
		return "en-US"
	}
}

// parseJobData decodes the jobData form value. It accepts the value directly, as
// a {"jobData": "<json>"} wrapper, or with one or more extra layers of string
// encoding, unwrapping up to three layers.
func parseJobData(raw string) (model.JobData, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return model.JobData{}, errors.New("jobData is required")
	}

	for i := 0; i < 3; i++ {
		var job model.JobData
		if err := json.Unmarshal([]byte(raw), &job); err == nil && hasJobData(job) {
			return job, nil
		}

		var wrapper struct {
			JobData string `json:"jobData"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapper); err == nil && wrapper.JobData != "" {
			raw = wrapper.JobData
			continue
		}

		var unquoted string
		if err := json.Unmarshal([]byte(raw), &unquoted); err == nil && unquoted != "" {
			raw = unquoted
			continue
		}
		break
	}

	var job model.JobData
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return model.JobData{}, fmt.Errorf("unable to parse jobData: %w", err)
	}
	return job, nil
}

func hasJobData(j model.JobData) bool {
	return j.Title != "" || j.Description != "" || j.EvaluationCriteria != "" || len(j.RequiredSkills) > 0
}
