package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.sajari.com/docconv"
	"github.com/gen2brain/go-fitz"
	"github.com/nguyenthenguyen/docx"
)

// CVFetcher downloads a CV from a URL and extracts its plain text.
type CVFetcher interface {
	FetchAndParse(ctx context.Context, rawURL string) (string, error)
}

// CVService is the production CVFetcher. It bounds download time and size to
// avoid hanging or exhausting memory on hostile or oversized inputs.
type CVService struct {
	client   *http.Client
	maxBytes int64
	log      *slog.Logger
}

func NewCVService(timeout time.Duration, maxBytes int64, log *slog.Logger) *CVService {
	return &CVService{
		client:   &http.Client{Timeout: timeout},
		maxBytes: maxBytes,
		log:      log,
	}
}

// FetchAndParse downloads the CV at rawURL and returns its cleaned text content.
func (c *CVService) FetchAndParse(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid cv url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported cv url scheme %q", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build cv request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download cv: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download cv: status code %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes))
	if err != nil {
		return "", fmt.Errorf("read cv content: %w", err)
	}

	text, err := parseByFileType(content, strings.ToLower(filepath.Ext(u.Path)))
	if err != nil {
		return "", fmt.Errorf("parse cv: %w", err)
	}

	return cleanCVText(text), nil
}

func parseByFileType(content []byte, ext string) (string, error) {
	reader := bytes.NewReader(content)

	switch ext {
	case ".pdf":
		return parsePDF(content)
	case ".docx":
		return parseDocx(content)
	case ".doc":
		text, _, err := docconv.ConvertDoc(reader)
		if err != nil {
			return "", fmt.Errorf("parse DOC: %w", err)
		}
		return text, nil
	case ".txt":
		return string(content), nil
	case ".rtf":
		text, _, err := docconv.ConvertRTF(reader)
		if err != nil {
			return "", fmt.Errorf("parse RTF: %w", err)
		}
		return text, nil
	default:
		// Unknown extension: try the parsers in turn and fall back to raw text.
		if text, err := parsePDF(content); err == nil && text != "" {
			return text, nil
		}
		if text, err := parseDocx(content); err == nil && text != "" {
			return text, nil
		}
		if _, err := reader.Seek(0, io.SeekStart); err == nil {
			if text, _, err := docconv.ConvertDoc(reader); err == nil && text != "" {
				return text, nil
			}
		}
		return string(content), nil
	}
}

// parsePDF extracts text from a PDF using go-fitz, which works on files, so the
// content is written to a temporary file first.
func parsePDF(content []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "cv_*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	defer func() { _ = tmpFile.Close() }()

	if _, err := tmpFile.Write(content); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}

	doc, err := fitz.New(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	defer func() { _ = doc.Close() }()

	var b strings.Builder
	for i := 0; i < doc.NumPage(); i++ {
		text, err := doc.Text(i)
		if err != nil {
			continue
		}
		b.WriteString(text)
		b.WriteString("\n")
	}

	if b.Len() == 0 {
		return "", fmt.Errorf("no text extracted from PDF")
	}
	return b.String(), nil
}

func parseDocx(content []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "cv_*.docx")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	defer func() { _ = tmpFile.Close() }()

	if _, err := tmpFile.Write(content); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}

	doc, err := docx.ReadDocxFile(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("read DOCX: %w", err)
	}
	defer func() { _ = doc.Close() }()

	return doc.Editable().GetContent(), nil
}

// cleanCVText drops blank lines and collapses runs of whitespace within each
// line to a single space.
func cleanCVText(text string) string {
	lines := strings.Split(text, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if fields := strings.Fields(line); len(fields) > 0 {
			clean = append(clean, strings.Join(fields, " "))
		}
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}
