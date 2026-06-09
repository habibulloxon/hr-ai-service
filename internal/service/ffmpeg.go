package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// MediaProcessor extracts audio and still frames from a video file. ffmpeg
// subprocesses honour ctx, so a cancelled request stops the work.
type MediaProcessor interface {
	ExtractAudio(ctx context.Context, videoPath string) (string, error)
	ExtractFrames(ctx context.Context, videoPath string, numFrames int) ([]string, error)
}

// FFmpegProcessor is the production MediaProcessor backed by the ffmpeg/ffprobe
// command-line tools.
type FFmpegProcessor struct{}

func NewFFmpegProcessor() FFmpegProcessor { return FFmpegProcessor{} }

// ExtractAudio converts the video to 16kHz mono WAV suitable for speech-to-text.
func (FFmpegProcessor) ExtractAudio(ctx context.Context, videoPath string) (string, error) {
	if err := checkFFmpegInstalled(); err != nil {
		return "", err
	}
	if err := checkFileExists(videoPath); err != nil {
		return "", err
	}

	audioPath := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".wav"

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", videoPath,
		"-vn", // no video
		"-acodec", "pcm_s16le",
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // mono
		audioPath)

	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg extract audio: %w\nOutput: %s", err, string(output))
	}
	return audioPath, nil
}

// ExtractFrames extracts numFrames still JPEGs at random timestamps and returns
// their file paths. The caller is responsible for removing the files.
func (FFmpegProcessor) ExtractFrames(ctx context.Context, videoPath string, numFrames int) ([]string, error) {
	if numFrames <= 0 {
		return nil, fmt.Errorf("numFrames must be positive, got: %d", numFrames)
	}

	duration, err := getVideoDuration(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("get video duration: %w", err)
	}
	if duration < 1.0 {
		return nil, fmt.Errorf("video too short: %f seconds", duration)
	}

	var framePaths []string
	for i := 0; i < numFrames; i++ {
		ts := randomFloat64(0.5, duration-0.5)
		tsStr := fmt.Sprintf("%.3f", math.Max(0, ts))
		framePath := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + fmt.Sprintf("_frame_%d.jpg", i+1)

		cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", tsStr, "-i", videoPath,
			"-frames:v", "1", "-q:v", "2", framePath)

		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg extract frame %d: %w\nOutput: %s", i+1, err, string(output))
		}
		if err := checkFileExists(framePath); err != nil {
			return nil, fmt.Errorf("frame file not created: %s", framePath)
		}
		framePaths = append(framePaths, framePath)
	}
	return framePaths, nil
}

func checkFFmpegInstalled() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return fmt.Errorf("ffprobe not found in PATH: %w", err)
	}
	return nil
}

func checkFileExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", path)
	}
	return nil
}

func fixWebMFile(ctx context.Context, inputPath, outputPath string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-i", inputPath, "-c", "copy", "-avoid_negative_ts", "make_zero", outputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg fix webm: %w\nOutput: %s", err, string(output))
	}
	return nil
}

func getVideoDuration(ctx context.Context, videoPath string) (float64, error) {
	if err := checkFFmpegInstalled(); err != nil {
		return 0, err
	}
	if err := checkFileExists(videoPath); err != nil {
		return 0, err
	}

	// WebM files often have missing duration metadata; remux first if needed.
	if strings.HasSuffix(strings.ToLower(videoPath), ".webm") {
		fixedPath := videoPath + ".fixed.webm"
		if err := fixWebMFile(ctx, videoPath, fixedPath); err == nil {
			defer func() { _ = os.Remove(fixedPath) }()
			videoPath = fixedPath
		}
	}

	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries",
		"format=duration", "-of", "default=noprint_wrappers=1:nokey=1", videoPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration: %w\nOutput: %s", err, string(output))
	}

	durationStr := strings.TrimSpace(string(output))
	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", durationStr, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("invalid duration: %f", duration)
	}
	return duration, nil
}

func randomFloat64(min, max float64) float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	r := float64(binary.LittleEndian.Uint64(b[:])) / (1 << 64)
	return min + r*(max-min)
}
