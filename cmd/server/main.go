package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/habibulloxon/hr-ai-service/internal/config"
	"github.com/habibulloxon/hr-ai-service/internal/handler"
	"github.com/habibulloxon/hr-ai-service/internal/middleware"
	"github.com/habibulloxon/hr-ai-service/internal/service"

	"github.com/joho/godotenv"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// .env is optional: in production, configuration comes from the environment.
	if err := godotenv.Load(); err != nil {
		logger.Warn("no .env file loaded; relying on the environment", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	ai := service.NewOpenAIService(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.MaxTokensTech, cfg.MaxTokensInterview, cfg.MaxTokensFinal, logger)
	stt := service.NewGCPTranscriber(cfg.GCSBucket, cfg.GCPRecognizer, cfg.SpeechEndpoint, cfg.PollInterval, logger)
	cv := service.NewCVService(cfg.CVDownloadTimeout, cfg.CVMaxBytes, logger)
	media := service.NewFFmpegProcessor()

	h := handler.New(cfg, logger, ai, stt, cv, media)

	mux := http.NewServeMux()
	h.Register(mux)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           middleware.Logging(logger)(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute, // generous: clients upload video
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// Serve until an interrupt/terminate signal arrives, then drain gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
