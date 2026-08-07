package wsroot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

const trustFileName = "trust.json"

type trustFile struct {
	Trusted []string `json:"trusted"`
}

func trustPath() (string, error) {
	configDir, err := configRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, trustFileName), nil
}

func trustKey(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	return filepath.Clean(abs), nil
}

func readTrust() ([]string, error) {
	path, err := trustPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var file trustFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, nil
	}
	return file.Trusted, nil
}

func writeTrust(roots []string) error {
	path, err := trustPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
	}
	raw, err := json.MarshalIndent(trustFile{Trusted: roots}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode workspace trust: %w", err)
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), trustFileName+".*")
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func IsTrusted(root string) (bool, error) {
	key, err := trustKey(root)
	if err != nil {
		return false, err
	}
	roots, err := readTrust()
	if err != nil {
		return false, err
	}
	return slices.Contains(roots, key), nil
}

func Trust(root string) error {
	key, err := trustKey(root)
	if err != nil {
		return err
	}
	roots, err := readTrust()
	if err != nil {
		return err
	}
	if slices.Contains(roots, key) {
		return nil
	}
	return writeTrust(append(slices.Clone(roots), key))
}

func Revoke(root string) error {
	key, err := trustKey(root)
	if err != nil {
		return err
	}
	roots, err := readTrust()
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(roots))
	for _, r := range roots {
		if r != key {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(roots) {
		return nil
	}
	return writeTrust(kept)
}
