package potion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const modelRevision = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"

type modelFile struct {
	name   string
	size   int64
	sha256 string
}

var modelFiles = []modelFile{
	{
		name:   "config.json",
		size:   59,
		sha256: "148e5691a6fcc553437156859701fba017a1ba5d340b170f17e0f3668fb861a7",
	},
	{
		name:   "tokenizer.json",
		size:   1024340,
		sha256: "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45",
	},
	{
		name:   "model.safetensors",
		size:   32490072,
		sha256: "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c",
	},
}

const modelURLPrefix = "https://huggingface.co/minishlab/potion-code-16M-v2/resolve/" + modelRevision + "/"

func ensureModelFiles(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("potion: create model cache directory: %w", err)
	}
	for _, file := range modelFiles {
		path := filepath.Join(dir, file.name)
		valid, err := cachedFileIsValid(path, file)
		if err != nil {
			return fmt.Errorf("potion: inspect cached %s: %w", file.name, err)
		}
		if valid {
			continue
		}
		if err := downloadModelFile(ctx, path, file); err != nil {
			return err
		}
	}
	return nil
}

func cachedFileIsValid(path string, file modelFile) (bool, error) {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !stat.Mode().IsRegular() || stat.Size() != file.size {
		return false, nil
	}
	digest, err := sha256File(path)
	if err != nil {
		return false, err
	}
	return digest == file.sha256, nil
}

func downloadModelFile(ctx context.Context, path string, file modelFile) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURLPrefix+file.name, nil)
	if err != nil {
		return fmt.Errorf("potion: build download request for %s: %w", file.name, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("potion: download %s: %w", file.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("potion: download %s: HTTP %s", file.name, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+file.name+".tmp-*")
	if err != nil {
		return fmt.Errorf("potion: create temporary %s: %w", file.name, err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	digest := sha256.New()
	// The expected size bounds the response before it reaches disk, while the
	// final hash prevents accepting a truncated or unexpected model revision.
	reader := io.LimitReader(resp.Body, file.size+1)
	written, err := io.Copy(io.MultiWriter(tmp, digest), reader)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("potion: write %s: %w", file.name, err)
	}
	if written != file.size {
		_ = tmp.Close()
		return fmt.Errorf("potion: downloaded %s has size %d, want %d", file.name, written, file.size)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != file.sha256 {
		_ = tmp.Close()
		return fmt.Errorf("potion: downloaded %s has SHA-256 %s, want %s", file.name, got, file.sha256)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("potion: sync %s: %w", file.name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("potion: close %s: %w", file.name, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("potion: chmod %s: %w", file.name, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("potion: install %s: %w", file.name, err)
	}
	removeTemp = false
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
