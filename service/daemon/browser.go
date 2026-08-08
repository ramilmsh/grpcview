package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// ErrHeadless is a reason to print the URL, never a reason to fail.
var ErrHeadless = errors.New("no display to open a browser on")

// Open launches a browser and names what happened on w. Failing to launch is not a failure:
// a headless box, an SSH session or no DISPLAY gets the URL and carries on.
func Open(w io.Writer, url string) {
	if err := OpenBrowser(url); err != nil {
		fmt.Fprintf(w, "grpcview: %s\n", url)
		return
	}
	fmt.Fprintf(w, "grpcview: opened %s\n", url)
}

// OpenBrowser launches the platform's URL handler.
func OpenBrowser(url string) error {
	if headless() {
		return ErrHeadless
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open a browser on %s: %w", url, err)
	}
	go cmd.Wait()
	return nil
}

// An SSH session counts as headless on macOS too, where `open` would otherwise put a window on
// the console user's display rather than the caller's.
func headless() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return true
	}
	if runtime.GOOS == "darwin" {
		return false
	}
	return os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
}
