package wsroot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Workspace trust, copied from VS Code's Workspace Trust for exactly the reason VS Code has it.
//
// A committed grpcview.json may name a bazel label, and resolving that label runs `bazel build`
// — arbitrary code from a BUILD file, in the same threat class as a VS Code task. Opening a
// colleague's repo (or a repo off the internet) must therefore not be enough to run it; somebody
// has to say "I trust this folder" first. Everything else grpcview does — reflection, uploads,
// reading a descriptor set off disk — resolves regardless, so trust gates exactly the kinds that
// EXECUTE.
//
// Trust is on the FOLDER, not on its content: a workspace trusted yesterday whose grpcview.json
// gained a new label today is still trusted. Content-hashing the manifest instead would mean a
// prompt on every `git pull`, which trains people to click through it — and it would be security
// theater anyway, because the BUILD files the label reaches are outside the manifest.
//
// The decision is USER state, never repo state: a `trusted: true` a repo could commit about itself
// would be worthless. One file holds every workspace's answer, so revoking one does not have to
// find a per-workspace directory.

// trustFileName is the one file, under the user's config dir next to workspaces/.
const trustFileName = "trust.json"

// trustFile is the on-disk shape. Plain encoding/json rather than a proto: wsroot deliberately has
// no proto dependency (it is the package everything else roots itself against), and a list of
// absolute paths needs nothing a schema would buy.
type trustFile struct {
	Trusted []string `json:"trusted"`
}

// trustPath is <os.UserConfigDir()>/grpcview/trust.json.
func trustPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}
	return filepath.Join(configDir, "grpcview", trustFileName), nil
}

// trustKey is how a root is compared and stored: absolute and cleaned, so ".", a relative path and
// a trailing slash all name the same entry. Symlinks are deliberately NOT resolved — Discover does
// not resolve them either, so a user who opened the workspace through a symlink trusts the path
// they typed, and resolving here would disagree with the root every other call passes in.
func trustKey(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	return filepath.Clean(abs), nil
}

// readTrust returns the stored list. A missing file is an empty list, and so is an unparseable one:
// "not trusted" is the safe answer to "I cannot tell", and the only cost of being wrong is one more
// trust click. Trust overwrites a corrupt file rather than failing forever on it.
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

// writeTrust replaces the file atomically — temp file plus rename — because two grpcview processes
// serving two workspaces share it, and a half-written list would read as "nothing is trusted".
// 0o600: it is a list of the user's own paths and nothing else needs it.
//
// The RENAME is atomic; the read-modify-write around it is NOT, and there is deliberately no lock.
// Two processes trusting two different roots at the same moment both read the old list and the
// second rename wins, so one of the two grants can be lost. That is accepted rather than fixed: the
// losing side fails SAFE (a missing entry means "not trusted", and the fix is one more click), and
// two people granting trust in the same instant is not a workflow anybody has. A lock would add a
// stale-lockfile failure mode to a file whose worst outcome is a re-prompt.
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
	defer func() { _ = os.Remove(tmpName) }() // a no-op once the rename succeeded
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

// IsTrusted reports whether the workspace at root has been trusted. The error is only for "I could
// not tell" (no user config dir, an unreadable file) — a missing or corrupt list is not trusted,
// not an error.
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

// Trust marks root trusted. It is idempotent: trusting an already-trusted root rewrites the same
// list rather than duplicating the entry.
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

// Revoke un-trusts root, and is likewise idempotent. It only refuses FUTURE builds: nothing already
// resolved is un-resolved by it.
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
