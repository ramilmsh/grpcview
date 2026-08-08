package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// What a client hands a server it spawns. A hand-run server gets neither, which is the whole
// explicit-vs-auto split: an explicit invocation opens a browser and lives until stopped, an
// auto-spawned one is silent and idles out.
const (
	DefaultIdleTimeout = time.Hour

	probeTimeout    = 2 * time.Second
	startupTimeout  = 10 * time.Second
	shutdownTimeout = 10 * time.Second
	pollInterval    = 25 * time.Millisecond
	logTailBytes    = 4 << 10
)

type Options struct {
	// Absolute workspace root. cwd never crosses the wire: the client resolves, the server obeys.
	Root string
	// What a spawned server is given; zero means it never idles out.
	IdleTimeout time.Duration
	// Notes that are neither data nor failure — a version-skew restart — land here.
	Notes io.Writer
	// Reports a server that is not running rather than starting one. `grpcview shutdown`.
	NoSpawn bool
}

var ErrNotRunning = errors.New("no grpcview server is running for this workspace")

func client(baseURL string) grpcviewv1.ServerServiceClient {
	return grpcviewv1.NewServerServiceClient(http.DefaultClient, baseURL)
}

// Info dials a base URL and returns what the server says it is. The one call that turns a
// registration from a guess into a fact.
func Info(ctx context.Context, baseURL string) (*grpcviewv1.ServerInfoResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	res, err := client(baseURL).ServerInfo(ctx, connect.NewRequest(&grpcviewv1.ServerInfoRequest{}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

type state int

const (
	stateNone state = iota // nothing registered, or what is registered does not answer
	stateOK                // answers, and it is this workspace's, running this binary
	stateSkew              // answers, and it is this workspace's, running a different binary
)

func probe(ctx context.Context, root string) (Registration, state) {
	reg, err := Read(root)
	if err != nil {
		return Registration{}, stateNone
	}
	if !Alive(reg.Pid) {
		return reg, stateNone
	}
	info, err := Info(ctx, reg.URL())
	if err != nil {
		return reg, stateNone
	}
	// Pid reuse and hash collisions both die here, and this is also what `--server` needs:
	// a collection id resolved against one root must never be interpreted against another's.
	if info.GetWorkspaceRoot() != root {
		return reg, stateNone
	}
	// The pid that ANSWERED, not the one the file claims: it is the only one Stop may ever
	// signal. The port stays as read — it is the one this connect succeeded on.
	if pid := int(info.GetPid()); pid > 0 {
		reg.Pid = pid
	}
	if self := SelfExecutable(); self != (Executable{}) && self != executableOf(info) {
		return reg, stateSkew
	}
	return reg, stateOK
}

func executableOf(info *grpcviewv1.ServerInfoResponse) Executable {
	exe := info.GetExecutable()
	return Executable{Path: exe.GetPath(), Modified: exe.GetModifiedUnix(), Size: exe.GetSize()}
}

// Proto is the wire form every server binding reports itself with; executableOf is its inverse.
func (e Executable) Proto() *grpcviewv1.ServerExecutable {
	return &grpcviewv1.ServerExecutable{Path: e.Path, ModifiedUnix: e.Modified, Size: e.Size}
}

// Connect returns this workspace's server, starting one if none is running.
//
// The close race is absorbed rather than reported: a registration read microseconds before the
// server unlinks it fails the probe, which is indistinguishable from staleness and lands in the
// same spawn path — with the lock held and the probe repeated, so a server another client just
// started is reused rather than duplicated.
func Connect(ctx context.Context, opts Options) (Registration, error) {
	if opts.Root == "" {
		return Registration{}, errors.New("daemon: a workspace root is required")
	}

	reg, st := probe(ctx, opts.Root)
	if st == stateOK {
		return reg, nil
	}
	if opts.NoSpawn {
		if st == stateSkew {
			return reg, nil
		}
		return Registration{}, ErrNotRunning
	}

	unlock, err := lock(ctx, opts.Root)
	if err != nil {
		return Registration{}, err
	}
	defer unlock()

	reg, st = probe(ctx, opts.Root)
	switch st {
	case stateOK:
		return reg, nil
	case stateSkew:
		note(opts.Notes, fmt.Sprintf(
			"grpcview: the server for %s is running a different build; restarting it", opts.Root))
		if err := Stop(ctx, reg); err != nil {
			return Registration{}, err
		}
	}

	return spawn(ctx, opts)
}

func note(w io.Writer, msg string) {
	if w != nil {
		fmt.Fprintln(w, msg)
	}
}

// Stop asks a verified server to exit and waits for it to go. A signal is the last resort for a
// process that answered and then refused to leave — never a first move against a bare pid.
func Stop(ctx context.Context, reg Registration) error {
	callCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	_, err := client(reg.URL()).Shutdown(callCtx, connect.NewRequest(&grpcviewv1.ShutdownRequest{}))
	cancel()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("failed to ask the server on %s to exit: %w", reg.URL(), err)
	}

	deadline := time.Now().Add(shutdownTimeout)
	for time.Now().Before(deadline) {
		if !Alive(reg.Pid) {
			return Remove(reg.Root)
		}
		if _, err := Read(reg.Root); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	// It answered, so the pid is the one that answered: signalling it cannot hit a stranger.
	if proc, err := os.FindProcess(reg.Pid); err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	return Remove(reg.Root)
}

// A detached self-exec. The child keeps the write end of a pipe it never learns about: the pipe
// closes when it dies, so a crash is seen in milliseconds instead of at the startup timeout.
func spawn(ctx context.Context, opts Options) (Registration, error) {
	exe, err := os.Executable()
	if err != nil {
		return Registration{}, fmt.Errorf("failed to resolve the grpcview binary: %w", err)
	}

	logPath, err := LogPath(opts.Root)
	if err != nil {
		return Registration{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Registration{}, fmt.Errorf("failed to open the server log %q: %w", logPath, err)
	}
	defer logFile.Close()

	args := SpawnArgs(opts.Root, opts.IdleTimeout)

	pr, pw, err := os.Pipe()
	if err != nil {
		return Registration{}, err
	}
	defer pr.Close()

	cmd := exec.Command(exe, args...)
	cmd.Dir = opts.Root
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.ExtraFiles = []*os.File{pw}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		pw.Close()
		return Registration{}, fmt.Errorf("failed to start a grpcview server: %w", err)
	}
	pw.Close()
	_ = cmd.Process.Release()

	died := make(chan struct{})
	go func() {
		io.Copy(io.Discard, pr)
		close(died)
	}()

	deadline := time.Now().Add(startupTimeout)
	for {
		if reg, st := probe(ctx, opts.Root); st == stateOK || st == stateSkew {
			return reg, nil
		}
		select {
		case <-ctx.Done():
			return Registration{}, ctx.Err()
		case <-died:
			return Registration{}, startupError(opts.Root, "exited", logPath)
		case <-time.After(pollInterval):
		}
		if time.Now().After(deadline) {
			return Registration{}, startupError(opts.Root, "did not come up within "+startupTimeout.String(), logPath)
		}
	}
}

func startupError(root, why, logPath string) error {
	msg := fmt.Sprintf("the grpcview server for %s %s", root, why)
	if tail := tail(logPath); tail != "" {
		msg += "\n" + tail
	}
	return errors.New(msg)
}

func tail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > logTailBytes {
		data = data[len(data)-logTailBytes:]
	}
	return strings.TrimRight(string(data), "\n")
}

// SpawnArgs is the command line a client hands the server it starts. No --port: the server
// takes the default and falls back to an ephemeral one if a sibling workspace already holds it.
func SpawnArgs(root string, idle time.Duration) []string {
	args := []string{"serve", "--workspace", filepath.Clean(root), "--no-open"}
	if idle > 0 {
		args = append(args, "--idle-timeout", idle.String())
	}
	return args
}
