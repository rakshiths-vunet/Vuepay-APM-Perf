# PRD & Copilot Prompt: VuePay High-Throughput Go Journey Producer

> **Dual-purpose document**: This file serves both as a Product Requirements Document (PRD) and as a direct prompt for GitHub Copilot / AI coding assistants to implement the project.

---

## 1. Project Overview

Build a **high-throughput, config-driven Go application** that continuously produces journey API calls against the VuePay gateway, sustaining a minimum of **6,000 journeys per second**. The producer handles authentication lifecycle automatically — fetching new bearer tokens every 7 minutes — and fires three distinct journey types in a tight concurrent loop.

---

## 2. Goals & Success Criteria

| Goal | Target |
|------|--------|
| Journey throughput | ≥ 6,000 journeys/second sustained |
| Token refresh | Proactive rotation every 7 minutes (token lives 10 min, refresh at 7) |
| Config-driven | Zero code changes needed to alter endpoints, credentials, or load params |
| Observability | Real-time TPS counter, per-journey success/error rates printed every second |
| Resilience | Auth failures trigger immediate token refresh; journey errors are logged but do not stop the producer |

---

## 3. Authentication Flow

The authentication is a **two-step process**. Both steps must be completed before any journey API can be called.

### Step 1 — Login (get `transaction_id`)

```
POST /vuepay/gateway-v4/auth/login
Content-Type: application/json

{
  "phone_number": "<config>",
  "password": "<config>"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Password verified and OTP sent successfully",
  "transaction_id": "cb4c34ce-caa4-4b31-8e8c-bb1c258fcd4f"
}
```

### Step 2 — OTP Verify (get `access_token`)

```
POST /vuepay/gateway-v4/auth/verify-otp
Content-Type: application/json

{
  "transaction_id": "<from step 1>",
  "otp": "<config>"
}
```

**Response:**
```json
{
  "success": true,
  "access_token": "<JWT>",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

### Token Lifecycle

- Token is **valid for 10 minutes** server-side.
- Producer **rotates proactively at 7 minutes** to avoid mid-flight 401s.
- A single background goroutine manages token refresh; all worker goroutines read the current token via a shared `sync.RWMutex`-protected variable.
- If any journey returns HTTP 401, the producer triggers an **immediate emergency refresh** before retrying.

---

## 4. Journey Definitions

All three journeys share the same bearer token. Static fields (phone, account numbers, beneficiary, amount) come from config.

### Journey 1 — Account Details

```
POST /vuepay/gateway-v4/account
Authorization: Bearer <token>
Content-Type: application/json

{
  "phone": "<config.phone_number>"
}
```

**Expected response:**
```json
{
  "success": true,
  "data": { "accountName": "...", "accountNo": "...", "balance": 0, "status": "ACTIVE" }
}
```

### Journey 2 — Payment Initiate

```
POST /vuepay/gateway-v4/payment/initiate
Authorization: Bearer <token>
Content-Type: application/json

{
  "userId": "<config.user_id>",
  "accountNo": "<config.account_no>",
  "account_number": "<config.account_no>",
  "type": "Digital Transfer",
  "amount": <config.amount>,
  "channel": "BROWSER",
  "beneficiaryName": "<config.beneficiary_name>",
  "beneficiaryAccount": "<config.beneficiary_account>"
}
```

**Expected response:**
```json
{
  "success": true,
  "txnRef": "UTL-xxxx"
}
```

### Journey 3 — Bills Fetch

```
GET /vuepay/gateway-v4/bills
Authorization: Bearer <token>
```

**Expected response:** JSON with `"success": true`

---

## 5. Configuration Schema

The application is **100% config-driven** via a single YAML file (`config.yaml`). No hardcoded values anywhere in the code.

```yaml
# config.yaml — VuePay Go Producer Configuration

server:
  base_url: "https://home-blr-001.vunetsystems.com:8443"
  tls_skip_verify: true           # Set false in prod

