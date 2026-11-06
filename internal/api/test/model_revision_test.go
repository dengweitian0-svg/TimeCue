package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

// TestModelListReportsPublishedRevision 验证列表只确认已发布版本，旧配置兼容且不泄露密钥。
func TestModelListReportsPublishedRevision(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	write := func(revision, extra string) {
		t.Helper()
		contents := "chat_model:\n  - provider: test\n    base_url: https://example.test\n" + revision + "    models:\n      - model: chat\n        context_window: 32768\n      - model: reasoner\n        context_window: 65536\n" + extra
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("", "")
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("TEST_CHAT=controlled-secret\nTEST_REASONER=controlled-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	load := func() error {
		err := manager.LoadConfig()
		manager.Close()
		return err
	}
	if err := load(); err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(chat.Storage{}, completion.NewClient(nil), manager.ChatModels, nil)
	router := api.NewRouter(&app.App{ChatServices: chat.Services{
		Model:   service,
		Run:     service,
		Session: service,
	}}, nil)
	check := func(revision string) {
		t.Helper()
		response := chatRequest(router, "GET", "/v1/model", "")
		var body struct {
			Models []map[string]any `json:"models"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Models) != 2 {
			t.Fatalf("模型列表错误: %d %s", response.Code, response.Body)
		}
		for _, model := range body.Models {
			if revision == "" {
				if _, ok := model["config_revision"]; ok {
					t.Fatal("旧配置不应伪造版本")
				}
			} else if model["config_revision"] != revision {
				t.Fatalf("未确认实际发布版本: %v", model)
			}
			if model["available"] != true || strings.Contains(response.Body.String(), "controlled-secret") {
				t.Fatal("可用性或密钥隔离错误")
			}
		}
	}
	check("")
	write("    config_revision: saved-one\n", "")
	if err := load(); err != nil {
		t.Fatal(err)
	}
	check("saved-one")
	write("    config_revision: rejected-two\n", "chat:\n  max_rounds: -1\n")
	if err := load(); err == nil {
		t.Fatal("非法配置应拒绝发布")
	}
	check("saved-one")
}
