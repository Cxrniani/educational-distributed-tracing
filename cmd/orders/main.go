package main

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/Cxrniani/educational-distributed-tracing/internal/server"
)

func main() {
	inventoryURL := server.Env("INVENTORY_URL", "http://localhost:8082")
	client := server.HTTPClient()

	server.Run("orders", "8081", func(mux server.Mux, logger *slog.Logger) {
		tracer := otel.Tracer("orders")
		meter := otel.Meter("orders")

		created, _ := meter.Int64Counter("orders.created",
			metric.WithDescription("Orders successfully created"))
		rejected, _ := meter.Int64Counter("orders.rejected",
			metric.WithDescription("Orders rejected by reason"))

		mux.Route("GET /orders", func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), "orders.create")
			defer span.End()

			item := r.URL.Query().Get("item")
			qty, err := strconv.Atoi(r.URL.Query().Get("qty"))
			if err != nil || qty < 1 || item == "" {
				rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "invalid_request")))
				server.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid item or qty"})
				return
			}
			span.SetAttributes(attribute.String("item", item), attribute.Int("qty", qty))

			_, validate := tracer.Start(ctx, "orders.validate")
			time.Sleep(time.Duration(5+rand.IntN(20)) * time.Millisecond)
			validate.End()

			status, body, err := server.Get(ctx, client,
				fmt.Sprintf("%s/stock/reserve?item=%s&qty=%d", inventoryURL, url.QueryEscape(item), qty))
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "inventory_unreachable")))
				logger.ErrorContext(ctx, "inventory unreachable", "error", err)
				server.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": "inventory unreachable"})
				return
			}

			switch {
			case status == http.StatusOK:
				orderID := fmt.Sprintf("ord-%06d", rand.IntN(1000000))
				span.SetAttributes(attribute.String("order.id", orderID))
				created.Add(ctx, 1, metric.WithAttributes(attribute.String("item", item)))
				logger.InfoContext(ctx, "order created", "order_id", orderID, "item", item, "qty", qty)
				server.WriteJSON(w, http.StatusCreated, map[string]any{"order_id": orderID, "item": item, "qty": qty})
			case status >= 500:
				span.SetStatus(codes.Error, "inventory failed")
				rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "inventory_error")))
				logger.ErrorContext(ctx, "inventory returned error", "status", status, "body", string(body))
				server.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": "inventory failed"})
			default:
				rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "rejected_by_inventory")))
				logger.WarnContext(ctx, "order rejected", "status", status, "item", item, "body", string(body))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(body)
			}
		})
	})
}