auth:
  phone_number: "7795012759"
  password: "password"
  otp: "999999"
  token_ttl_minutes: 7            # Refresh interval (token lives 10min, refresh at 7)

user:
  phone_number: "7795012759"
  user_id: "User"
  account_no: "ACC1000000002"

payment:
  amount: 123
  beneficiary_name: "Ganesh"
  beneficiary_account: "ACC1000000001"

producer:
  target_tps: 6000                # Target journeys per second
  worker_count: 500               # Number of concurrent goroutines
  journey_weights:                # Relative call frequency per journey type
    account: 1
    payment_initiate: 1
    bills: 1
  http_timeout_ms: 5000           # Per-request HTTP timeout
  stats_interval_seconds: 1       # How often to print TPS stats to stdout

logging:
  level: "info"                   # debug | info | warn | error
  format: "json"                  # json | text
  output: "stdout"                # stdout | file path
```

---

## 6. Architecture & Design

### 6.1 Component Diagram

```
┌────────────────────────────────────────────────────┐
│                   main.go                          │
│  - Load config                                     │
│  - Start TokenManager                              │
│  - Start WorkerPool                                │
│  - Start StatsReporter                             │
└────────────────────────────────────────────────────┘
         │                    │                │
         ▼                    ▼                ▼
┌─────────────────┐  ┌──────────────┐  ┌─────────────────┐
│  TokenManager   │  │  WorkerPool  │  │  StatsReporter  │
│                 │  │              │  │                 │
│ - Login()       │  │ 500 gorout.  │  │ Prints TPS      │
│ - VerifyOTP()   │  │ each picks   │  │ every 1s        │
│ - Auto-refresh  │  │ journey type │  │                 │
│   every 7 min   │  │ fires HTTP   │  └─────────────────┘
│ - RWMutex token │  │ req          │
└─────────────────┘  └──────────────┘
         │                    │
         └────────────────────┘
              shared token
```

### 6.2 Worker Pool Design

- Spawn `config.producer.worker_count` goroutines at startup.
- Each worker runs an infinite loop: pick a journey → get current token → fire HTTP → record result.
- Journey type is selected round-robin (weighted by `journey_weights`) across workers.
- Use a **rate limiter** (`golang.org/x/time/rate`) capped at `target_tps` to prevent thundering herd.
- HTTP client is shared and pre-warmed with persistent keep-alive connections.

### 6.3 Token Manager

```
goroutine: tokenRefreshLoop
  every (token_ttl_minutes * 60) seconds:
    1. Call Login → get transaction_id
    2. Call VerifyOTP(transaction_id) → get access_token
    3. mu.Lock(); currentToken = access_token; mu.Unlock()
    4. Reset timer

on 401 from any worker:
    signal emergency refresh channel (non-blocking)
    tokenRefreshLoop drains channel and refreshes immediately
