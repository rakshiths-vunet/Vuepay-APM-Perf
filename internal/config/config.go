package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	User     UserConfig     `yaml:"user"`
	Payment  PaymentConfig  `yaml:"payment"`
	Producer ProducerConfig `yaml:"producer"`
	Webhook  WebhookConfig  `yaml:"webhook"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	BaseURL       string `yaml:"base_url"`
	TLSSkipVerify bool   `yaml:"tls_skip_verify"`
}

type AuthConfig struct {
	PhoneNumber     string `yaml:"phone_number"`
	Password        string `yaml:"password"`
	OTP             string `yaml:"otp"`
	TokenTTLMinutes int    `yaml:"token_ttl_minutes"`
}

type UserConfig struct {
	PhoneNumber string `yaml:"phone_number"`
	UserID      string `yaml:"user_id"`
	AccountNo   string `yaml:"account_no"`
}

type PaymentConfig struct {
	Amount             int    `yaml:"amount"`
	BeneficiaryName    string `yaml:"beneficiary_name"`
	BeneficiaryAccount string `yaml:"beneficiary_account"`
}

type ProducerConfig struct {
	TargetTPS            int            `yaml:"target_tps"`
	WorkerCount          int            `yaml:"worker_count"`
	JourneyWeights       map[string]int `yaml:"journey_weights"`
	HTTPTimeoutMs        int            `yaml:"http_timeout_ms"`
	StatsIntervalSeconds int            `yaml:"stats_interval_seconds"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

