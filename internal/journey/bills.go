package journey

import (
	"context"
	"net/http"
	"strings"

	"github.com/your-org/vuepay-producer/internal/config"
)

type BillsJourney struct {
	cfg    *config.Config
	client *http.Client
	inflight chan struct{}
}

func NewBillsJourney(cfg *config.Config, client *http.Client) *BillsJourney {
	return &BillsJourney{
		cfg:      cfg,
		client:   client,
		inflight: make(chan struct{}, maxAsyncInFlight(cfg.Producer.WorkerCount)),
	}
}

func (j *BillsJourney) Name() string {
	return "bills"
}

func (j *BillsJourney) Execute(ctx context.Context, token string) error {
	select {
	case j.inflight <- struct{}{}:
	default:
		return nil
	}

	// Fire-and-forget: send request in background, return immediately
	go func() {
		defer func() { <-j.inflight }()

		endpoint := strings.TrimRight(j.cfg.Server.BaseURL, "/") + "/vuepay/gateway-v4/bills"
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := j.client.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()

	return nil
}