```

### 6.4 HTTP Client Configuration

```go
transport := &http.Transport{
    MaxIdleConns:        1000,
    MaxIdleConnsPerHost: 1000,
    MaxConnsPerHost:     1000,
    IdleConnTimeout:     90 * time.Second,
    TLSClientConfig:     &tls.Config{InsecureSkipVerify: cfg.Server.TLSSkipVerify},
    DisableKeepAlives:   false,
}
client := &http.Client{
    Transport: transport,
    Timeout:   time.Duration(cfg.Producer.HTTPTimeoutMs) * time.Millisecond,
}
```

---

## 7. Project File Structure

```
vuepay-producer/
├── cmd/
│   └── producer/
│       └── main.go              # Entry point
├── internal/
│   ├── auth/
│   │   └── token_manager.go     # Login, OTP verify, auto-refresh
│   ├── config/
│   │   └── config.go            # YAML config loader + struct definitions
│   ├── journey/
│   │   ├── account.go           # Journey 1: Account Details
│   │   ├── payment.go           # Journey 2: Payment Initiate
│   │   └── bills.go             # Journey 3: Bills Fetch
│   ├── producer/
│   │   ├── worker_pool.go       # Goroutine pool + rate limiter
│   │   └── dispatcher.go        # Journey selection logic (weighted round-robin)
│   └── stats/
│       └── reporter.go          # TPS counter + periodic stdout reporting
├── config.yaml                  # Runtime configuration (gitignored for prod)
├── config.example.yaml          # Safe example config to commit
├── go.mod
├── go.sum
└── README.md
```

---

## 8. Key Implementation Details for Copilot

### 8.1 Config Loading (`internal/config/config.go`)

```go
// Use github.com/spf13/viper or gopkg.in/yaml.v3 to load config.yaml
// Support env var overrides: VUEPAY_AUTH_PASSWORD etc.
// Validate all required fields at startup; exit with clear error if missing
type Config struct {
    Server   ServerConfig
    Auth     AuthConfig
    User     UserConfig
    Payment  PaymentConfig
    Producer ProducerConfig
    Logging  LoggingConfig
}
```

### 8.2 Token Manager (`internal/auth/token_manager.go`)

```go
type TokenManager struct {
    cfg           *config.Config
    client        *http.Client
    mu            sync.RWMutex
    currentToken  string
    refreshSignal chan struct{}
}

func (tm *TokenManager) GetToken() string  // RLock read
func (tm *TokenManager) RefreshNow()       // send to refreshSignal (non-blocking)
func (tm *TokenManager) startRefreshLoop() // goroutine: ticker + signal select
func (tm *TokenManager) doRefresh() error  // login → verifyOTP → set token
```

### 8.3 Journey Interface (`internal/journey/`)

```go
type Journey interface {
    Name() string
    Execute(ctx context.Context, token string) error
}

// Each journey file implements Journey:
// AccountJourney, PaymentJourney, BillsJourney
```

### 8.4 Stats Reporter (`internal/stats/reporter.go`)

```go
// Use atomic int64 counters per journey type for success/error counts
// Every stats_interval_seconds, compute delta TPS and print:
// [2026-04-28 10:00:01] TPS: 6124 | account: 2041 ok/0 err | payment: 2039 ok/1 err | bills: 2044 ok/0 err
```

### 8.5 Rate Limiting

```go
// golang.org/x/time/rate
limiter := rate.NewLimiter(rate.Limit(cfg.Producer.TargetTPS), cfg.Producer.TargetTPS)
// Each worker calls limiter.Wait(ctx) before firing the HTTP request
```

---

## 9. Dependencies

```
// go.mod
module github.com/your-org/vuepay-producer

go 1.22

require (
    gopkg.in/yaml.v3 v3.0.1
    golang.org/x/time v0.5.0
    go.uber.org/zap v1.27.0
)
```

---

## 10. README Requirements

The `README.md` must include:

1. **Prerequisites**: Go 1.22+
2. **Setup**: `cp config.example.yaml config.yaml` → edit credentials
3. **Run**: `go run ./cmd/producer`
4. **Build**: `go build -o vuepay-producer ./cmd/producer`
5. **Tuning guide**: How to adjust `worker_count` and `target_tps` to hit desired throughput
6. **Sample output**: What the stats line looks like at steady state

---

## 11. Out of Scope

- Kafka / message queue integration (journeys are fire-and-forget HTTP)
- Persistent storage of results
- Distributed multi-node execution
- Retry logic beyond a single immediate retry on 401

---

## 12. Open Questions / Assumptions

| # | Assumption |
|---|-----------|
| 1 | OTP `999999` is a static test OTP that always works in the target environment |
| 2 | All three journeys are fired with equal weight by default; weights are configurable |
| 3 | `tls_skip_verify: true` is acceptable for the internal test environment |
| 4 | The server can sustain 6,000 RPS; producer-side is the focus of this spec |
| 5 | A single phone number / account pair is used for all load (no user rotation needed) |

---

*Document version: 1.0 | Created: 2026-04-28*