type WebhookConfig struct {
	Enabled   bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listen_addr"`
	Secret    string `yaml:"secret"`
	AutoStart bool   `yaml:"auto_start"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	applyEnvOverrides(&cfg)
	if err := Prepare(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func Prepare(cfg *Config) error {
	normalize(cfg)
	return validate(cfg)
}

func Clone(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	copyCfg := *cfg
	if cfg.Producer.JourneyWeights != nil {
		copyCfg.Producer.JourneyWeights = make(map[string]int, len(cfg.Producer.JourneyWeights))
		for key, value := range cfg.Producer.JourneyWeights {
			copyCfg.Producer.JourneyWeights[key] = value
		}
	}
	return &copyCfg
}

func applyEnvOverrides(cfg *Config) {
	overrides := map[string]*string{
		"VUEPAY_SERVER_BASE_URL":             &cfg.Server.BaseURL,
		"VUEPAY_AUTH_PHONE_NUMBER":           &cfg.Auth.PhoneNumber,
		"VUEPAY_AUTH_PASSWORD":               &cfg.Auth.Password,
		"VUEPAY_AUTH_OTP":                    &cfg.Auth.OTP,
		"VUEPAY_USER_PHONE_NUMBER":           &cfg.User.PhoneNumber,
		"VUEPAY_USER_ID":                     &cfg.User.UserID,
		"VUEPAY_USER_ACCOUNT_NO":             &cfg.User.AccountNo,
		"VUEPAY_PAYMENT_BENEFICIARY_NAME":    &cfg.Payment.BeneficiaryName,
		"VUEPAY_PAYMENT_BENEFICIARY_ACCOUNT": &cfg.Payment.BeneficiaryAccount,
		"VUEPAY_LOGGING_LEVEL":               &cfg.Logging.Level,
		"VUEPAY_LOGGING_FORMAT":              &cfg.Logging.Format,
		"VUEPAY_LOGGING_OUTPUT":              &cfg.Logging.Output,
		"VUEPAY_WEBHOOK_LISTEN_ADDR":         &cfg.Webhook.ListenAddr,
		"VUEPAY_WEBHOOK_SECRET":              &cfg.Webhook.Secret,
	}
	for key, ptr := range overrides {
		if v := os.Getenv(key); v != "" {
			*ptr = v
		}
	}

	if v := getenvInt("VUEPAY_AUTH_TOKEN_TTL_MINUTES"); v > 0 {
		cfg.Auth.TokenTTLMinutes = v
	}
	if v := getenvInt("VUEPAY_PAYMENT_AMOUNT"); v > 0 {
		cfg.Payment.Amount = v
	}
	if v := getenvInt("VUEPAY_PRODUCER_TARGET_TPS"); v > 0 {
		cfg.Producer.TargetTPS = v
	}
	if v := getenvInt("VUEPAY_PRODUCER_WORKER_COUNT"); v > 0 {
		cfg.Producer.WorkerCount = v
	}
	if v := getenvInt("VUEPAY_PRODUCER_HTTP_TIMEOUT_MS"); v > 0 {
		cfg.Producer.HTTPTimeoutMs = v
	}
	if v := getenvInt("VUEPAY_PRODUCER_STATS_INTERVAL_SECONDS"); v > 0 {
		cfg.Producer.StatsIntervalSeconds = v
	}

	if v := os.Getenv("VUEPAY_SERVER_TLS_SKIP_VERIFY"); v != "" {
		cfg.Server.TLSSkipVerify = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("VUEPAY_WEBHOOK_ENABLED"); v != "" {
		cfg.Webhook.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("VUEPAY_WEBHOOK_AUTO_START"); v != "" {
		cfg.Webhook.AutoStart = strings.EqualFold(v, "true") || v == "1"
	}
}

func normalize(cfg *Config) {
	if cfg.Auth.TokenTTLMinutes == 0 {
		cfg.Auth.TokenTTLMinutes = 7
	}
	if cfg.Producer.StatsIntervalSeconds == 0 {
		cfg.Producer.StatsIntervalSeconds = 1
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if cfg.Logging.Output == "" {
		cfg.Logging.Output = "stdout"
	}
	if cfg.Webhook.ListenAddr == "" {
		cfg.Webhook.ListenAddr = ":8090"
	}
}

func validate(cfg *Config) error {
	if cfg.Server.BaseURL == "" {
		return fmt.Errorf("server.base_url is required")
	}
	if cfg.Auth.PhoneNumber == "" {
		return fmt.Errorf("auth.phone_number is required")
	}
	if cfg.Auth.Password == "" {
		return fmt.Errorf("auth.password is required")
	}
	if cfg.Auth.OTP == "" {
		return fmt.Errorf("auth.otp is required")
	}
	if cfg.Auth.TokenTTLMinutes <= 0 {
		return fmt.Errorf("auth.token_ttl_minutes must be > 0")
	}
	if cfg.User.PhoneNumber == "" {
		return fmt.Errorf("user.phone_number is required")
	}
	if cfg.User.UserID == "" {
		return fmt.Errorf("user.user_id is required")
	}
	if cfg.User.AccountNo == "" {
		return fmt.Errorf("user.account_no is required")
	}
	if cfg.Payment.Amount <= 0 {
		return fmt.Errorf("payment.amount must be > 0")
	}
	if cfg.Payment.BeneficiaryName == "" {
		return fmt.Errorf("payment.beneficiary_name is required")
	}
	if cfg.Payment.BeneficiaryAccount == "" {
		return fmt.Errorf("payment.beneficiary_account is required")
	}
	if cfg.Producer.TargetTPS <= 0 {
		return fmt.Errorf("producer.target_tps must be > 0")
	}
	if cfg.Producer.WorkerCount <= 0 {
		return fmt.Errorf("producer.worker_count must be > 0")
	}
	if cfg.Producer.HTTPTimeoutMs <= 0 {
		return fmt.Errorf("producer.http_timeout_ms must be > 0")
	}
	if cfg.Producer.StatsIntervalSeconds <= 0 {
		return fmt.Errorf("producer.stats_interval_seconds must be > 0")
	}
	if len(cfg.Producer.JourneyWeights) == 0 {
		return fmt.Errorf("producer.journey_weights must not be empty")
	}
	for journeyName, w := range cfg.Producer.JourneyWeights {
		if w <= 0 {
			return fmt.Errorf("producer.journey_weights.%s must be > 0", journeyName)
		}
	}
	if cfg.Webhook.ListenAddr == "" {
		return fmt.Errorf("webhook.listen_addr is required")
	}
	return nil
}

func getenvInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	var parsed int
	_, _ = fmt.Sscanf(v, "%d", &parsed)
	return parsed
}
