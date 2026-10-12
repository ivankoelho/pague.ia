// Package ocr transforma um documento em texto: PDF com texto via pdftotext,
// PDF escaneado/imagem via Tesseract. Tudo roda dentro do container do app.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Extractor permite trocar o mecanismo (OCR em nuvem, outro motor) sem tocar no pipeline.
type Extractor interface {
	Text(ctx context.Context, path, mime string) (string, error)
}

// Local usa poppler-utils + tesseract instalados na imagem.
type Local struct{ Lang string }

func (l Local) Text(ctx context.Context, path, mime string) (string, error) {
	switch {
	case strings.Contains(mime, "xml"), strings.HasPrefix(mime, "text/"):
		b, err := os.ReadFile(path)
		return string(b), err
	case strings.HasPrefix(mime, "image/"):
		return l.tesseract(ctx, path)
	case mime == "application/pdf":
		out, err := run(ctx, "pdftotext", "-layout", path, "-")
		if err == nil && len(strings.TrimSpace(out)) >= 40 {
			return out, nil
		}
		return l.scanned(ctx, path)
	}
	return "", fmt.Errorf("tipo não suportado: %s", mime)
}

// scanned rasteriza as páginas (máx. 5) e passa cada uma no Tesseract.
func (l Local) scanned(ctx context.Context, path string) (string, error) {
	dir, err := os.MkdirTemp("", "ocr")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := run(ctx, "pdftoppm", "-r", "300", "-l", "5", "-png", path, filepath.Join(dir, "p")); err != nil {
		return "", err
	}
	pages, _ := filepath.Glob(filepath.Join(dir, "p*.png"))
	sort.Strings(pages)
	var b strings.Builder
	for _, p := range pages {
		t, err := l.tesseract(ctx, p)
		if err != nil {
			return "", err
		}
		b.WriteString(t + "\n")
	}
	return b.String(), nil
}

func (l Local) tesseract(ctx context.Context, path string) (string, error) {
	lang := l.Lang
	if lang == "" {
		lang = "por+eng"
	}
	return run(ctx, "tesseract", path, "stdout", "-l", lang)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
