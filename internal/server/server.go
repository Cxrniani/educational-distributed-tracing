package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/Cxrniani/educational-distributed-tracing/internal/telemetry"
)

type Mux struct {
	*http.ServeMux
}

func (m Mux) Route(pattern string, handler http.HandlerFunc) {
	m.Handle(pattern, otelhttp.NewHandler(handler, pattern))
}

func Env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func HTTPClient() *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   5 * time.Second,
	}
}

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func Get(ctx context.Context, client *http.Client, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func Run(service, defaultPort string, register func(Mux, *slog.Logger)) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Setup(ctx, service)
	if err != nil {
		fmt.Fprintln(os.Stderr, "telemetry setup failed:", err)
		os.Exit(1)
	}
	logger := telemetry.Logger(service)

	mux := Mux{http.NewServeMux()}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	register(mux, logger)

	srv := &http.Server{
		Addr:              ":" + Env("PORT", defaultPort),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	drained := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(drained)
	}()

	logger.Info("Service started and accepting HTTP requests", "service", service, "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped unexpectedly, shutting the service down", "error", err)
		stop()
	}
	<-drained

	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdownTelemetry(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "telemetry shutdown failed:", err)
	}
}
