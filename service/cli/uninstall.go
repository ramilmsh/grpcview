package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

type uninstallOptions struct {
	purge     bool
	binDir    string
	dryRun    bool
	force     bool
	assumeYes bool
}

func newUninstallCmd(s Streams) *cobra.Command {
	var o uninstallOptions

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the grpcview binary, and optionally its state",
		Long: "Deletes every `grpcview` it can find — the usual install locations, whatever\n" +
			"`grpcview` resolves to on PATH, and the binary actually running — plus, with\n" +
			"--purge, everything else it owns: the state directory (workspace trust list,\n" +
			"descriptor cache, run history) and the cache directory (which server serves\n" +
			"which workspace). Repositories are never touched; collections, requests, and\n" +
			"scripts live in them, not here.\n\n" +
			"Nothing is printed but the plan, a confirmation prompt, and what was removed.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(s, o)
		},
	}
	cmd.Flags().BoolVar(&o.purge, "purge", false,
		"also delete everything grpcview owns on disk: the state directory (trust list, descriptor cache, run history) and the cache directory (daemon registrations) — repositories are never touched")
	cmd.Flags().StringVar(&o.binDir, "bin-dir", "", "only look for the binary in DIR")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print what would be deleted, delete nothing")
	cmd.Flags().BoolVar(&o.force, "force", false,
		"delete symlinks, and a state directory that does not look like grpcview's")
	cmd.Flags().BoolVarP(&o.assumeYes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// candidateBinaries lists the places a grpcview binary plausibly lives, deduplicated and in the
// order they're worth reporting. os.Executable() — the binary actually running this command — is
// last: it is the one place this process can be certain of, where the shell version had to guess.
func candidateBinaries(binDir string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = filepath.Clean(abs)
		}
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	if binDir != "" {
		add(filepath.Join(binDir, "grpcview"))
		return out
	}

	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/usr/local/bin", filepath.Join(home, ".local/bin"), "/opt/homebrew/bin", filepath.Join(home, "bin")} {
		add(filepath.Join(dir, "grpcview"))
	}
	if resolved, err := exec.LookPath("grpcview"); err == nil {
		add(resolved)
	}
	if self, err := os.Executable(); err == nil {
		add(self)
	}
	return out
}

func runUninstall(s Streams, o uninstallOptions) error {
	var targets, skipped []string
	for _, candidate := range candidateBinaries(o.binDir) {
		info, err := os.Lstat(candidate)
		if err != nil {
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0 && !o.force:
			skipped = append(skipped, candidate+" (symlink; --force to remove)")
		case info.IsDir():
			skipped = append(skipped, candidate+" (directory)")
		default:
			targets = append(targets, candidate)
		}
	}

	purgeTargets, err := resolvePurgeTargets(s, o)
	if err != nil {
		return err
	}

	if len(targets) == 0 && len(purgeTargets) == 0 {
		fmt.Fprintln(s.Out, "nothing to remove")
		printSkipped(s.Err, skipped)
		return nil
	}

	fmt.Fprintln(s.Out, "will delete:")
	for _, t := range targets {
		fmt.Fprintf(s.Out, "  %s\n", t)
	}
	for _, pt := range purgeTargets {
		fmt.Fprintf(s.Out, "  %s/ (%s)\n", pt.root, pt.contains)
	}
	printSkipped(s.Out, skipped)
	fmt.Fprintln(s.Out)

	if o.dryRun {
		fmt.Fprintln(s.Out, "dry run: nothing deleted")
		return nil
	}

	if !o.assumeYes {
		if err := confirm(s); err != nil {
			return err
		}
	}

	failed := false
	for _, t := range targets {
		if err := os.Remove(t); err != nil {
			fmt.Fprintf(s.Err, "error: could not remove %s (try as root): %v\n", t, err)
			failed = true
			continue
		}
		fmt.Fprintf(s.Out, "removed %s\n", t)
	}
	for _, pt := range purgeTargets {
		if err := os.RemoveAll(pt.root); err != nil {
			fmt.Fprintf(s.Err, "error: could not remove %s/: %v\n", pt.root, err)
			failed = true
			continue
		}
		fmt.Fprintf(s.Out, "removed %s/\n", pt.root)
	}
	if failed {
		return statusError{code: 1, err: errors.New("some targets could not be removed")}
	}

	if !o.purge {
		fmt.Fprintln(s.Out, "state kept; re-run with --purge to delete it too")
	}
	return nil
}

