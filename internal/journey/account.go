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

type AccountJourney struct {
	cfg    *config.Config
	client *http.Client
}

func NewAccountJourney(cfg *config.Config, client *http.Client) *AccountJourney {
	return &AccountJourney{cfg: cfg, client: client}
}

func (j *AccountJourney) Name() string {
	return "account"
}

func (j *AccountJourney) Execute(ctx context.Context, token string) error {
	payload := map[string]string{"phone": j.cfg.User.PhoneNumber}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(j.cfg.Server.BaseURL, "/") + "/vuepay/gateway/account"

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
		return fmt.Errorf("account status %d body=%s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("account unsuccessful: %s", string(respBody))
	}
	return nil
}
