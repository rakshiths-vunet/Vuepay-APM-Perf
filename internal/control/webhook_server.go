package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/vuepay-producer/internal/config"
)

type WebhookServer struct {
	server     *http.Server
	controller *Controller
	cfgPath    string
	secret     string
	logger     *zap.Logger
}

type configureRequest struct {
	ConfigPath string         `json:"config_path"`
	Config     *config.Config `json:"config"`
	Restart    bool           `json:"restart"`
	Start      bool           `json:"start"`
}

func NewWebhookServer(cfg *config.Config, cfgPath string, controller *Controller, logger *zap.Logger) *WebhookServer {
	mux := http.NewServeMux()
	ws := &WebhookServer{
		controller: controller,
		cfgPath:    cfgPath,
		secret:     cfg.Webhook.Secret,
		logger:     logger,
	}

	mux.HandleFunc("/webhook/status", ws.wrap(ws.handleStatus))
	mux.HandleFunc("/webhook/start", ws.wrap(ws.handleStart))
	mux.HandleFunc("/webhook/stop", ws.wrap(ws.handleStop))
	mux.HandleFunc("/webhook/restart", ws.wrap(ws.handleRestart))
	mux.HandleFunc("/webhook/configure", ws.wrap(ws.handleConfigure))

	ws.server = &http.Server{
		Addr:              cfg.Webhook.ListenAddr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return ws
}

func (s *WebhookServer) Start() {
	go func() {
		s.logger.Info("webhook server listening", zap.String("addr", s.server.Addr))
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("webhook server failed", zap.Error(err))
		}
	}()
}

func (s *WebhookServer) Shutdown(ctx context.Context) {
	_ = s.server.Shutdown(ctx)
}

func (s *WebhookServer) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.secret != "" {
			if r.Header.Get("X-Webhook-Secret") != s.secret {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
		}
		next(w, r)
	}
}

func (s *WebhookServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": s.controller.Status(),
	})
}

func (s *WebhookServer) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	go func() {
		if err := s.controller.Start(); err != nil {
			s.logger.Warn("async start failed", zap.Error(err))
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"message": "start triggered"})
}

func (s *WebhookServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	s.controller.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"message": "stopped"})
}

func (s *WebhookServer) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	if err := s.controller.Restart(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "restarted"})
}

func (s *WebhookServer) handleConfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var req configureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON payload"})
		return
	}

	nextCfg, err := s.resolveConfig(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if err := s.controller.Configure(nextCfg, req.Restart, req.Start); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "configured",
		"status":  s.controller.Status(),
	})
}

func (s *WebhookServer) resolveConfig(req configureRequest) (*config.Config, error) {
	hasPath := strings.TrimSpace(req.ConfigPath) != ""
	hasInline := req.Config != nil
	if !hasPath && !hasInline {
		if s.cfgPath == "" {
			return nil, fmt.Errorf("provide config_path or config payload")
		}
		loaded, err := config.Load(s.cfgPath)
		if err != nil {
			return nil, err
		}
		return loaded, nil
	}

	if hasPath {
		loaded, err := config.Load(req.ConfigPath)
		if err != nil {
			return nil, err
		}
		return loaded, nil
	}

	next := config.Clone(req.Config)
	if err := config.Prepare(next); err != nil {
		return nil, err
	}
	return next, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