// purgeTarget is one directory --purge deletes: a durable root grpcview owns entirely, guarded
// so it can only ever be exactly that root, never something a careless path could reach outside it.
type purgeTarget struct {
	root     string
	contains string // human description for the "will delete" line
}

// resolvePurgeTargets computes and guards every --purge target. wsroot.ConfigRoot and
// wsroot.CacheRoot are the same calls the rest of the binary uses to place the trust list,
// per-workspace state, and daemon registrations, so this never drifts from where things
// actually get written the way a shell script reimplementing the platform lookups by hand could.
// The two collapse onto the same directory under GRPCVIEW_CONFIG_DIR, in which case there is
// only one target to report and delete, not two.
func resolvePurgeTargets(s Streams, o uninstallOptions) ([]purgeTarget, error) {
	if !o.purge {
		return nil, nil
	}

	configRoot, err := wsroot.ConfigRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the state directory: %w", err)
	}
	cacheRoot, err := wsroot.CacheRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the cache directory: %w", err)
	}

	var targets []purgeTarget
	root, err := guardPurgeRoot(s, o, configRoot, "state directory",
		[]string{"trust.json", "workspaces"}, "trust.json or workspaces/")
	if err != nil {
		return nil, err
	}
	if root != "" {
		targets = append(targets, purgeTarget{root: root, contains: "trust list, descriptor cache, run history"})
	}

	if cacheRoot != configRoot {
		root, err := guardPurgeRoot(s, o, cacheRoot, "cache directory", []string{"servers"}, "servers/")
		if err != nil {
			return nil, err
		}
		if root != "" {
			targets = append(targets, purgeTarget{root: root, contains: "running-server registrations"})
		}
	}
	return targets, nil
}

// guardPurgeRoot checks that root is safe to delete wholesale — a nested directory literally
// named grpcview, neither $HOME nor /grpcview — and, absent --force, that it actually looks like
// one of ours: it holds at least one of markers. Returns "" with no error for a root that simply
// doesn't exist yet.
func guardPurgeRoot(s Streams, o uninstallOptions, root, label string, markers []string, markersDesc string) (string, error) {
	root = filepath.Clean(root)
	if filepath.Base(root) != "grpcview" {
		return "", fmt.Errorf("refusing to delete %s: not a grpcview directory", root)
	}
	home, _ := os.UserHomeDir()
	if root == string(filepath.Separator) || root == home || root == string(filepath.Separator)+"grpcview" {
		return "", fmt.Errorf("refusing to delete %s", root)
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(s.Out, "note: no %s at %s\n", label, root)
			return "", nil
		}
		return "", fmt.Errorf("failed to stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("refusing to delete %s: not a directory", root)
	}

	looksRight := false
	for _, m := range markers {
		if exists(filepath.Join(root, m)) {
			looksRight = true
			break
		}
	}
	if !looksRight && !o.force {
		return "", fmt.Errorf("%s has no %s; --force to delete it anyway", root, markersDesc)
	}
	return root, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func printSkipped(w io.Writer, skipped []string) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintln(w, "skipped:")
	for _, sk := range skipped {
		fmt.Fprintf(w, "  %s\n", sk)
	}
}

func confirm(s Streams) error {
	fmt.Fprint(s.Out, "proceed? [y/N] ")
	reply, err := bufio.NewReader(s.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("aborted: %w", err)
	}
	reply = strings.ToLower(strings.TrimSpace(reply))
	if !strings.HasPrefix(reply, "y") {
		return errors.New("aborted")
	}
	return nil
}
