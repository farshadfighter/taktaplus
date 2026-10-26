// Command taktaplus runs every TaktaPlus service from one binary, so all of
// them share the same version and license code.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/api"
	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/buildinfo"
	"github.com/farshadfighter/taktaplus/internal/config"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/monitor"
	"github.com/farshadfighter/taktaplus/internal/store"
	"github.com/farshadfighter/taktaplus/web"
)

type service struct {
	summary string
	phase   int // non-zero: planned for that phase, not implemented yet
	run     func(ctx context.Context, cfg config.Config, log *slog.Logger) error
}

var order = []string{"api", "migrate", "create-admin", "worker", "fileserver", "radius", "smtpgw", "collector", "version"}

var services = map[string]service{
	"api":          {summary: "web UI and REST API", run: runAPI},
	"migrate":      {summary: "apply database migrations and exit", run: runMigrate},
	"create-admin": {summary: "create a superadmin: create-admin -username U [-name N]", run: runCreateAdmin},
	"worker":       {summary: "background jobs and schedules", phase: 1},
	"fileserver":   {summary: "built-in TFTP/FTP server", phase: 2},
	"radius":       {summary: "RADIUS server for SMS two-factor", phase: 3},
	"smtpgw":       {summary: "email-to-SMS gateway", phase: 3},
	"collector":    {summary: "syslog collector", phase: 4},
	"version":      {summary: "print the version"},
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

	authSvc := auth.NewService(pool, cfg.MasterKey)
	guard := license.NewGuard(ring, identity)
	inv := inventory.New(pool, cfg.MasterKey, guard)
	lic := license.NewManager(guard, pool, log, cfg.LicenseServer, cfg.HostRoot, inv.Usage)
	if err := lic.Load(ctx); err != nil {
		return err
	}
	go lic.Run(ctx)
	alertSvc := alerts.New(pool, cfg.MasterKey, inv, guard, log.With("component", "alerts"))
	mon := monitor.New(inv, alertSvc, log.With("component", "monitor"), monitor.Options{})
	go mon.Run(ctx)
	backups := backup.New(pool, cfg.MasterKey, inv, filepath.Join(cfg.DataDir, "backups"), log.With("component", "backup"), backup.Options{})
	if err := backups.RecoverJobs(ctx); err != nil {
		return err
	}
	go backups.Scheduler(ctx)

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.New(api.Deps{
			Log: log, DB: pool, Auth: authSvc, License: lic, UI: web.App(), CookieSecure: cfg.CookieSecure,
			Inventory: inv, Alerts: alertSvc, Monitor: mon, Backups: backups,
		}),
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

// runCreateAdmin creates a superadmin. The password comes from
// $TAKTAPLUS_ADMIN_PASSWORD or the first line of stdin.
func runCreateAdmin(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	username := fs.String("username", "", "login name (required)")
	name := fs.String("name", "Administrator", "display name")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *username == "" {
		return errors.New("-username is required")
	}
	password := os.Getenv("TAKTAPLUS_ADMIN_PASSWORD")
	if password == "" {
		fmt.Fprint(os.Stderr, "password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		password = strings.TrimRight(line, "\r\n")
	}
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool, log); err != nil {
		return err
	}
	u, err := auth.NewService(pool, cfg.MasterKey).CreateUser(ctx, *username, *name, "", auth.RoleSuperAdmin, password)
	if err != nil {
		return err
	}
	fmt.Printf("created superadmin %s (id %d); set up two-factor authentication at first login\n", u.Username, u.ID)
	return nil
}
