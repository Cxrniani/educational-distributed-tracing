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

## Onde olhar no OpenObserve

- **Traces**: menu *Traces*, filtre por `service_name` e abra um trace para ver o waterfall.
- **Logs**: menu *Logs*, busque por `trace_id` para ver os logs de uma requisição específica.
- **Métricas**: menu *Metrics*, por exemplo `orders_created`, `orders_rejected`, `inventory_reservations`, `inventory_stock_level` e `http_server_duration`.

## Rodando sem Docker

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:5080/api/default
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic cm9vdEBleGFtcGxlLmNvbTpDb21wbGV4cGFzcyMxMjM="

go run ./cmd/inventory &
go run ./cmd/orders &
go run ./cmd/gateway &
```

Suba o OpenObserve separadamente:

```bash
docker run -p 5080:5080 -e ZO_ROOT_USER_EMAIL=root@example.com -e ZO_ROOT_USER_PASSWORD='Complexpass#123' public.ecr.aws/zinclabs/openobserve:latest
```

## Levando para outra máquina

```bash
docker compose build
docker pull public.ecr.aws/zinclabs/openobserve:latest
docker save -o imagens.tar public.ecr.aws/zinclabs/openobserve:latest educational-distributed-tracing-gateway educational-distributed-tracing-orders educational-distributed-tracing-inventory
```

No destino: `docker load -i imagens.tar && docker compose up -d`.

## Licença

Veja o arquivo `LICENSE`.
