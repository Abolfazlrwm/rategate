// Command rategate-server runs the RateGate gRPC + REST server.
//
// Flags:
//
//	--config <path>         path to a YAML config file (optional)
//	--store <memory|redis>  overrides store.backend
//	--redis-addr <host:port>  overrides redis.addr
//	--grpc-port <n>         overrides server.grpc_port
//	--rest-port <n>         overrides server.rest_port
//
// Environment variables (RATEGATE_*) take precedence over the config file
// but are overridden by flags. See internal/config for the full schema.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/Abolfazlrwm/rategate/internal/config"
	"github.com/Abolfazlrwm/rategate/internal/observability"
	"github.com/Abolfazlrwm/rategate/internal/server"
	"github.com/Abolfazlrwm/rategate/internal/store"
	"github.com/Abolfazlrwm/rategate/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "rategate-server: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "", "path to a YAML config file")
	storeBackend := flag.String("store", "", "override store.backend (memory|redis)")
	redisAddr := flag.String("redis-addr", "", "override redis.addr")
	grpcPort := flag.Int("grpc-port", 0, "override server.grpc_port")
	restPort := flag.Int("rest-port", 0, "override server.rest_port")
	flag.Parse()

	v := config.New()
	if *cfgPath != "" {
		v.SetConfigFile(*cfgPath)
	}
	cfg, err := config.Load(v)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	applyFlags(cfg, *storeBackend, *redisAddr, *grpcPort, *restPort)

	logger := observability.NewLogger(cfg.Log.Level, cfg.Log.Format)
	logger.Info("starting rategate-server", "version", version.String(), "config", redactedSnapshot(cfg))

	// Observability
	metrics, reg := observability.NewMetrics()
	tp, err := observability.InitTracing(context.Background(), cfg.Observability.OTLPEndpoint, cfg.Observability.ServiceName)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(shutdownCtx)
	}()

	// Store
	s, err := buildStore(cfg)
	if err != nil {
		return fmt.Errorf("build store: %w", err)
	}
	defer s.Close()

	// Core server
	core := server.New(cfg, s, metrics, logger)

	// gRPC listener
	grpcAddr := fmt.Sprintf("%s:%d", cfg.Server.GRPCHost, cfg.Server.GRPCPort)
	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}

	// gRPC server with the RateGateService + health.
	grpcSrv := grpc.NewServer()
	grpcAdapter := server.NewGRPCServer(core)
	grpcAdapter.RegisterServer(grpcSrv)

	// REST server (Gin) with the /metrics handler bound to our private registry.
	metricsHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	restAdapter := server.NewHTTPServer(core, metricsHandler)
	restAddr := fmt.Sprintf("%s:%d", cfg.Server.RESTHost, cfg.Server.RESTPort)
	restSrv := &http.Server{
		Addr:         restAddr,
		Handler:      restAdapter.Engine(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	core.MarkReady()

	// Start listeners
	errCh := make(chan error, 2)
	go func() {
		logger.Info("grpc listening", "addr", grpcLis.Addr().String())
		if err := grpcSrv.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		logger.Info("rest listening", "addr", restAddr)
		if err := restSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("rest serve: %w", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig.String())
	case err := <-errCh:
		logger.Error("server error", "error", err)
	}

	core.MarkNotReady()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	grpcSrv.GracefulStop()
	_ = restSrv.Shutdown(shutdownCtx)
	logger.Info("shutdown complete")
	return nil
}

// applyFlags overlays command-line overrides on the loaded config.
func applyFlags(cfg *config.Config, backend, redisAddr string, grpcPort, restPort int) {
	if backend != "" {
		cfg.Store.Backend = backend
	}
	if redisAddr != "" {
		cfg.Redis.Addr = redisAddr
	}
	if grpcPort > 0 {
		cfg.Server.GRPCPort = grpcPort
	}
	if restPort > 0 {
		cfg.Server.RESTPort = restPort
	}
}

// buildStore constructs the Store based on the config backend.
func buildStore(cfg *config.Config) (store.Store, error) {
	switch cfg.Store.Backend {
	case "memory":
		return store.NewMemoryStore(store.MemoryOptions{
			TBShards:      cfg.Store.MemoryShards,
			SWShards:      cfg.Store.MemorySWShards,
			SWGranularity: cfg.Store.MemorySWGran,
			SWApproximate: cfg.Store.MemorySWApprox,
		}), nil
	case "redis":
		return store.NewRedisStore(store.RedisOptions{
			Addr:      cfg.Redis.Addr,
			Password:  cfg.Redis.Password,
			DB:        cfg.Redis.DB,
			PoolSize:  cfg.Redis.PoolSize,
			KeyPrefix: cfg.Redis.Prefix,
		}), nil
	default:
		return nil, fmt.Errorf("unknown store backend %q", cfg.Store.Backend)
	}
}

// redactedSnapshot returns a map of non-sensitive config fields for logging.
func redactedSnapshot(cfg *config.Config) map[string]any {
	return map[string]any{
		"store_backend":     cfg.Store.Backend,
		"default_algorithm": cfg.Store.DefaultAlgorithm,
		"grpc_port":         cfg.Server.GRPCPort,
		"rest_port":         cfg.Server.RESTPort,
		"redis_addr":        cfg.Redis.Addr,
		"policies":          len(cfg.Policies),
		"metrics_enabled":   cfg.Observability.MetricsEnabled,
		"tracing_enabled":   cfg.Observability.TracingEnabled,
		"auth_enabled":      cfg.Server.APIKey != "",
	}
}

// keep slog referenced even if logging calls move into helpers.
var _ = slog.Info
