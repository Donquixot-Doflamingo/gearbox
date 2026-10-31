package objstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FS stores objects as files under a root directory. Writes are atomic
// (temp file + rename). Only blobs/sha256/* keys get download URLs, signed
// with Signer and served by the control plane.
type FS struct {
	root   string
	signer Signer
}

// NewFS creates the root directory if needed.
func NewFS(root string, signer Signer) (*FS, error) {
	if root == "" {
		return nil, errors.New("objstore: empty filesystem root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("objstore: %w", err)
	}
	return &FS{root: abs, signer: signer}, nil
}

func (f *FS) Kind() string { return "file" }

// Root returns the absolute root directory.
func (f *FS) Root() string { return f.root }

func (f *FS) path(key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("objstore: invalid key %q", key)
	}
	return filepath.Join(f.root, filepath.FromSlash(key)), nil
}

func (f *FS) Put(_ context.Context, key string, data []byte) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := p + ".tmp-" + hex.EncodeToString(rnd[:])
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (f *FS) Get(_ context.Context, key string) ([]byte, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return b, err
}

func (f *FS) Exists(_ context.Context, key string) (bool, error) {
	p, err := f.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (f *FS) URL(_ context.Context, key string, ttl time.Duration) (string, error) {
	sha, ok := strings.CutPrefix(key, "blobs/sha256/")
	if !ok {
		return "", fmt.Errorf("objstore: filesystem store only serves blobs, not %q", key)
	}
	return f.signer.BlobURL(sha, ttl), nil
}
