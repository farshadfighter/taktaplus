// Package api wires the HTTP API and the embedded web UI.
package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/buildinfo"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/monitor"
	"github.com/farshadfighter/taktaplus/internal/packages"
)

// Deps are the server's collaborators.
type Deps struct {
	Log          *slog.Logger
	DB           *pgxpool.Pool
	Auth         *auth.Service
	License      *license.Manager
	UI           fs.FS
	CookieSecure bool
	Inventory    *inventory.Service
	Alerts       *alerts.Service
	Monitor      *monitor.Monitor
	Backups      *backup.Service
	Packages     *packages.Service
	TmpDir       string // for large uploads; default os.TempDir()
}

type srv struct {
	log          *slog.Logger
	db           *pgxpool.Pool
	auth         *auth.Service
	lic          *license.Manager
	cookieSecure bool
	inv          *inventory.Service
	alerts       *alerts.Service
	mon          *monitor.Monitor
	backups      *backup.Service
	pkgs         *packages.Service
	tmpDir       string
}

// New returns the root HTTP handler.
func New(d Deps) http.Handler {
	s := &srv{log: d.Log, db: d.DB, auth: d.Auth, lic: d.License, cookieSecure: d.CookieSecure,
		inv: d.Inventory, alerts: d.Alerts, mon: d.Monitor, backups: d.Backups, pkgs: d.Packages, tmpDir: d.TmpDir}
	if s.tmpDir == "" {
		s.tmpDir = os.TempDir()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, securityHeaders)

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			d.Log.Warn("health check failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "database": "down"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"version": buildinfo.Version, "commit": buildinfo.Commit, "date": buildinfo.Date,
			})
		})

		// Large uploads: no request timeout.
		r.Group(func(r chi.Router) {
			r.Use(csrfGuard, s.loadSession)
			pm := requirePerm(auth.PermPackages)
			r.With(pm).Post("/packages/upload", s.uploadPackage)
			r.With(pm).Post("/packages/bundle", s.importBundle)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(120*time.Second), csrfGuard, s.loadSession)
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/mfa", s.handleMFA)
			r.Post("/auth/mfa/setup", s.handleMFASetup)
			r.Post("/auth/mfa/enable", s.handleMFAEnable)
			r.Post("/auth/logout", s.handleLogout)
			r.With(requireUser).Get("/me", s.handleMe)
			r.With(requirePerm(auth.PermAuditRead)).Get("/audit", s.handleAuditList)

			users := requirePerm(auth.PermUsersManage)
			r.With(users).Get("/users", s.listUsers)
			r.With(users).Post("/users", s.createUser)
			r.With(users).Put("/users/{id}", s.updateUser)
			r.With(users).Post("/users/{id}/password", s.setPassword)
			r.With(users).Post("/users/{id}/reset-mfa", s.resetMFA)
			r.With(users).Post("/users/{id}/unlock", s.unlockUser)

			r.With(requirePerm(auth.PermLicenseRead)).Get("/license", s.getLicense)
			lm := requirePerm(auth.PermLicenseManage)
			r.With(lm).Post("/license/activate", s.activateLicense)
			r.With(lm).Post("/license/request", s.licenseRequest)
			r.With(lm).Post("/license/upload", s.uploadLicense)
			r.With(lm).Post("/license/heartbeat", s.licenseHeartbeat)

			read := requirePerm(auth.PermDevicesRead)
			write := requirePerm(auth.PermDevicesWrite)
			r.With(read).Get("/devices", s.listDevices)
			r.With(write).Post("/devices/discover", s.discoverDevice)
			r.With(write).Post("/devices", s.createDevice)
			r.With(read).Get("/devices/{id}", s.getDevice)
			r.With(write).Put("/devices/{id}", s.updateDevice)
			r.With(write).Delete("/devices/{id}", s.deleteDevice)
			r.With(read).Post("/devices/{id}/refresh", s.refreshDevice)
			r.With(read).Get("/devices/{id}/metrics", s.deviceMetrics)
			r.With(read).Get("/groups", s.listGroups)
			r.With(write).Post("/groups", s.saveGroup)
			r.With(write).Put("/groups/{id}", s.saveGroup)
			r.With(write).Delete("/groups/{id}", s.deleteGroup)

			am := requirePerm(auth.PermAlertsManage)
			r.With(read).Get("/alerts", s.listAlerts)
			r.With(read).Post("/alerts/{id}/ack", s.ackAlert)
			r.With(read).Get("/alert-rules", s.listRules)
			r.With(am).Post("/alert-rules", s.saveRule)
			r.With(am).Put("/alert-rules/{id}", s.saveRule)
			r.With(am).Delete("/alert-rules/{id}", s.deleteRule)
			r.With(am).Get("/channels", s.listChannels)
			r.With(am).Post("/channels", s.saveChannel)
			r.With(am).Put("/channels/{id}", s.saveChannel)
			r.With(am).Delete("/channels/{id}", s.deleteChannel)
			r.With(am).Post("/channels/{id}/test", s.testChannel)

			badmin := requirePerm(auth.PermBackupsAdmin)
			r.With(read).Get("/backups", s.listBackups)
			r.With(requirePerm(auth.PermBackupsRun)).Post("/devices/{id}/backup", s.runBackup)
			r.With(read).Get("/backups/diff", s.diffBackups)
			r.With(read).Get("/backups/{id}/content", s.backupContent)
			r.With(badmin).Get("/backups/{id}/download", s.downloadBackup)
			r.With(badmin).Put("/backups/{id}", s.updateBackup)
			r.With(badmin).Delete("/backups/{id}", s.deleteBackup)
			r.With(requirePerm(auth.PermRestore)).Get("/backups/{id}/diff-current", s.diffCurrent)
			r.With(requirePerm(auth.PermRestore)).Post("/backups/{id}/restore", s.restoreBackup)
			r.With(read).Get("/restores", s.listRestores)
			r.With(read).Get("/restores/{id}", s.getRestore)
			r.With(read).Get("/backup-policies", s.listPolicies)
			r.With(badmin).Post("/backup-policies", s.savePolicy)
			r.With(badmin).Put("/backup-policies/{id}", s.savePolicy)
			r.With(badmin).Delete("/backup-policies/{id}", s.deletePolicy)

			pm := requirePerm(auth.PermPackages)
			r.With(read).Get("/packages", s.listPackages)
			r.With(pm).Put("/packages/{id}", s.updatePackage)
			r.With(pm).Delete("/packages/{id}", s.deletePackage)
			r.With(read).Get("/package-sources", s.listSources)
			r.With(pm).Post("/package-sources", s.saveSource)
			r.With(pm).Put("/package-sources/{id}", s.saveSource)
			r.With(pm).Delete("/package-sources/{id}", s.deleteSource)
			r.With(pm).Post("/package-sources/{id}/sync", s.syncSource)
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
	})

	r.Handle("/*", spaHandler(d.UI))
	return r
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if !isAPI(r.URL.Path) {
			h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves static files and falls back to index.html so client-side
// routes work on reload.
func spaHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if fi, err := fs.Stat(ui, name); err == nil && !fi.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(ui, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "web UI is not built (run: make web)", http.StatusServiceUnavailable)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
