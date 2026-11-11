package main

import (
	"context"
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
	"go.opentelemetry.io/otel/trace"

	"github.com/Cxrniani/educational-distributed-tracing/internal/server"
)

const maxPaymentDelaySeconds = 10

func authorizePayment(ctx context.Context, tracer trace.Tracer, item string, qty int, delay time.Duration) {
	_, span := tracer.Start(ctx, "payment.authorize", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("peer.service", "payment-provider"),
			attribute.String("payment.item", item),
			attribute.Int("payment.qty", qty),
			attribute.Float64("payment.delay_seconds", delay.Seconds()),
		))
	defer span.End()

	time.Sleep(delay)
}

func envSeconds(key string, fallback float64) float64 {
	value, err := strconv.ParseFloat(server.Env(key, ""), 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

func main() {
	inventoryURL := server.Env("INVENTORY_URL", "http://localhost:8082")
	client := server.HTTPClient()
	defaultPaymentDelay := envSeconds("PAYMENT_DELAY_SECONDS", 0.2)

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
			paymentDelay := defaultPaymentDelay
			if raw := r.URL.Query().Get("payment_delay"); raw != "" {
				parsed, err := strconv.ParseFloat(raw, 64)
				if err != nil || parsed < 0 || parsed > maxPaymentDelaySeconds {
					rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "invalid_request")))
					server.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "payment_delay must be between 0 and 10 seconds"})
					return
				}
				paymentDelay = parsed
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
				logger.ErrorContext(ctx, "Could not reach the inventory service, order was not created", "error", err)
				server.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": "inventory unreachable"})
				return
			}

			switch {
			case status == http.StatusOK:
				logger.InfoContext(ctx, "Stock reserved, requesting payment authorization from the external provider", "item", item, "qty", qty)
				authorizePayment(ctx, tracer, item, qty, seconds(paymentDelay))
				logger.InfoContext(ctx, "External payment provider authorized the payment", "item", item, "qty", qty, "delay_seconds", paymentDelay)
				orderID := fmt.Sprintf("ord-%06d", rand.IntN(1000000))
				span.SetAttributes(attribute.String("order.id", orderID))
				created.Add(ctx, 1, metric.WithAttributes(attribute.String("item", item)))
				logger.InfoContext(ctx, "Payment authorized, order created successfully", "order_id", orderID, "item", item, "qty", qty)
				server.WriteJSON(w, http.StatusCreated, map[string]any{"order_id": orderID, "item": item, "qty": qty})
			case status >= 500:
				span.SetStatus(codes.Error, "inventory failed")
				rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "inventory_error")))
				logger.ErrorContext(ctx, "Inventory service failed while reserving stock, order was not created", "status", status, "body", string(body))
				server.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": "inventory failed"})
			default:
				rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "rejected_by_inventory")))
				logger.WarnContext(ctx, "Inventory refused the reservation, order was not created", "status", status, "item", item, "body", string(body))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(body)
			}
		})
	})
}
