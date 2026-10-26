package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
