package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestChatRejectsNormalizedKeyCollision 验证密钥名规范化冲突拒绝发布配置。
func TestChatRejectsNormalizedKeyCollision(t *testing.T) {
	directory := t.TempDir()
	text := "chat_model:\n  - provider: test\n    base_url: https://example.test\n    models:\n      - model: model-a\n        context_window: 32768\n      - model: model_a\n        context_window: 32768\n"
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err = manager.LoadConfig(); err == nil {
		t.Fatal("规范化到同一密钥名的模型配置必须报错")
	}
}

// TestChatRejectsNegativeRoundLimit 验证普通轮数上限由统一配置校验，非法更新不发布。
func TestChatRejectsNegativeRoundLimit(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("chat:\n  max_rounds: 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err = manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	// 此用例只验证显式加载，避免目录监听读到覆盖写入的空文件中间态。
	manager.Close()
	if err = os.WriteFile(path, []byte("chat:\n  max_rounds: -1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = manager.LoadConfig(); err == nil {
		t.Fatal("负数轮数上限必须拒绝")
	}
	current, err := manager.CurrentConfig()
	if err != nil || current.Chat.MaxRounds != 7 {
		t.Fatalf("非法配置污染原快照: %+v %v", current.Chat, err)
	}
}

// TestChatToolSettingsRejectNegativeAndKeepSnapshot 验证工具配置非法更新不污染快照，重试指针独立。
func TestChatToolSettingsRejectNegativeAndKeepSnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("chat:\n  tool_concurrency: 2\n  tool_timeout_seconds: 8\n  tool_max_retry: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	// 热更新另有真实目录监听用例；此处只验证非法值拒绝和快照隔离。
	manager.Close()
	for _, field := range []string{"tool_concurrency", "tool_timeout_seconds", "tool_max_retry"} {
		if err := os.WriteFile(path, []byte("chat:\n  "+field+": -1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := manager.LoadConfig(); err == nil {
			t.Fatalf("负数 %s 被接受", field)
		}
		cfg, err := manager.CurrentConfig()
		if err != nil || cfg.Chat.ToolConcurrency != 2 || cfg.Chat.ToolTimeoutSeconds != 8 || cfg.Chat.ToolMaxRetry == nil || *cfg.Chat.ToolMaxRetry != 1 {
			t.Fatalf("非法配置污染快照: %+v %v", cfg.Chat, err)
		}
		*cfg.Chat.ToolMaxRetry = 99
		fresh, err := manager.CurrentConfig()
		if err != nil || *fresh.Chat.ToolMaxRetry != 1 {
			t.Fatal("工具重试指针污染快照", err)
		}
	}
}

// TestChatSecretsAndTimeoutsReloadFromFixedDataDirectory 验证实例目录密钥变更和超时配置热更新。
func TestChatSecretsAndTimeoutsReloadFromFixedDataDirectory(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	models := "chat_model:\n  - provider: acme.lab\n    base_url: https://example.test\n    models:\n      - model: model-a\n        context_window: 32768\n"
	if err := os.WriteFile(configPath, []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err = manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	configs, err := manager.ChatModels()
	if err != nil || len(configs) != 1 || configs[0].Key != "" || configs[0].MaxRetry != 3 || configs[0].MaxRounds != 20 || configs[0].FirstTimeout != 120*time.Second || configs[0].IdleTimeout != 60*time.Second || configs[0].ToolConcurrency != 3 || configs[0].ToolTimeout != 30*time.Second || configs[0].ToolMaxRetry == nil || *configs[0].ToolMaxRetry != 3 {
		t.Fatalf("默认值错误: err=%v", err)
	}
	for _, key := range []string{"first", "replacement", ""} {
		if err = os.WriteFile(filepath.Join(directory, ".env"), []byte("ACME_LAB_MODEL_A="+key+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		configs, err = manager.ChatModels()
		if err != nil || configs[0].Key != key || configs[0].Provider != "acme.lab" || configs[0].Model != "model-a" {
			t.Fatal("新增、替换或清空密钥没有生效")
		}
	}
	if err = os.Remove(filepath.Join(directory, ".env")); err != nil {
		t.Fatal(err)
	}
	configs, err = manager.ChatModels()
	if err != nil || configs[0].Key != "" {
		t.Fatal("删除 .env 没有生效")
	}
	if err = os.WriteFile(configPath, []byte(models+"chat:\n  max_retry: 0\n  max_rounds: 7\n  first_timeout_seconds: 7\n  idle_timeout_seconds: 4\n  tool_concurrency: 2\n  tool_timeout_seconds: 8\n  tool_max_retry: 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		configs, err = manager.ChatModels()
		if err == nil && configs[0].MaxRetry == 0 && configs[0].MaxRounds == 7 && configs[0].FirstTimeout == 7*time.Second && configs[0].IdleTimeout == 4*time.Second && configs[0].ToolConcurrency == 2 && configs[0].ToolTimeout == 8*time.Second && *configs[0].ToolMaxRetry == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("配置监听未发布最新超时和零重试预算")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestChatConfigSnapshotsAreIndependent 验证模型配置快照相互独立且缺密钥不阻断加载。
func TestChatConfigSnapshotsAreIndependent(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte("chat_model:\n  - provider: test\n    base_url: https://example.test\n    models:\n      - model: model\n        context_window: 32768\n        max_output_tokens: 8192\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err = manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	first, err := manager.CurrentConfig()
	if err != nil {
		t.Fatal(err)
	}
	first.ChatModel[0].Provider = "mutated"
	*first.ChatModel[0].Models[0].MaxOutputTokens = 0
	second, err := manager.CurrentConfig()
	if err != nil {
		t.Fatal(err)
	}
	if second.ChatModel[0].Provider != "test" || *second.ChatModel[0].Models[0].MaxOutputTokens != 8192 {
		t.Fatal("修改值快照污染了已发布的配置")
	}
	configs, err := manager.ChatModels()
	if err != nil || len(configs) != 1 || configs[0].Key != "" {
		t.Fatal("缺少密钥仍应正常读取配置", err)
	}
	if err = os.WriteFile(filepath.Join(directory, ".env"), []byte("TEST_MODEL=local-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	configs, err = manager.ChatModels()
	if err != nil || configs[0].Key != "local-secret" {
		t.Fatal("新增密钥无需重启", err)
	}
}
