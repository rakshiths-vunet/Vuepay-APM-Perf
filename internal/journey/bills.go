package journey

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/your-org/vuepay-producer/internal/config"
)

type BillsJourney struct {
	cfg    *config.Config
	client *http.Client
}

func NewBillsJourney(cfg *config.Config, client *http.Client) *BillsJourney {
	return &BillsJourney{cfg: cfg, client: client}
}

func (j *BillsJourney) Name() string {
	return "bills"
}

func (j *BillsJourney) Execute(ctx context.Context, token string) error {
	endpoint := strings.TrimRight(j.cfg.Server.BaseURL, "/") + "/vuepay/gateway/bills"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

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
		return fmt.Errorf("bills status %d body=%s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("bills unsuccessful: %s", string(respBody))
	}
	return nil
}
