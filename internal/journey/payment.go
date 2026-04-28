package journey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/your-org/vuepay-producer/internal/config"
)

type PaymentJourney struct {
	cfg    *config.Config
	client *http.Client
}

func NewPaymentJourney(cfg *config.Config, client *http.Client) *PaymentJourney {
	return &PaymentJourney{cfg: cfg, client: client}
}

func (j *PaymentJourney) Name() string {
	return "payment_initiate"
}

func (j *PaymentJourney) Execute(ctx context.Context, token string) error {
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
	endpoint := strings.TrimRight(j.cfg.Server.BaseURL, "/") + "/vuepay/gateway/payment/initiate"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("payment status %d body=%s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Success bool   `json:"success"`
		TxnRef  string `json:"txnRef"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("payment unsuccessful: %s", string(respBody))
	}
	return nil
}
