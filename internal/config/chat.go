package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/subosito/gotenv"
)

// ChatProvider 是配置文件中的模型提供方。
type ChatProvider struct {
	Provider string      `mapstructure:"provider"`
	BaseURL  string      `mapstructure:"base_url"`
	Models   []ChatModel `mapstructure:"models"`
}

// ChatModel 是单个模型的上下文和输出预算。
type ChatModel struct {
	Model           string `mapstructure:"model"`
	ContextWindow   int    `mapstructure:"context_window"`
	MaxOutputTokens *int   `mapstructure:"max_output_tokens"`
}

// ChatRuntime 是模型请求的统一运行参数，零值使用默认值。
type ChatRuntime struct {
	ToolResultMaxTokens int     `mapstructure:"tool_result_max_tokens"`
	CompactThreshold    float64 `mapstructure:"compact_threshold"`
	MaxRounds           int     `mapstructure:"max_rounds"`
	MaxContinue         int     `mapstructure:"max_continue"`
	MaxRetry            *int    `mapstructure:"max_retry"`
	FirstTimeoutSeconds int     `mapstructure:"first_timeout_seconds"`
	IdleTimeoutSeconds  int     `mapstructure:"idle_timeout_seconds"`
	ToolConcurrency     int     `mapstructure:"tool_concurrency"`
	ToolTimeoutSeconds  int     `mapstructure:"tool_timeout_seconds"`
	ToolMaxRetry        *int    `mapstructure:"tool_max_retry"`
}

func chatKeyName(provider, model string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, provider+"_"+model)
}

func validateChatConfig(cfg *Config) error {
	if value := cfg.Chat.ToolResultMaxTokens; value != 0 && (value < 64 || value > int(^uint(0)>>1)/4) {
		return fmt.Errorf("chat tool_result_max_tokens must be zero or between 64 and max_int/4")
	}
	if math.IsNaN(cfg.Chat.CompactThreshold) || math.IsInf(cfg.Chat.CompactThreshold, 0) || cfg.Chat.CompactThreshold < 0 || cfg.Chat.CompactThreshold > 1 {
		return fmt.Errorf("chat compact_threshold must be between 0 and 1")
	}
	keys := map[string]bool{}
	for _, provider := range cfg.ChatModel {
		for _, model := range provider.Models {
			if strings.TrimSpace(provider.Provider) == "" || strings.TrimSpace(model.Model) == "" {
				return fmt.Errorf("chat provider and model must not be empty")
			}
			key := chatKeyName(provider.Provider, model.Model)
			if keys[key] {
				return fmt.Errorf("chat model key normalization conflict")
			}
			keys[key] = true
		}
	}
	if cfg.Chat.MaxRetry != nil && *cfg.Chat.MaxRetry < 0 || cfg.Chat.MaxRounds < 0 || cfg.Chat.MaxContinue < 0 || cfg.Chat.FirstTimeoutSeconds < 0 || cfg.Chat.IdleTimeoutSeconds < 0 {
		return fmt.Errorf("chat rounds, retry and timeouts must not be negative")
	}
	if cfg.Chat.ToolConcurrency < 0 || cfg.Chat.ToolTimeoutSeconds < 0 || cfg.Chat.ToolMaxRetry != nil && *cfg.Chat.ToolMaxRetry < 0 {
		return fmt.Errorf("tool concurrency, retry and timeout must not be negative")
	}
	return nil
}

// ChatModels 读取当前配置和同一数据目录的最新密钥，不进行上游探测。
func (m *Manager) ChatModels() ([]chat.ModelConfig, error) {
	cfg, err := m.CurrentConfig()
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	file, err := os.Open(filepath.Join(m.dataDir, ".env"))
	if err == nil {
		defer file.Close()
		parsed, e := gotenv.StrictParse(file)
		if e != nil {
			return nil, fmt.Errorf("invalid .env format")
		}
		keys = parsed
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read .env")
	}
	result := []chat.ModelConfig{}
	for _, p := range cfg.ChatModel {
		for _, model := range p.Models {
			output := 8192
			if model.MaxOutputTokens != nil {
				output = *model.MaxOutputTokens
			}
			retries := 3
			if cfg.Chat.MaxRetry != nil {
				retries = *cfg.Chat.MaxRetry
			}
			if retries < 0 {
				retries = 0
			}
			first, idle := 120, 60
			rounds := cfg.Chat.MaxRounds
			if rounds == 0 {
				rounds = 20
			}
			continues := cfg.Chat.MaxContinue
			threshold := cfg.Chat.CompactThreshold
			if threshold == 0 {
				threshold = 0.8
			}
			if continues == 0 {
				continues = 5
			}
			if cfg.Chat.FirstTimeoutSeconds > 0 {
				first = cfg.Chat.FirstTimeoutSeconds
			}
			if cfg.Chat.IdleTimeoutSeconds > 0 {
				idle = cfg.Chat.IdleTimeoutSeconds
			}
			keyName := chatKeyName(p.Provider, model.Model)
			toolConcurrency, toolTimeout, toolRetry := 3, 30, 3
			if cfg.Chat.ToolConcurrency > 0 {
				toolConcurrency = cfg.Chat.ToolConcurrency
			}
			if cfg.Chat.ToolTimeoutSeconds > 0 {
				toolTimeout = cfg.Chat.ToolTimeoutSeconds
			}
			if cfg.Chat.ToolMaxRetry != nil {
				toolRetry = *cfg.Chat.ToolMaxRetry
			}
			key := strings.TrimSpace(keys[keyName])
			toolResultTokens := cfg.Chat.ToolResultMaxTokens
			if toolResultTokens == 0 {
				toolResultTokens = 8192
			}
			u, e := url.Parse(p.BaseURL)
			if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
				key = ""
			}
			result = append(result, chat.ModelConfig{
				Provider:            p.Provider,
				Model:               model.Model,
				BaseURL:             p.BaseURL,
				Key:                 key,
				ContextWindow:       model.ContextWindow,
				MaxOutputTokens:     output,
				MaxRetry:            retries,
				MaxRounds:           rounds,
				MaxContinue:         continues,
				CompactThreshold:    threshold,
				FirstTimeout:        time.Duration(first) * time.Second,
				IdleTimeout:         time.Duration(idle) * time.Second,
				ToolConcurrency:     toolConcurrency,
				ToolTimeout:         time.Duration(toolTimeout) * time.Second,
				ToolMaxRetry:        &toolRetry,
				ToolResultMaxTokens: toolResultTokens,
			})
		}
	}
	return result, nil
}
