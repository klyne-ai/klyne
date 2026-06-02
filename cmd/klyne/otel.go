package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/otelexport"
	"github.com/klyne-ai/klyne/internal/store"
)

// newOtelCmd registers `klyne otel` with an `emit` sub-command. v1
// ships file/stdout output only — no always-on OTLP push. The user
// chooses when data leaves the machine.
//
// Recommended usage in a team setting:
//
//	klyne otel emit --since=24h --out spans.jsonl
//	# then upload spans.jsonl to your collector of choice
//
// Inspired by ColeMurray/claude-code-otel, but built without the
// OpenTelemetry SDK so the daemon stays dependency-light.
func newOtelCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "otel",
		Short: "Emit OTel-shaped spans for klyne sessions (opt-in, file-only)",
		Long: `Export klyne's session activity as OTel-shaped JSON lines.

v1 ships an explicit emit subcommand only — there is no daemon-side
auto-push, no network in the default path. The user runs the command,
inspects spans.jsonl, and uploads it to a collector if they want to.

Each emitted span carries gen_ai.* attributes (per the OTel GenAI
working-group draft) plus klyne.* resource fields so consumers can
join back to klyne's local database.`,
	}
	c.AddCommand(newOtelEmitCmd())
	return c
}

func newOtelEmitCmd() *cobra.Command {
	var (
		projectPath string
		sinceStr    string
		outPath     string
		limit       int
		rawPaths    bool
	)
	c := &cobra.Command{
		Use:   "emit",
		Short: "Write one OTel-shaped span per assistant message to a file or stdout",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOtelEmit(cmd, projectPath, sinceStr, outPath, limit, rawPaths)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "restrict to one project root")
	c.Flags().StringVar(&sinceStr, "since", "", "Go duration; only messages newer than this")
	c.Flags().StringVar(&outPath, "out", "", "write spans here (default: stdout)")
	c.Flags().IntVar(&limit, "limit", 1000, "max sessions to walk")
	c.Flags().BoolVar(&rawPaths, "raw-paths", false,
		"emit the full absolute project path instead of the redacted basename+hash (leaks local paths to the collector)")
	return c
}

func runOtelEmit(cmd *cobra.Command, projectPath, sinceStr, outPath string, limit int, rawPaths bool) error {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Defaults()
	}
	engine, err := cost.New(cfg)
	if err != nil {
		return fmt.Errorf("cost engine: %w", err)
	}
	db, err := store.Open(cmd.Context(), config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w (run `klyne start` to ingest sessions)", err)
	}
	defer db.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	since := int64(0)
	if sinceStr != "" {
		d, err := time.ParseDuration(sinceStr)
		if err != nil {
			return fmt.Errorf("invalid --since %q: %w", sinceStr, err)
		}
		since = time.Now().Add(-d).UnixMilli()
	}

	var w io.Writer = cmd.OutOrStdout()
	if outPath != "" {
		// 0600: the spans may carry project paths / model usage the user does
		// not want other local accounts to read before they upload it.
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", outPath, err)
		}
		defer f.Close()
		w = f
	}

	// Silence the cost engine's "unknown model" logs — they are noisy
	// for cross-CLI data and the cost figure ends up 0 either way.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prev)

	n, err := otelexport.Emit(ctx, db, engine, w, otelexport.Filter{
		ProjectPath: projectPath,
		SinceMs:     since,
		MaxSessions: limit,
		// Redact paths by default; --raw-paths opts back into the full path.
		RedactPaths: !rawPaths,
	})
	if err != nil {
		return err
	}
	// When writing to a file, log the count to stderr so the user knows
	// it succeeded. When writing to stdout, stay silent (the JSON is the output).
	if outPath != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d spans to %s\n", n, outPath)
	}
	return nil
}
