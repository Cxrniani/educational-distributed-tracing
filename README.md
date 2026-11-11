# educational-distributed-tracing

Exemplo educacional de distributed tracing com três serviços em Go e OpenObserve como plataforma de observabilidade (traces, logs e métricas), tudo via OpenTelemetry.

## Arquitetura

```
curl ──> gateway :8080 ──> orders :8081 ──> inventory :8082
              │                 │                 │
              └──────── OTLP/HTTP ────────────────┘
                              │
                      OpenObserve :5080
```

| Serviço   | Porta | Papel                                              |
|-----------|-------|----------------------------------------------------|
| gateway   | 8080  | Recebe `/checkout` e chama o orders                |
| orders    | 8081  | Valida o pedido e pede reserva ao inventory        |
| inventory | 8082  | Reserva estoque e expõe métricas de nível de estoque |

O contexto do trace (W3C `traceparent`) é propagado automaticamente entre os serviços pelo `otelhttp`, e os logs (`slog`) saem com `trace_id` e `span_id`, o que permite ir de um trace para os logs dele.

## Executando

Requisitos: Docker com Compose.

```bash
docker compose up -d --build
```

- OpenObserve: http://localhost:5080
- Usuário: `root@example.com`
- Senha: `Complexpass#123`

Gerando tráfego:

```bash
./scripts/load.sh
```

Ou manualmente:

```bash
curl "localhost:8080/checkout?item=book&qty=2"
```

## Cenários para a demonstração

| Requisição                | Resultado | O que mostrar                                        |
|---------------------------|-----------|------------------------------------------------------|
| `item=book`               | 201       | Trace normal atravessando os 3 serviços              |
| `item=monitor`            | 201 lento | Span `inventory.db.query` de ~700ms como gargalo     |
| `item=cable`              | 409       | Sem estoque, log de warning correlacionado ao trace  |
| `item=ghost`              | 404       | Item inexistente                                     |
| `item=fail`               | 502       | Erro no banco propagado: span com status de erro     |
| `item=book&payment_delay=5` | 201 lento | Span `payment.authorize` de 5s (chamada externa lenta) |

O atraso normal do payment vem da variável `PAYMENT_DELAY_SECONDS` do orders (padrão `0.2`). O parâmetro `payment_delay` (em segundos, de 0 a 10) sobrescreve esse valor em uma requisição.

## Onde olhar no OpenObserve

- **Traces**: menu *Traces*, filtre por `service_name` e abra um trace para ver o waterfall.
- **Logs**: menu *Logs*, busque por `trace_id` para ver os logs de uma requisição específica.
- **Métricas**: menu *Metrics*, por exemplo `orders_created`, `orders_rejected`, `inventory_reservations`, `inventory_stock_level` e `http_server_duration`.
