package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/Cxrniani/educational-distributed-tracing/internal/server"
)

func main() {
	ordersURL := server.Env("ORDERS_URL", "http://localhost:8081")
	client := server.HTTPClient()

	server.Run("gateway", "8080", func(mux server.Mux, logger *slog.Logger) {
		meter := otel.Meter("gateway")
		checkouts, _ := meter.Int64Counter("checkout.requests",
			metric.WithDescription("Checkout requests received by item"))

		mux.Route("GET /checkout", func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			item := r.URL.Query().Get("item")
			if item == "" {
				item = "book"
			}
			qty := 1
			if raw := r.URL.Query().Get("qty"); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil {
					server.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid qty"})
					return
				}
				qty = parsed
			}

			checkouts.Add(ctx, 1, metric.WithAttributes(attribute.String("item", item)))
			logger.InfoContext(ctx, "Received checkout request, forwarding it to the orders service", "item", item, "qty", qty)

			target := fmt.Sprintf("%s/orders?item=%s&qty=%d", ordersURL, url.QueryEscape(item), qty)
			if delay := r.URL.Query().Get("payment_delay"); delay != "" {
				target += "&payment_delay=" + url.QueryEscape(delay)
			}
			status, body, err := server.Get(ctx, client, target)
			if err != nil {
				logger.ErrorContext(ctx, "Could not reach the orders service, returning 502 to the client", "error", err)
				server.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": "orders unreachable"})
				return
			}

			logger.InfoContext(ctx, "Checkout request completed, relaying the orders service response to the client", "status", status)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(body)
		})
	})
}
