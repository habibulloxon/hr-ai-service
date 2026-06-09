package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	speech "cloud.google.com/go/speech/apiv2"
	speechpb "cloud.google.com/go/speech/apiv2/speechpb"
	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	audioPrefix      = "audio-files/"
	transcriptPrefix = "transcripts/"
)

// Transcriber turns a local audio file into a text transcript.
type Transcriber interface {
	Transcribe(ctx context.Context, localFilePath, language string) (string, error)
}

// GCPTranscriber is the production Transcriber backed by Google Cloud Storage
// and the Speech-to-Text v2 batch API.
type GCPTranscriber struct {
	bucket     string
	recognizer string
	endpoint   string
	pollEvery  time.Duration
	log        *slog.Logger
}

func NewGCPTranscriber(bucket, recognizer, endpoint string, pollEvery time.Duration, log *slog.Logger) *GCPTranscriber {
	if pollEvery <= 0 {
		pollEvery = 5 * time.Second
	}
	return &GCPTranscriber{
		bucket:     bucket,
		recognizer: recognizer,
		endpoint:   endpoint,
		pollEvery:  pollEvery,
		log:        log,
	}
}

// Transcribe uploads localFilePath to GCS, runs batch recognition, downloads the
// result and returns the transcript. Temporary GCS objects are always cleaned up.
func (s *GCPTranscriber) Transcribe(ctx context.Context, localFilePath, language string) (string, error) {
	objectName := audioPrefix + localFilePath

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("create storage client: %w", err)
	}
	defer storageClient.Close()

	// Cleanup runs even if the request context is cancelled, so use a detached
	// context with its own deadline.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.deleteObject(cleanupCtx, storageClient, objectName); err != nil {
			s.log.Warn("cleanup: delete audio object", "object", objectName, "err", err)
		}
		if err := s.deletePrefix(cleanupCtx, storageClient, transcriptPrefix); err != nil {
			s.log.Warn("cleanup: delete transcripts", "err", err)
		}
	}()

	s.log.Info("uploading audio to GCS", "object", objectName)
	if err := s.uploadFile(ctx, storageClient, objectName, localFilePath); err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}

	speechClient, err := speech.NewClient(ctx, option.WithEndpoint(s.endpoint))
	if err != nil {
		return "", fmt.Errorf("create speech client: %w", err)
	}
	defer speechClient.Close()

	fileURI := fmt.Sprintf("gs://%s/%s%s", s.bucket, audioPrefix, localFilePath)
	outputURI := fmt.Sprintf("gs://%s/%s", s.bucket, transcriptPrefix)

	req := &speechpb.BatchRecognizeRequest{
		Recognizer: s.recognizer,
		Config: &speechpb.RecognitionConfig{
			DecodingConfig: &speechpb.RecognitionConfig_ExplicitDecodingConfig{
				ExplicitDecodingConfig: &speechpb.ExplicitDecodingConfig{
					Encoding:          speechpb.ExplicitDecodingConfig_LINEAR16,
					SampleRateHertz:   16000,
					AudioChannelCount: 1,
				},
			},
			Model:         "chirp_2",
			LanguageCodes: []string{language},
			Features: &speechpb.RecognitionFeatures{
				EnableWordTimeOffsets: true,
				EnableWordConfidence:  true,
			},
		},
		Files: []*speechpb.BatchRecognizeFileMetadata{
			{AudioSource: &speechpb.BatchRecognizeFileMetadata_Uri{Uri: fileURI}},
		},
		RecognitionOutputConfig: &speechpb.RecognitionOutputConfig{
			Output: &speechpb.RecognitionOutputConfig_GcsOutputConfig{
				GcsOutputConfig: &speechpb.GcsOutputConfig{Uri: outputURI},
			},
		},
	}

	s.log.Info("starting batch recognition", "language", language)
	operation, err := speechClient.BatchRecognize(ctx, req)
	if err != nil {
		return "", fmt.Errorf("start batch recognize: %w", err)
	}

	if err := s.waitForOperation(ctx, speechClient, operation.Name()); err != nil {
		return "", fmt.Errorf("wait for operation: %w", err)
	}

	transcript, err := s.downloadResults(ctx, storageClient, transcriptPrefix)
	if err != nil {
		return "", fmt.Errorf("download results: %w", err)
	}

	return transcript, nil
}

// waitForOperation polls the long-running operation until it completes,
// returning early if ctx is cancelled (e.g. the caller's deadline elapses).
func (s *GCPTranscriber) waitForOperation(ctx context.Context, client *speech.Client, operationName string) error {
	ticker := time.NewTicker(s.pollEvery)
	defer ticker.Stop()

	for {
		op, err := client.LROClient.GetOperation(ctx, &longrunningpb.GetOperationRequest{Name: operationName})
		if err != nil {
			return fmt.Errorf("get operation status: %w", err)
		}
		if op.Done {
			if op.GetError() != nil {
				return fmt.Errorf("operation failed: %v", op.GetError())
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *GCPTranscriber) uploadFile(ctx context.Context, client *storage.Client, objectName, fileName string) error {
	f, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer f.Close()

	wc := client.Bucket(s.bucket).Object(objectName).NewWriter(ctx)
	if _, err := io.Copy(wc, f); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

// downloadResults reads every transcript JSON under prefix and concatenates the
// recognized text.
func (s *GCPTranscriber) downloadResults(ctx context.Context, client *storage.Client, prefix string) (string, error) {
	bucket := client.Bucket(s.bucket)
	it := bucket.Objects(ctx, &storage.Query{Prefix: prefix})

	var transcript string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", fmt.Errorf("iterate objects: %w", err)
		}
		if attrs.Name == prefix || attrs.Name == prefix+"/" {
			continue
		}

		reader, err := bucket.Object(attrs.Name).NewReader(ctx)
		if err != nil {
			s.log.Warn("read transcript object", "object", attrs.Name, "err", err)
			continue
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			s.log.Warn("read transcript data", "object", attrs.Name, "err", err)
			continue
		}

		transcript += extractTranscript(data)
	}

	return transcript, nil
}

// extractTranscript pulls the concatenated transcript text out of a Speech-to-Text
// batch result document.
func extractTranscript(data []byte) string {
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return ""
	}

	results, ok := result["results"].([]interface{})
	if !ok {
		return ""
	}

	var out string
	for _, res := range results {
		resMap, ok := res.(map[string]interface{})
		if !ok {
			continue
		}
		alternatives, ok := resMap["alternatives"].([]interface{})
		if !ok || len(alternatives) == 0 {
			continue
		}
		alt, ok := alternatives[0].(map[string]interface{})
		if !ok {
			continue
		}
		if t, ok := alt["transcript"].(string); ok {
			out += t + "\n"
		}
	}
	return out
}

func (s *GCPTranscriber) deleteObject(ctx context.Context, client *storage.Client, objectName string) error {
	return client.Bucket(s.bucket).Object(objectName).Delete(ctx)
}

func (s *GCPTranscriber) deletePrefix(ctx context.Context, client *storage.Client, prefix string) error {
	bucket := client.Bucket(s.bucket)
	it := bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("list objects: %w", err)
		}
		if err := bucket.Object(attrs.Name).Delete(ctx); err != nil {
			s.log.Warn("delete object", "object", attrs.Name, "err", err)
		}
	}
	return nil
}
