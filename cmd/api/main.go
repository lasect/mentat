package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	apiauth "tetra/internal/api/auth"
	apimiddleware "tetra/internal/api/middleware"
	apiorgs "tetra/internal/api/orgs"
	coreauth "tetra/internal/auth"
	"tetra/internal/config"
	coreorgs "tetra/internal/orgs"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("tetra-api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAPI()
	if err != nil {
		return err
	}
	privateKey, err := coreauth.ParsePrivateKey(cfg.JWTPrivateKey)
	if err != nil {
		return err
	}
	tokens, err := coreauth.NewTokenManager(privateKey, cfg.JWTIssuer, cfg.JWTAudience, cfg.AccessTTL)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		return err
	}
	service, err := coreauth.NewService(pool, tokens, cfg.RefreshTTL)
	if err != nil {
		return err
	}
	authHandler, err := apiauth.NewHandler(service, cfg)
	if err != nil {
		return err
	}
	connectionCipher, err := coreorgs.NewConnectionCipher(cfg.ConnectionKey, 1)
	if err != nil {
		return err
	}
	orgService, err := coreorgs.NewService(pool, connectionCipher)
	if err != nil {
		return err
	}
	orgHandler, err := apiorgs.NewHandler(orgService)
	if err != nil {
		return err
	}
	clientIPMiddleware, err := apimiddleware.ClientIP(cfg.TrustedProxyCIDRs)
	if err != nil {
		return err
	}

	router := chi.NewRouter()
	router.NotFound(apimiddleware.NotFound)
	router.MethodNotAllowed(apimiddleware.MethodNotAllowed)
	router.Use(chimiddleware.RequestID)
	router.Use(clientIPMiddleware)
	router.Use(apimiddleware.AccessLog)
	router.Use(chimiddleware.Recoverer)
	router.Use(chimiddleware.Timeout(15 * time.Second))
	router.Use(apimiddleware.SecurityHeaders)
	router.Use(apimiddleware.CORS(cfg.AllowedOrigins))
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	router.Mount("/v1/auth", authHandler.Routes())
	router.Route("/v1/orgs", func(r chi.Router) {
		r.Use(authHandler.RequireAccess)
		r.Mount("/", orgHandler.Routes())
	})

	server := &http.Server{
		Addr: cfg.Address, Handler: router,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second,
		WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		slog.Info("tetra-api listening", "address", cfg.Address)
		errs <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-signals:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
