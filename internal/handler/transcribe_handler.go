package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/habibulloxon/hr-ai-service/internal/model"
)

// Transcribe handles interview processing. With an allFeedbacks value it returns
// a final aggregated evaluation; otherwise it transcribes the uploaded video and
// evaluates that single response.
func (h *Handlers) Transcribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		http.Error(w, "failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	allFeedbacks := r.FormValue("allFeedbacks")
	isFinal := allFeedbacks != ""
	question := r.FormValue("question")
	language := normalizeLanguage(r.FormValue("language"))

	job, err := parseJobData(r.FormValue("jobData"))
	if err != nil {
		http.Error(w, "failed to parse job data: "+err.Error(), http.StatusBadRequest)
		return
	}

	var cvContent string
	if cvLink := r.FormValue("cvLink"); cvLink != "" {
		cvContent, err = h.cv.FetchAndParse(r.Context(), cvLink)
		if err != nil {
			h.log.Error("parse cv", "err", err)
			http.Error(w, "failed to parse CV: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if isFinal {
		result, err := h.ai.FinalEvaluation(r.Context(), allFeedbacks, cvContent, job, language)
		if err != nil {
			h.log.Error("final evaluation", "err", err)
			http.Error(w, "failed to process final evaluation: "+err.Error(), http.StatusInternalServerError)
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{
			"ai_response": result.OverallAIFeedback,
			"ai_score":    result.Score,
		})
		return
	}

	h.processInterviewVideo(w, r, question, job, cvContent, language)
}

// processInterviewVideo extracts audio and frames from the uploaded video,
// transcribes the audio, and asks the AI for feedback. The whole pipeline is
// bounded by the configured transcribe timeout.
func (h *Handlers) processInterviewVideo(w http.ResponseWriter, r *http.Request, question string, job model.JobData, cvContent, language string) {
	file, header, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "failed to get video file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	tmpVideoPath, err := saveTempUpload(file, header.Filename)
	if err != nil {
		http.Error(w, "failed to save video file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = os.Remove(tmpVideoPath) }()

	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.TranscribeTimeout)
	defer cancel()

	audioPath, err := h.media.ExtractAudio(ctx, tmpVideoPath)
	if err != nil {
		h.writeMediaError(w, "failed to extract audio", err)
		return
	}
	defer func() { _ = os.Remove(audioPath) }()

	framePaths, err := h.media.ExtractFrames(ctx, tmpVideoPath, 5)
	if err != nil {
		h.writeMediaError(w, "failed to extract frames", err)
		return
	}
	defer removeFiles(framePaths)

	base64Frames, err := framesToBase64(framePaths)
	if err != nil {
		http.Error(w, "failed to process frames: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// The transcriber uploads by relative path, so place the audio in the working
	// directory under a unique name and clean it up afterwards.
	cwd, err := os.Getwd()
	if err != nil {
		http.Error(w, "failed to resolve working directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	baseAudioName := fmt.Sprintf("audio_%d_%d.wav", time.Now().UnixNano(), rand.Intn(10000))
	localAudioPath := filepath.Join(cwd, baseAudioName)
	if err := copyFile(audioPath, localAudioPath); err != nil {
		http.Error(w, "failed to stage audio file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = os.Remove(localAudioPath) }()

	transcript, err := h.stt.Transcribe(ctx, baseAudioName, language)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "transcription timed out", http.StatusGatewayTimeout)
			return
		}
		h.log.Error("transcribe", "err", err)
		http.Error(w, "failed to transcribe audio: "+err.Error(), http.StatusInternalServerError)
		return
	}

	result, err := h.ai.EvaluateInterviewResponse(ctx, transcript, question, job, base64Frames, language)
	if err != nil {
		h.log.Error("evaluate interview response", "err", err)
		http.Error(w, "failed to process transcript: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"transcript":          transcript,
		"ai_response":         result.AIResponse,
		"additional_question": result.AdditionalQuestion,
	})
}

func (h *Handlers) writeMediaError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		http.Error(w, "request timed out", http.StatusGatewayTimeout)
		return
	}
	h.log.Error(msg, "err", err)
	http.Error(w, msg+": "+err.Error(), http.StatusInternalServerError)
}

// saveTempUpload streams an uploaded file to a uniquely named temp file and
// returns its path.
func saveTempUpload(file multipart.File, filename string) (string, error) {
	ext := filepath.Ext(filename)
	name := fmt.Sprintf("video_%d_%d%s", time.Now().UnixNano(), rand.Intn(10000), ext)
	path := filepath.Join(os.TempDir(), name)

	out, err := os.Create(path)
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(out, file); err != nil {
		_ = out.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func framesToBase64(framePaths []string) ([]string, error) {
	frames := make([]string, 0, len(framePaths))
	for _, framePath := range framePaths {
		data, err := os.ReadFile(framePath)
		if err != nil {
			return nil, fmt.Errorf("read frame %s: %w", framePath, err)
		}
		frames = append(frames, base64.StdEncoding.EncodeToString(data))
	}
	return frames, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func removeFiles(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}
