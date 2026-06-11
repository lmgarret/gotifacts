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
	"github.com/lmgarret/gotifacts/internal/portal"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
	"github.com/lmgarret/gotifacts/web"
)

func runServe(ctx context.Context, _ []string) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		return err
	}
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
	apex := api.New(cfg, st, spa, log).Handler()
	sites := portal.NewSiteServer(cfg)

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
