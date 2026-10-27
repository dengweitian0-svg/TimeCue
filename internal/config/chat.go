package config

import (
	"fmt"
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
	MaxRetry            *int `mapstructure:"max_retry"`
	FirstTimeoutSeconds int  `mapstructure:"first_timeout_seconds"`
	IdleTimeoutSeconds  int  `mapstructure:"idle_timeout_seconds"`
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
	if cfg.Chat.MaxRetry != nil && *cfg.Chat.MaxRetry < 0 || cfg.Chat.FirstTimeoutSeconds < 0 || cfg.Chat.IdleTimeoutSeconds < 0 {
		return fmt.Errorf("chat retry and timeouts must not be negative")
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
			if cfg.Chat.FirstTimeoutSeconds > 0 {
				first = cfg.Chat.FirstTimeoutSeconds
			}
			if cfg.Chat.IdleTimeoutSeconds > 0 {
				idle = cfg.Chat.IdleTimeoutSeconds
			}
			keyName := chatKeyName(p.Provider, model.Model)
			key := strings.TrimSpace(keys[keyName])
			u, e := url.Parse(p.BaseURL)
			if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
				key = ""
			}
			result = append(result, chat.ModelConfig{
				Provider:        p.Provider,
				Model:           model.Model,
				BaseURL:         p.BaseURL,
				Key:             key,
				ContextWindow:   model.ContextWindow,
				MaxOutputTokens: output,
				MaxRetry:        retries,
				FirstTimeout:    time.Duration(first) * time.Second,
				IdleTimeout:     time.Duration(idle) * time.Second,
			})
		}
	}
	return result, nil
}
