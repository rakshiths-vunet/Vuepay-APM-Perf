# VuePay High-Throughput Go Journey Producer

A config-driven Go load producer that continuously executes 3 VuePay journeys (`account`, `payment_initiate`, `bills`) with automatic auth token lifecycle management.

## Prerequisites

- Go 1.22+

## Setup

```bash
cp config.example.yaml config.yaml
# edit credentials and endpoint values in config.yaml
```

## Run

```bash
go run ./cmd/producer
```

## Build

```bash
go build -o vuepay-producer ./cmd/producer
```

## Tuning Guide

- Increase `producer.worker_count` if workers are starved and CPU has headroom.
- Increase `producer.target_tps` to raise global request throughput.
- Keep `worker_count` sufficiently above target TPS to absorb network jitter.
- Use lower `http_timeout_ms` in healthy low-latency environments to fail fast.
- Keep journey weights balanced unless intentionally biasing a particular API.

Recommended iteration loop:

1. Start with `worker_count: 500`, `target_tps: 6000`.
2. Observe TPS and host CPU/network utilization.
3. Increase `worker_count` in steps of 100 if TPS plateaus below target.
4. Increase `target_tps` only after stable behavior is achieved.

## Sample Output

```text
[2026-04-28 10:00:01] TPS: 6124 | account: 2041 ok/0 err | payment_initiate: 2039 ok/1 err | bills: 2044 ok/0 err
```

## Notes

- Token refresh runs proactively every `auth.token_ttl_minutes`.
- If any journey gets HTTP 401, an emergency token refresh is triggered and the request is retried once.
- Config supports environment variable overrides with `VUEPAY_` prefix (for example `VUEPAY_AUTH_PASSWORD`).
