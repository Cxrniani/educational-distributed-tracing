package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Cxrniani/educational-distributed-tracing/internal/server"
)

const (
	outcomeReserved   = "reserved"
	outcomeNotFound   = "not_found"
	outcomeOutOfStock = "out_of_stock"
	outcomeError      = "error"
)

var errDatabase = errors.New("database connection lost")

type store struct {
	mu    sync.Mutex
	stock map[string]int
}

func (s *store) reserve(ctx context.Context, tracer trace.Tracer, item string, qty int) (string, int, error) {
	_, span := tracer.Start(ctx, "inventory.db.query", trace.WithAttributes(
		attribute.String("db.system", "memory"),
		attribute.String("db.operation", "reserve"),
		attribute.String("item", item),
	))
	defer span.End()

	if item == "monitor" {
		time.Sleep(time.Duration(600+rand.IntN(300)) * time.Millisecond)
	} else {
		time.Sleep(time.Duration(5+rand.IntN(15)) * time.Millisecond)
	}

	if item == "fail" {
		span.RecordError(errDatabase)
		span.SetStatus(codes.Error, errDatabase.Error())
		return outcomeError, 0, errDatabase
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	available, ok := s.stock[item]
	switch {
	case !ok:
		return outcomeNotFound, 0, nil
	case available < qty:
		return outcomeOutOfStock, available, nil
	}
	s.stock[item] = available - qty
	span.SetAttributes(attribute.Int("stock.remaining", s.stock[item]))
	return outcomeReserved, s.stock[item], nil
}

func (s *store) snapshot() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]int, len(s.stock))
	for item, level := range s.stock {
		copied[item] = level
	}
	return copied
}

func main() {
	s := &store{stock: map[string]int{
		"book":     1000,
		"notebook": 500,
		"monitor":  200,
		"cable":    0,
	}}

	server.Run("inventory", "8082", func(mux server.Mux, logger *slog.Logger) {
		tracer := otel.Tracer("inventory")
		meter := otel.Meter("inventory")

		reservations, _ := meter.Int64Counter("inventory.reservations",
			metric.WithDescription("Stock reservation attempts by outcome"))
		_, _ = meter.Int64ObservableGauge("inventory.stock.level",
			metric.WithDescription("Current stock level by item"),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				for item, level := range s.snapshot() {
					o.Observe(int64(level), metric.WithAttributes(attribute.String("item", item)))
				}
				return nil
			}))

		mux.Route("GET /stock/reserve", func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			item := r.URL.Query().Get("item")
			qty, err := strconv.Atoi(r.URL.Query().Get("qty"))
			if err != nil || qty < 1 {
				server.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid qty"})
				return
			}

			outcome, remaining, err := s.reserve(ctx, tracer, item, qty)
			reservations.Add(ctx, 1, metric.WithAttributes(
				attribute.String("item", item),
				attribute.String("outcome", outcome),
			))

			switch outcome {
			case outcomeError:
				logger.ErrorContext(ctx, "reservation failed", "item", item, "error", err)
				server.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			case outcomeNotFound:
				logger.WarnContext(ctx, "unknown item", "item", item)
				server.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "unknown item"})
			case outcomeOutOfStock:
				logger.WarnContext(ctx, "out of stock", "item", item, "requested", qty, "available", remaining)
				server.WriteJSON(w, http.StatusConflict, map[string]any{"error": "out of stock", "available": remaining})
			default:
				logger.InfoContext(ctx, "stock reserved", "item", item, "qty", qty, "remaining", remaining)
				server.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "reserved": qty, "remaining": remaining})
			}
		})
	})
}
