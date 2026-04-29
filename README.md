# VuePay APM Perf Producer

High-throughput Go producer for VuePay journey APIs with webhook-based runtime control.

This service can:

1. Authenticate using login + OTP and keep the bearer token refreshed.
2. Dispatch weighted journey traffic across account, payment_initiate, and bills.
3. Expose webhook endpoints to start, stop, restart, and reconfigure the producer at runtime.

## Current Execution Model

Journey APIs are currently executed in fire-and-forget mode.

1. Account, payment, and bills requests are launched asynchronously.
2. The worker does not wait for HTTP response completion for journey APIs.
3. Metrics reflect dispatch attempts, not confirmed backend response outcomes.

Auth APIs are still synchronous.

1. Login and OTP verification must succeed before traffic starts.
2. If initial auth fails, producer start fails.

## Repository Layout

1. cmd/producer/main.go: process entrypoint, logger, signal handling, webhook bootstrap.
2. internal/config/config.go: YAML loading, env overrides, defaults, validation.
3. internal/auth/token_manager.go: login, OTP verify, periodic and signal-based token refresh.
4. internal/control/controller.go: runtime lifecycle start, stop, restart, configure.
5. internal/control/webhook_server.go: HTTP webhook handlers.
6. internal/producer/dispatcher.go: weighted round-robin journey selection.
7. internal/producer/worker_pool.go: concurrent workers and dispatch loop.
8. internal/journey/*.go: API request implementations.
9. internal/stats/reporter.go: periodic TPS and per-journey counters.

## Prerequisites

1. Go 1.21 or newer on this workspace.
2. Network reachability from your host to the configured VuePay gateway.

## Configuration

Copy the sample and edit values:

```bash
cp config.example.yaml config.yaml
```

Important fields:

1. server.base_url: VuePay gateway base URL.
2. server.tls_skip_verify: set true only for non-production/self-signed TLS.
3. auth.phone_number, auth.password, auth.otp: login credentials and OTP.
4. auth.token_ttl_minutes: proactive token refresh interval.
5. producer.target_tps: desired send rate.
6. producer.worker_count: number of concurrent workers.
7. producer.journey_weights: weighted distribution per journey.
8. producer.http_timeout_ms: HTTP client timeout.
9. webhook.enabled, webhook.listen_addr, webhook.secret, webhook.auto_start.

## Run

Run with default config path:

```bash
go run ./cmd/producer
```

Run with explicit config path:

```bash
go run ./cmd/producer -config config.yaml
```

Build binary:

```bash
go build -o vuepay-producer ./cmd/producer
```

## Webhook API

If webhook.enabled is true, the control server starts on webhook.listen_addr.

If webhook.secret is non-empty, every webhook request must include header X-Webhook-Secret.

### 1) Get status

Method and path:

```text
GET or POST /webhook/status
```

Example:

```bash
curl -s http://10.1.92.124:8090/webhook/status
```

Alternative:

```bash
curl -s -X POST http://10.1.92.124:8090/webhook/status
```

Sample response:

```json
{
	"status": {
		"running": true,
		"started_at": "2026-04-28T12:20:00Z",
		"config": {
			"target_tps": 6000,
			"worker_count": 5000,
			"http_timeout_ms": 50000
		}
	}
}
```

### 2) Start producer

Method and path:

```text
POST /webhook/start
```

Example:

```bash
curl -s -X POST http://10.1.92.124:8090/webhook/start
```

Response behavior:

1. Returns immediately with HTTP 202 and `{"message":"start triggered"}`.
2. Auth + producer startup continues asynchronously in background.
3. Use `/webhook/status` to watch `starting` transition to `running`.

### 3) Stop producer

Method and path:

```text
POST /webhook/stop
```

Example:

```bash
curl -s -X POST http://10.1.92.124:8090/webhook/stop
```

### 4) Restart producer

Method and path:

```text
POST /webhook/restart
```

Example:

```bash
curl -s -X POST http://10.1.92.124:8090/webhook/restart
```

### 5) Configure producer

Method and path:

```text
POST /webhook/configure
```

Request body supports:

1. config_path: load config from file path.
2. config: inline full config object.
3. restart: if true and running, stop then start with new config.
4. start: if true and currently stopped, start with new config.

Example using config_path:

```bash
curl -s -X POST http://10.1.92.124:8090/webhook/configure \
	-H 'Content-Type: application/json' \
	-d '{"config_path":"config.yaml","restart":true}'
```

Example using inline config:

```bash
curl -s -X POST http://10.1.92.124:8090/webhook/configure \
	-H 'Content-Type: application/json' \
	-d '{
		"config": {
			"server": {
				"base_url": "https://home-blr-001.vunetsystems.com:8443",
				"tls_skip_verify": true
			},
			"auth": {
				"phone_number": "7795012759",
				"password": "password",
				"otp": "999999",
				"token_ttl_minutes": 7
			},
			"user": {
				"phone_number": "7795012759",
				"user_id": "User",
				"account_no": "ACC1000000002"
			},
			"payment": {
				"amount": 123,
				"beneficiary_name": "Ganesh",
				"beneficiary_account": "ACC1000000001"
			},
			"producer": {
				"target_tps": 6000,
				"worker_count": 5000,
				"journey_weights": {
					"account": 1,
					"payment_initiate": 1,
					"bills": 1
				},
				"http_timeout_ms": 50000,
				"stats_interval_seconds": 1
			},
			"webhook": {
				"enabled": true,
				"listen_addr": ":8090",
				"secret": "",
				"auto_start": true
			},
			"logging": {
				"level": "info",
				"format": "json",
				"output": "stdout"
			}
		},
		"start": true
	}'
```

## Logging and Metrics

Reporter output format:

```text
[YYYY-MM-DD HH:MM:SS] TPS: N | ok_tps: X | err_tps: Y | account: a ok/b err | payment_initiate: c ok/d err | bills: e ok/f err
```

Because journeys are fire-and-forget, ok and err counters describe dispatch-level execution results in worker code, not definitive downstream HTTP success rates.

## Throughput Notes

The controller logs a warning when your timeout-limited theoretical ceiling is below target TPS:

timeout_limited_tps = worker_count / (http_timeout_ms / 1000)

Example:

1. worker_count = 5000
2. http_timeout_ms = 50000
3. timeout_limited_tps = 100

This warning is a sizing hint under slow or blocking responses.

## Environment Variable Overrides

Prefix: VUEPAY_

String fields:

1. VUEPAY_SERVER_BASE_URL
2. VUEPAY_AUTH_PHONE_NUMBER
3. VUEPAY_AUTH_PASSWORD
4. VUEPAY_AUTH_OTP
5. VUEPAY_USER_PHONE_NUMBER
6. VUEPAY_USER_ID
7. VUEPAY_USER_ACCOUNT_NO
8. VUEPAY_PAYMENT_BENEFICIARY_NAME
9. VUEPAY_PAYMENT_BENEFICIARY_ACCOUNT
10. VUEPAY_LOGGING_LEVEL
11. VUEPAY_LOGGING_FORMAT
12. VUEPAY_LOGGING_OUTPUT
13. VUEPAY_WEBHOOK_LISTEN_ADDR
14. VUEPAY_WEBHOOK_SECRET

Numeric fields:

1. VUEPAY_AUTH_TOKEN_TTL_MINUTES
2. VUEPAY_PAYMENT_AMOUNT
3. VUEPAY_PRODUCER_TARGET_TPS
4. VUEPAY_PRODUCER_WORKER_COUNT
5. VUEPAY_PRODUCER_HTTP_TIMEOUT_MS
6. VUEPAY_PRODUCER_STATS_INTERVAL_SECONDS

Boolean fields:

1. VUEPAY_SERVER_TLS_SKIP_VERIFY
2. VUEPAY_WEBHOOK_ENABLED
3. VUEPAY_WEBHOOK_AUTO_START

## Common Issues

1. Initial auth failed with dial tcp i/o timeout:
Cause: host cannot reach gateway on configured port.

2. curl works on one machine but app fails on another:
Cause: network path, firewall, proxy, or source IP allowlist differs by host.

3. Producer starts but TPS is far below target:
Cause: worker_count and timeout impose concurrency ceiling, or downstream backend is saturated.

4. Webhook requests return 401:
Cause: missing or incorrect X-Webhook-Secret header when webhook.secret is set.
