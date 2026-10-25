// Command taktaplus runs every TaktaPlus service from one binary, so all of
// them share the same version and license code.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/farshadfighter/taktaplus/internal/api"
	"github.com/farshadfighter/taktaplus/internal/buildinfo"
	"github.com/farshadfighter/taktaplus/internal/config"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/store"
	"github.com/farshadfighter/taktaplus/web"
)

type service struct {
	summary string
	phase   int // non-zero: planned for that phase, not implemented yet
	run     func(ctx context.Context, cfg config.Config, log *slog.Logger) error
}

var order = []string{"api", "migrate", "worker", "fileserver", "radius", "smtpgw", "collector", "version"}

var services = map[string]service{
	"api":        {summary: "web UI and REST API", run: runAPI},
	"migrate":    {summary: "apply database migrations and exit", run: runMigrate},
	"worker":     {summary: "background jobs and schedules", phase: 1},
	"fileserver": {summary: "built-in TFTP/FTP server", phase: 2},
	"radius":     {summary: "RADIUS server for SMS two-factor", phase: 3},
	"smtpgw":     {summary: "email-to-SMS gateway", phase: 3},
	"collector":  {summary: "syslog collector", phase: 4},
	"version":    {summary: "print the version"},
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: taktaplus <command>\n\ncommands:")
	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	for _, name := range order {
		fmt.Fprintf(w, "  %s\t%s\n", name, services[name].summary)
	}
	w.Flush()
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]
	svc, ok := services[name]
	if !ok {
		usage()
		os.Exit(2)
	}
	if name == "version" {
		fmt.Println(buildinfo.String())
		return
	}
	if svc.phase != 0 {
		fmt.Fprintf(os.Stderr, "taktaplus %s: not implemented yet (planned for phase %d)\n", name, svc.phase)
		os.Exit(1)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		fmt.Fprintln(os.Stderr, "config: TAKTAPLUS_LOG_LEVEL:", err)
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", name)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := svc.run(ctx, cfg, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func runMigrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return store.Migrate(ctx, pool, log)
}

func runAPI(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	identity, err := license.LoadOrCreateIdentity(cfg.DataDir)
	if err != nil {
		return err
	}
	ring, err := license.TrustedKeys()
	if err != nil {
		return err
	}
	if len(ring) == 0 {
		log.Warn("no trusted license keys are built in; licenses cannot be installed")
	}
	log.Info("installation identity", "installation_id", identity.ID())

	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool, log); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(log, pool, web.App()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "version", buildinfo.Version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
