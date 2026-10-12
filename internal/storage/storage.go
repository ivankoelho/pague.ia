// Package storage guarda arquivos por chave. Local hoje; S3/MinIO implementa a mesma interface.
package storage

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store interface {
	Put(key string, r io.Reader) error
	Open(key string) (io.ReadCloser, error)
	Path(key string) (string, error) // caminho local para ferramentas (OCR); S3 baixaria para tmp
}

type Local struct{ Dir string }

func (l Local) full(key string) string {
	// chaves são geradas pelo sistema (hash); limpa por segurança contra path traversal
	return filepath.Join(l.Dir, filepath.Clean("/"+strings.ReplaceAll(key, "..", "")))
}

func (l Local) Put(key string, r io.Reader) error {
	p := l.full(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".up-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	f.Close()
	return os.Rename(f.Name(), p)
}

func (l Local) Open(key string) (io.ReadCloser, error) { return os.Open(l.full(key)) }

func (l Local) Path(key string) (string, error) { return l.full(key), nil }
