package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/habibulloxon/hr-ai-service/internal/config"
	"github.com/habibulloxon/hr-ai-service/internal/model"
	"github.com/habibulloxon/hr-ai-service/internal/service"
)

// --- fakes implementing the service interfaces ---

type fakeAI struct {
	tech      model.AIResponse
	interview model.InterviewResult
	final     model.AIResponse
	err       error
}

func (f fakeAI) EvaluateTechTask(context.Context, string, string, model.JobData) (model.AIResponse, error) {
	return f.tech, f.err
}

func (f fakeAI) EvaluateInterviewResponse(context.Context, string, string, model.JobData, []string, string) (model.InterviewResult, error) {
	return f.interview, f.err
}

func (f fakeAI) FinalEvaluation(context.Context, string, string, model.JobData, string) (model.AIResponse, error) {
	return f.final, f.err
}

type fakeTranscriber struct {
	transcript string
	err        error
}

func (f fakeTranscriber) Transcribe(context.Context, string, string) (string, error) {
	return f.transcript, f.err
}

type fakeCV struct {
	text string
	err  error
}

func (f fakeCV) FetchAndParse(context.Context, string) (string, error) { return f.text, f.err }

// fakeMedia creates real (tiny) temp files so the handler's file reads succeed.
type fakeMedia struct{}

func (fakeMedia) ExtractAudio(context.Context, string) (string, error) {
	f, err := os.CreateTemp("", "audio_*.wav")
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return f.Name(), nil
}

func (fakeMedia) ExtractFrames(_ context.Context, _ string, n int) ([]string, error) {
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		f, err := os.CreateTemp("", "frame_*.jpg")
		if err != nil {
			return nil, err
		}
		_, _ = f.WriteString("x")
		_ = f.Close()
		paths = append(paths, f.Name())
	}
	return paths, nil
}

func newTestHandlers(ai service.AIClient, stt service.Transcriber, cv service.CVFetcher, media service.MediaProcessor) *Handlers {
	cfg := &config.Config{TranscribeTimeout: time.Minute}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, logger, ai, stt, cv, media)
}

// --- tests ---

func TestPing(t *testing.T) {
	h := newTestHandlers(fakeAI{}, fakeTranscriber{}, fakeCV{}, fakeMedia{})
	rec := httptest.NewRecorder()
	h.Ping(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "pong" {
		t.Errorf("body = %q, want pong", got)
	}
}

func TestTask_OK(t *testing.T) {
	h := newTestHandlers(fakeAI{tech: model.AIResponse{Score: 80, OverallAIFeedback: "good"}}, fakeTranscriber{}, fakeCV{}, fakeMedia{})

	form := url.Values{
		"response": {"my answer"},
		"question": {"the question"},
		"jobData":  {`{"title":"Engineer","required_skills":["go"]}`},
	}
	req := httptest.NewRequest(http.MethodPost, "/task", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.Task(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ai_response"] != "good" {
		t.Errorf("ai_response = %v, want good", body["ai_response"])
	}
	if body["ai_score"].(float64) != 80 {
		t.Errorf("ai_score = %v, want 80", body["ai_score"])
	}
}

func TestTask_MethodNotAllowed(t *testing.T) {
	h := newTestHandlers(fakeAI{}, fakeTranscriber{}, fakeCV{}, fakeMedia{})
	rec := httptest.NewRecorder()
	h.Task(rec, httptest.NewRequest(http.MethodGet, "/task", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestTask_BadJobData(t *testing.T) {
	h := newTestHandlers(fakeAI{}, fakeTranscriber{}, fakeCV{}, fakeMedia{})
	form := url.Values{"response": {"a"}, "question": {"q"}, "jobData": {""}}
	req := httptest.NewRequest(http.MethodPost, "/task", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.Task(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestTranscribe_Final(t *testing.T) {
	h := newTestHandlers(fakeAI{final: model.AIResponse{Score: 91, OverallAIFeedback: "hireable"}}, fakeTranscriber{}, fakeCV{}, fakeMedia{})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("allFeedbacks", "feedback one; feedback two")
	_ = mw.WriteField("jobData", `{"title":"Engineer"}`)
	_ = mw.WriteField("language", "en-US")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/transcribe", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	h.Transcribe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ai_response"] != "hireable" {
		t.Errorf("ai_response = %v, want hireable", body["ai_response"])
	}
	if body["ai_score"].(float64) != 91 {
		t.Errorf("ai_score = %v, want 91", body["ai_score"])
	}
}

func TestTranscribe_Video(t *testing.T) {
	h := newTestHandlers(
		fakeAI{interview: model.InterviewResult{AIResponse: "solid", AdditionalQuestion: "why?"}},
		fakeTranscriber{transcript: "hello world"},
		fakeCV{},
		fakeMedia{},
	)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("question", "tell me about yourself")
	_ = mw.WriteField("jobData", `{"title":"Engineer"}`)
	fw, err := mw.CreateFormFile("video", "clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("fake video bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/transcribe", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	h.Transcribe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["transcript"] != "hello world" {
		t.Errorf("transcript = %v, want 'hello world'", body["transcript"])
	}
	if body["ai_response"] != "solid" {
		t.Errorf("ai_response = %v, want solid", body["ai_response"])
	}
	if body["additional_question"] != "why?" {
		t.Errorf("additional_question = %v, want why?", body["additional_question"])
	}
}

func TestParseJobData(t *testing.T) {
	want := model.JobData{Title: "Engineer", RequiredSkills: []string{"go", "k8s"}}
	inner := `{"title":"Engineer","required_skills":["go","k8s"]}`

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"direct", inner, false},
		{"wrapper", `{"jobData":` + jsonString(inner) + `}`, false},
		{"triple", jsonString(`{"jobData":` + jsonString(inner) + `}`), false},
		{"empty", "", true},
		{"garbage", "not json", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseJobData(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Title != want.Title || len(got.RequiredSkills) != len(want.RequiredSkills) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestNormalizeLanguage(t *testing.T) {
	cases := map[string]string{
		"uz-UZ":   "uz-UZ",
		"ru-RU":   "ru-RU",
		"en-US":   "en-US",
		"":        "en-US",
		"fr-FR":   "en-US",
		"invalid": "en-US",
	}
	for in, want := range cases {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

// jsonString returns s encoded as a JSON string literal (quoted and escaped).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
