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
	"time"

	"github.com/lmgarret/gotifacts/internal/api"
	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/httplog"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/logging"
	"github.com/lmgarret/gotifacts/internal/portal"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
	"github.com/lmgarret/gotifacts/web"
)

func runServe(ctx context.Context, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Build the configured logger as early as possible and make it the process
	// default so every package (store, auth, …) logs through it consistently.
	log := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			log.Error("invalid configuration", "err", e)
		}
		return fmt.Errorf("%d configuration error(s)", len(errs))
	}

	if err := os.MkdirAll(cfg.SitesDir(), 0o755); err != nil {
		return fmt.Errorf("create sites dir: %w", err)
	}

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	st.SetLogger(log)
	defer func() { _ = st.Close() }()

	dist, err := web.Dist()
	if err != nil {
		return fmt.Errorf("load embedded frontend: %w", err)
	}
	spa := portal.NewSPA(dist)
	apexSrv, err := api.New(cfg, st, spa, log)
	if err != nil {
		return fmt.Errorf("init apex server: %w", err)
	}
	// Log every request on completion. Management/ingest traffic on the apex host
	// logs at info (these are the actions operators care about); high-volume
	// static asset serving on site hosts logs at debug to stay out of the way.
	apex := httplog.Middleware(log.With("plane", "apex"), slog.LevelInfo)(apexSrv.Handler())
	sites := httplog.Middleware(log.With("plane", "site"), slog.LevelDebug)(portal.NewSiteServer(cfg))

	pub := ingest.New(cfg, st)
	go runPurgeLoop(ctx, pub, log)

	dispatch := router.NewDispatch(cfg, apex, sites)
	authn := auth.New(cfg, st)
	handler := authn.StripUntrustedIdentity(dispatch)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctxSignal().Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("gotifacts listening",
		"addr", cfg.ListenAddr,
		"base_domain", cfg.BaseDomain,
		"versioning", cfg.VersioningEnabled,
		"trusted_proxies", len(cfg.TrustedProxies),
		"admin_users", len(cfg.AdminUsers))

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("gotifacts stopped")
	return nil
}

func ctxSignal() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// runPurgeLoop runs the soft-deleted site purge on startup and then every hour
// until ctx is cancelled.
func runPurgeLoop(ctx context.Context, pub *ingest.Publisher, log *slog.Logger) {
	purge := func() {
		n, err := pub.PurgeDeleted(ctx)
		if err != nil {
			log.Error("purge deleted sites", "err", err)
		} else if n > 0 {
			log.Info("purged deleted sites", "count", n)
		}
	}
	purge()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purge()
		}
	}
}
