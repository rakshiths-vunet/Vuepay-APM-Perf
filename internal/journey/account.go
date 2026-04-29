package journey

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/your-org/vuepay-producer/internal/config"
)

type AccountJourney struct {
	cfg    *config.Config
	client *http.Client
	inflight chan struct{}
}

func NewAccountJourney(cfg *config.Config, client *http.Client) *AccountJourney {
	return &AccountJourney{
		cfg:      cfg,
		client:   client,
		inflight: make(chan struct{}, maxAsyncInFlight(cfg.Producer.WorkerCount)),
	}
}

func (j *AccountJourney) Name() string {
	return "account"
}

func (j *AccountJourney) Execute(ctx context.Context, token string) error {
	select {
	case j.inflight <- struct{}{}:
	default:
		return nil
	}

	// Fire-and-forget: send request in background, return immediately
	go func() {
		defer func() { <-j.inflight }()

		payload := map[string]string{"phone": j.cfg.User.PhoneNumber}
		body, _ := json.Marshal(payload)
		endpoint := strings.TrimRight(j.cfg.Server.BaseURL, "/") + "/vuepay/gateway-v4/account"

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := j.client.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()

	return nil
}

func maxAsyncInFlight(workerCount int) int {
	if workerCount <= 0 {
		return 1000
	}
	if workerCount < 1000 {
		return 1000
	}
	return workerCount * 2
}
