// Command zklens is a terminal UI for exploring and administering Apache
// ZooKeeper ensembles.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/hugantdev/zklens/internal/config"
	"github.com/hugantdev/zklens/internal/ui"
	"github.com/hugantdev/zklens/internal/zk"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, pickContextInteractive))
}

// errContextCancelled is returned by pickContextFunc when the user aborts
// the startup context picker; it exits cleanly, unlike a picker failure.
var errContextCancelled = errors.New("context selection cancelled")

// pickContextFunc lets the user choose one of the configured contexts at
// startup.
type pickContextFunc func([]ui.ContextItem) (int, error)

// run wires up config loading and the ZK connection attempt, and returns
// the process exit code. It takes its dependencies as arguments so it can
// be exercised without touching the real environment or process.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer, pickContext pickContextFunc) int {
	cfg, contexts, contextName, err := config.Load(args, getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "zklens:", err)
		return 2
	}

	if config.NeedsPicker(contexts, args, getenv) {
		items := make([]ui.ContextItem, len(contexts))
		for i, c := range contexts {
			items[i] = ui.ContextItem{Name: c.Name, Detail: c.Summary()}
		}
		idx, err := pickContext(items)
		switch {
		case errors.Is(err, errContextCancelled):
			return 0
		case err != nil:
			fmt.Fprintln(stderr, "zklens:", err)
			return 1
		}
		cfg, err = config.ApplyContext(contexts[idx], args, getenv)
		if err != nil {
			fmt.Fprintln(stderr, "zklens:", err)
			return 2
		}
		contextName = contexts[idx].Name
	}

	bannerLines := 0
	if contextName != "" {
		fmt.Fprintf(stdout, "zklens: connecting to %v (context %s)...\n", cfg.Hosts, contextName)
	} else {
		fmt.Fprintf(stdout, "zklens: connecting to %v...\n", cfg.Hosts)
	}
	bannerLines++

	client, err := zk.ConnectAndWait(context.Background(), cfg, zk.DefaultConnectTimeout)
	if err != nil {
		fmt.Fprintln(stderr, "zklens: connection failed:", err)
		return 1
	}
	defer client.Close()

	fmt.Fprintf(stdout, "zklens: connected (session state: %s)\n", client.State())
	bannerLines++

	title := strings.Join(cfg.Hosts, ",")
	if contextName != "" {
		title = contextName
	}
	program := tea.NewProgram(ui.New(client, title), tea.WithAltScreen())
	_, runErr := program.Run()

	// The banner above was written to the normal screen buffer before the
	// TUI switched to the alternate one, so it is untouched by anything the
	// TUI drew; left alone, it would resurface as stale clutter once the
	// alternate screen is torn down and the terminal falls back to the
	// normal buffer. Erase it now that it is no longer useful.
	clearBanner(stdout, bannerLines)

	if runErr != nil {
		fmt.Fprintln(stderr, "zklens:", runErr)
		return 1
	}
	return 0
}

// clearBanner erases the n most recently printed lines from the terminal by
// moving the cursor back up to where they started and clearing everything
// from there to the end of the screen. It is a no-op when stdout is not an
// interactive terminal (e.g. piped or redirected output), since the ANSI
// sequences would otherwise show up as garbage in whatever is capturing it.
func clearBanner(stdout io.Writer, n int) {
	if n <= 0 {
		return
	}
	f, ok := stdout.(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return
	}
	fmt.Fprintf(f, "\x1b[%dA\x1b[J", n)
}

// pickContextInteractive shows the startup context picker and returns the
// chosen index. The user cancelling the picker yields
// errContextCancelled; any other error means the picker could not run
// (e.g. no usable terminal).
func pickContextInteractive(items []ui.ContextItem) (int, error) {
	final, err := tea.NewProgram(ui.NewContextPicker(items), tea.WithAltScreen()).Run()
	if err != nil {
		return 0, fmt.Errorf("context selection failed: %w (pass --context or set %s when not interactive)", err, config.EnvContext)
	}
	picker, ok := final.(ui.ContextPicker)
	if !ok {
		return 0, fmt.Errorf("context selection failed: unexpected picker model %T", final)
	}
	if idx, ok := picker.Result(); ok {
		return idx, nil
	}
	return 0, errContextCancelled
}
