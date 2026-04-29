package journey

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/your-org/vuepay-producer/internal/config"
)

type PaymentJourney struct {
	cfg    *config.Config
	client *http.Client
	inflight chan struct{}
}

func NewPaymentJourney(cfg *config.Config, client *http.Client) *PaymentJourney {
	return &PaymentJourney{
		cfg:      cfg,
		client:   client,
		inflight: make(chan struct{}, maxAsyncInFlight(cfg.Producer.WorkerCount)),
	}
}

func (j *PaymentJourney) Name() string {
	return "payment_initiate"
}

func (j *PaymentJourney) Execute(ctx context.Context, token string) error {
	select {
	case j.inflight <- struct{}{}:
	default:
		return nil
	}

	// Fire-and-forget: send request in background, return immediately
	go func() {
		defer func() { <-j.inflight }()

		payload := map[string]any{
			"userId":             j.cfg.User.UserID,
			"accountNo":          j.cfg.User.AccountNo,
			"account_number":     j.cfg.User.AccountNo,
			"type":               "Digital Transfer",
			"amount":             j.cfg.Payment.Amount,
			"channel":            "BROWSER",
			"beneficiaryName":    j.cfg.Payment.BeneficiaryName,
			"beneficiaryAccount": j.cfg.Payment.BeneficiaryAccount,
		}
		body, _ := json.Marshal(payload)
		endpoint := strings.TrimRight(j.cfg.Server.BaseURL, "/") + "/vuepay/gateway-v4/payment/initiate"

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
