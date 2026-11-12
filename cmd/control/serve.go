package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/b0balYson/dropsite/internal/api"
	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/siteauth"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
	"github.com/b0balYson/dropsite/internal/ui"
)

const (
	housekeepingInterval = time.Hour
	shutdownTimeout      = 20 * time.Second
	readHeaderTimeout    = 10 * time.Second
	// readTimeout and writeTimeout are generous upper bounds: archive uploads
	// (up to ~53 MB) may arrive over slow links. The API additionally applies
	// a short read deadline to every non-upload request.
	readTimeout   = 15 * time.Minute
	writeTimeout  = 15 * time.Minute
	idleTimeout   = 2 * time.Minute
	healthTimeout = 3 * time.Second
)

// app holds the wired components, shared by serve and tests.
type app struct {
	cfg    *config.Config
	log    *slog.Logger
	db     *store.DB
	svc    *service.Service
	public http.Handler
	intern http.Handler
	admin  http.Handler
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func dbPath(cfg *config.Config) string    { return filepath.Join(cfg.StateDir, "state.db") }
func uploadDir(cfg *config.Config) string { return filepath.Join(cfg.StateDir, "uploads") }

// build wires everything and runs startup (migrations, bootstrap, reconcile).
func build(ctx context.Context, cfg *config.Config, log *slog.Logger) (*app, error) {
	if err := os.MkdirAll(uploadDir(cfg), 0o700); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	db, err := store.Open(ctx, dbPath(cfg))
	if err != nil {
		return nil, err
	}
	svc := service.New(service.Deps{
		Config: cfg, Repo: db, Files: storage.NewFS(cfg.SitesDir),
		Logger: log, UploadDir: uploadDir(cfg),
	})
	if err := svc.Start(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &app{
		cfg: cfg, log: log, db: db, svc: svc,
		public: api.New(svc, log),
		intern: internalHandler(svc, log),
		admin:  ui.New(svc, log),
	}, nil
}

func internalHandler(svc *service.Service, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	// Caddy's forward_auth for sites carrying the protected marker.
	mux.Handle("/internal/auth-check", siteauth.AuthCheck(svc, log))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := svc.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unavailable\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func serve(ctx context.Context) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	log.Info("starting", "product", brand.Name, "version", brand.Version, "base_domain", cfg.BaseDomain, "api_host", cfg.APIHost)
	log.Warn("the base domain must be dedicated to published sites: user JavaScript runs on its subdomains, " +
		"so do not host other company services or cookies on it or its parent")
	a, err := build(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer a.db.Close()
	return a.run(ctx)
}

func (a *app) run(ctx context.Context) error {
	servers := []*http.Server{
		newServer(a.cfg.PublicAddr, a.public, a.log),
		newServer(a.cfg.InternalAddr, a.intern, a.log),
		newServer(a.cfg.AdminAddr, a.admin, a.log),
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, srv := range servers {
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", srv.Addr, err)
		}
		a.log.Info("listening", "addr", ln.Addr().String())
		g.Go(func() error {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve %s: %w", srv.Addr, err)
			}
			return nil
		})
	}
	g.Go(func() error {
		a.svc.RunHousekeeping(gctx, housekeepingInterval)
		return nil
	})
	g.Go(func() error {
		a.svc.RunStats(gctx, a.cfg.StatsInterval)
		return nil
	})
	g.Go(func() error {
		a.svc.RunExpiry(gctx, service.ExpiryCheckInterval)
		return nil
	})
	g.Go(func() error {
		a.svc.RunFormDelivery(gctx)
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		a.log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.WithoutCancel(gctx), shutdownTimeout)
		defer cancel()
		var errs []error
		for _, srv := range servers {
			errs = append(errs, srv.Shutdown(sctx))
		}
		return errors.Join(errs...)
	})
	return g.Wait()
}

func newServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

func healthcheck(ctx context.Context) error {
	addr := os.Getenv("INTERNAL_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse INTERNAL_ADDR: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil) //nolint:gosec // loopback probe, port from own config
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback probe
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: status %d", resp.StatusCode)
	}
	return nil
}
