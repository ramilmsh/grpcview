// Package daemon is the rendezvous between a workspace's server and every client of it:
// the registration file a server publishes after it binds, and the connect-or-spawn a CLI
// runs against it.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

// Executable identifies the binary a server is running. Version strings cannot: an
// unstamped build links "dev" for every rebuild, which is exactly the case that matters.
type Executable struct {
	Path     string `json:"path"`
	Modified int64  `json:"modified_unix"`
	Size     int64  `json:"size"`
}

// SelfExecutable is the identity of the running binary, or the zero value if it cannot be
// stat'd — a server that cannot describe itself publishes nothing rather than a half-truth,
// and a zero identity never compares equal to a real one, so skew detection fails safe.
var SelfExecutable = sync.OnceValue(func() Executable {
	path, err := os.Executable()
	if err != nil {
		return Executable{}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return Executable{}
	}
	return Executable{Path: path, Modified: info.ModTime().UnixNano(), Size: info.Size()}
})

// Registration is a hint, never an authority: everything in it is re-verified over the wire
// before a client trusts it.
type Registration struct {
	Port        int        `json:"port"`
	Pid         int        `json:"pid"`
	Root        string     `json:"workspace_root"`
	Executable  Executable `json:"executable"`
	Version     string     `json:"version"`
	IdleTimeout int64      `json:"idle_timeout_nanos"`
	StartedUnix int64      `json:"started_unix"`
}

func (r Registration) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", r.Port)
}

// Not the workspace, which may be read-only or network-mounted, and not bare /tmp, which is
// mode 1777 and shared between users. GRPCVIEW_CONFIG_DIR wins where it is set so a throwaway
// run (CI, `example:up --isolated`) cannot adopt the developer's real daemon.
func dir() (string, error) {
	if override := os.Getenv(wsroot.ConfigDirEnv); override != "" {
		return filepath.Join(override, "servers"), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the user cache dir: %w", err)
	}
	return filepath.Join(cache, "grpcview", "servers"), nil
}

func key(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return hex.EncodeToString(sum[:]), nil
}

func pathFor(root, ext string) (string, error) {
	base, err := dir()
	if err != nil {
		return "", err
	}
	k, err := key(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, k+ext), nil
}

// Path is the registration file for a workspace root.
func Path(root string) (string, error) { return pathFor(root, ".json") }

// LogPath is where a spawned server's stdio goes; the failure path reads its tail.
func LogPath(root string) (string, error) { return pathFor(root, ".log") }

func lockPath(root string) (string, error) { return pathFor(root, ".lock") }

func ensureDir() (string, error) {
	base, err := dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("failed to create %q: %w", base, err)
	}
	return base, nil
}

// Read returns os.ErrNotExist when no server has registered for this root. A file that does
// not parse is treated the same way: a corrupt hint is no hint.
func Read(root string) (Registration, error) {
	path, err := Path(root)
	if err != nil {
		return Registration{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Registration{}, err
	}
	var reg Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registration{}, fmt.Errorf("%q: %w", path, os.ErrNotExist)
	}
	if reg.Port <= 0 || reg.Root == "" {
		return Registration{}, fmt.Errorf("%q: %w", path, os.ErrNotExist)
	}
	return reg, nil
}

// Write publishes a registration. Written to a temp file and renamed so a reader never sees a
// half-written one.
func Write(reg Registration) error {
	if _, err := ensureDir(); err != nil {
		return err
	}
	path, err := Path(reg.Root)
	if err != nil {
		return err
	}
	data, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".registration-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Remove unlinks a registration; an absent one is a success.
func Remove(root string) error {
	path, err := Path(root)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Alive is the cheap pre-check before a connect, never a licence to signal: signal 0 delivers
// nothing, and pid reuse is caught by the ServerInfo root check rather than here.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}
