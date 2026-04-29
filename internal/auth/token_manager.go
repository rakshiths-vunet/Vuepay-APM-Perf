package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/vuepay-producer/internal/config"
)

type TokenManager struct {
	cfg           *config.Config
	client        *http.Client
	logger        *zap.Logger
	mu            sync.RWMutex
	currentToken  string
	refreshSignal chan struct{}
}

type loginResponse struct {
	Success       bool   `json:"success"`
	Message       string `json:"message"`
	TransactionID string `json:"transaction_id"`
}

type verifyResponse struct {
	Success     bool   `json:"success"`
	AccessToken string `json:"access_token"`
}

func NewTokenManager(cfg *config.Config, client *http.Client, logger *zap.Logger) *TokenManager {
	return &TokenManager{
		cfg:           cfg,
		client:        client,
		logger:        logger,
		refreshSignal: make(chan struct{}, 1),
	}
}

func (tm *TokenManager) Start(ctx context.Context) error {
	if err := tm.doRefresh(ctx); err != nil {
		// Retry with backoff on initial auth failure
		backoff := 500 * time.Millisecond
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(backoff):
			}
			if err := tm.doRefresh(ctx); err == nil {
				go tm.startRefreshLoop(ctx)
				return nil
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
		return err
	}
	go tm.startRefreshLoop(ctx)
	return nil
}

func (tm *TokenManager) GetToken() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.currentToken
}

func (tm *TokenManager) RefreshNow() {
	select {
	case tm.refreshSignal <- struct{}{}:
	default:
	}
}

func (tm *TokenManager) startRefreshLoop(ctx context.Context) {
	interval := time.Duration(tm.cfg.Auth.TokenTTLMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tm.refreshWithRetry(ctx)
		case <-tm.refreshSignal:
			tm.refreshWithRetry(ctx)
		}
	}
}

func (tm *TokenManager) refreshWithRetry(ctx context.Context) {
	if err := tm.doRefresh(ctx); err == nil {
		return
	} else {
		tm.logger.Error("token refresh failed", zap.Error(err))
	}

	backoff := 500 * time.Millisecond
	for i := 0; i < 10; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if err := tm.doRefresh(ctx); err == nil {
			return
		} else {
			tm.logger.Warn("token refresh retry failed", zap.Int("attempt", i+1), zap.Error(err))
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (tm *TokenManager) doRefresh(ctx context.Context) error {
	transactionID, err := tm.login(ctx)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	accessToken, err := tm.verifyOTP(ctx, transactionID)
	if err != nil {
		return fmt.Errorf("verify otp failed: %w", err)
	}

	tm.mu.Lock()
	tm.currentToken = accessToken
	tm.mu.Unlock()

	tm.logger.Debug("token refreshed")
	return nil
}

func (tm *TokenManager) login(ctx context.Context) (string, error) {
	payload := map[string]string{
		"phone_number": tm.cfg.Auth.PhoneNumber,
		"password":     tm.cfg.Auth.Password,
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(tm.cfg.Server.BaseURL, "/") + "/vuepay/gateway-v4/auth/login"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d body=%s", resp.StatusCode, string(respBody))
	}

	var out loginResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	if !out.Success || out.TransactionID == "" {
		return "", fmt.Errorf("login unsuccessful: %s", string(respBody))
	}
	return out.TransactionID, nil
}

func (tm *TokenManager) verifyOTP(ctx context.Context, transactionID string) (string, error) {
	payload := map[string]string{
		"transaction_id": transactionID,
		"otp":            tm.cfg.Auth.OTP,
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(tm.cfg.Server.BaseURL, "/") + "/vuepay/gateway-v4/auth/verify-otp"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d body=%s", resp.StatusCode, string(respBody))
	}

	var out verifyResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	if !out.Success || out.AccessToken == "" {
		return "", fmt.Errorf("otp verification unsuccessful: %s", string(respBody))
	}
	return out.AccessToken, nil
}
