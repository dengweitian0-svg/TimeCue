package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// processFixture 只在测试新目录中手动应用已有迁移，不操作用户数据库。
func processFixture(t *testing.T, root, kind string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "中文 空格")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if kind != "unmigrated" {
		db, err := sql.Open("sqlite", filepath.Join(dir, "fixture.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, name := range []string{"001_initial.up.sql", "002_schedule_changes.up.sql", "003_ai_chat.up.sql"} {
			if kind == "old" && name == "003_ai_chat.up.sql" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, "migrations", name))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "old" {
				data = bytes.ReplaceAll(data, []byte("'loaded', "), nil)
			}
			if _, err := db.Exec(string(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := "db:\n  db_path: ./fixture.db\nlog:\n  log_path: ./logs\n"
	if kind == "bad-config" {
		cfg = "log: [invalid\n"
	}
	if kind == "bad-log" {
		if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("file"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

type testProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
	stderr bytes.Buffer
	stdout bytes.Buffer
	lines  chan []byte
	done   chan error
}

func launchProcess(t *testing.T, binary string, args ...string) *testProcess {
	t.Helper()
	args = append([]string{"--port", "1145"}, args...)
	p := &testProcess{cmd: exec.Command(binary, args...), done: make(chan error, 1), lines: make(chan []byte, 1)}
	p.cmd.Dir = t.TempDir()
	var err error
	p.input, err = p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.output, err = p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stderr = &p.stderr
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		reader := bufio.NewReader(p.output)
		first := true
		for {
			line, err := reader.ReadBytes('\n')
			p.stdout.Write(line)
			if first {
				p.lines <- line
				first = false
			}
			if err != nil {
				break
			}
		}
		p.done <- p.cmd.Wait()
	}()
	t.Cleanup(func() { _ = p.input.Close(); _ = p.cmd.Process.Kill() })
	return p
}

func (p *testProcess) wait(t *testing.T, success bool) {
	t.Helper()
	select {
	case err := <-p.done:
		if (err == nil) != success {
			t.Fatalf("exit: %v; stderr: %s", err, p.stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Fatal("托管进程未在测试预算内退出")
	}
}

func (p *testProcess) ready(t *testing.T) app.RuntimeInfo {
	t.Helper()
	select {
	case b := <-p.lines:
		var ready struct {
			Event, Address string
			app.RuntimeInfo
		}
		if err := json.Unmarshal(b, &ready); err != nil {
			t.Fatal("stdout 混入非协议文本", string(b), err)
		}
		if ready.Event != "ready" || ready.Address != "http://127.0.0.1:1145" || ready.PID != p.cmd.Process.Pid {
			t.Fatal(ready)
		}
		return ready.RuntimeInfo
	case <-time.After(10 * time.Second):
		t.Fatal("没有收到 ready")
	}
	return app.RuntimeInfo{}
}

// TestManagedProcess 使用编译后的自有进程和匿名管道串行验证固定端口协议。
func TestManagedProcess(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	packageDir := filepath.Join(t.TempDir(), "打包 空格")
	if err := os.Mkdir(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(packageDir, "timecue-test.exe")
	build := exec.Command("go", "build", "-o", binary, "./cmd/flutter")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:1145")
	if err != nil {
		// 已占用时仍验证拒绝启动，不停止占用者。
		p := launchProcess(t, binary, "--data-dir", processFixture(t, root, "valid"), "--instance-id", "occupied")
		p.wait(t, false)
		if !strings.Contains(p.stderr.String(), "127.0.0.1:1145") {
			t.Fatal(p.stderr.String())
		}
		t.Skip("端口已占用；正常握手集成需空闲固定端口，占用拒绝已验证")
	}
	_ = probe.Close()
	directory := processFixture(t, root, "valid")
	for _, reason := range []string{"shutdown", "eof", "invalid", "oversized", "restart"} {
		t.Run(reason, func(t *testing.T) {
			id := "launch-" + reason
			p := launchProcess(t, binary, "--data-dir", directory, "--instance-id", id)
			info := p.ready(t)
			if info.InstanceID != id {
				t.Fatal(info)
			}
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Get("http://127.0.0.1:1145/health")
			if err != nil {
				t.Fatal(err)
			}
			var health struct {
				Data struct {
					app.RuntimeInfo
					Status string
				}
			}
			err = json.NewDecoder(resp.Body).Decode(&health)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || health.Data.Status != "ok" || !reflect.DeepEqual(health.Data.RuntimeInfo, info) {
				t.Fatal(health, err)
			}
			success := true
			switch reason {
			case "eof":
				_ = p.input.Close()
			case "invalid":
				_, _ = io.WriteString(p.input, "{\"command\":\"restart\"}\n")
				success = false
			case "oversized":
				_, _ = io.WriteString(p.input, strings.Repeat("x", maxControlLine+1)+"\n")
				success = false
			default:
				_, _ = io.WriteString(p.input, "{\"command\":\"shutdown\"}\n")
			}
			p.wait(t, success)
			if bytes.Count(p.stdout.Bytes(), []byte("\n")) != 1 {
				t.Fatal("托管 stdout 不止一条 ready", p.stdout.String())
			}
		})
	}
	for _, kind := range []string{"unmigrated", "old"} {
		t.Run(kind, func(t *testing.T) {
			p := launchProcess(t, binary, "--data-dir", processFixture(t, root, kind), "--instance-id", "initialized")
			p.ready(t)
			_, _ = io.WriteString(p.input, "{\"command\":\"shutdown\"}\n")
			p.wait(t, true)
		})
	}
	for _, kind := range []string{"bad-config", "bad-log", "occupied", "early-eof"} {
		t.Run(kind, func(t *testing.T) {
			fixtureKind := kind
			if kind == "occupied" || kind == "early-eof" {
				fixtureKind = "valid"
			}
			if kind == "occupied" {
				listener, err := net.Listen("tcp", "127.0.0.1:1145")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			}
			p := launchProcess(t, binary, "--data-dir", processFixture(t, root, fixtureKind), "--instance-id", "failure")
			if kind == "early-eof" {
				_ = p.input.Close()
			}
			p.wait(t, kind == "early-eof")
			if p.stdout.Len() != 0 {
				t.Fatal("启动失败或提前取消输出 ready", p.stdout.String())
			}
			if kind != "early-eof" && p.stderr.Len() == 0 {
				t.Fatal("启动失败没有 stderr 诊断")
			}
		})
	}
	// 可执行文件旁只放配置和已迁移数据库；默认路径必须独立于启动工作目录。
	if err := os.Mkdir(filepath.Join(packageDir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	databaseContents, err := os.ReadFile(filepath.Join(directory, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "db", "timecue.db"), databaseContents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "config.yaml"), []byte("log:\n  level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 手动模式省略身份，stdin EOF 不取消本次运行；每次启动都生成新身份。
	previous := ""
	for i := 0; i < 2; i++ {
		var p *testProcess
		if i == 0 {
			p = launchProcess(t, binary)
		} else {
			p = launchProcess(t, binary, "--data-dir", directory)
		}
		_ = p.input.Close()
		client := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(5 * time.Second)
		var info app.RuntimeInfo
		for {
			resp, err := client.Get("http://127.0.0.1:1145/health")
			if err == nil {
				var body struct{ Data app.RuntimeInfo }
				_ = json.NewDecoder(resp.Body).Decode(&body)
				_ = resp.Body.Close()
				info = body.Data
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("手动模式被 stdin EOF 停止", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if info.InstanceID == "" || info.InstanceID == previous || info.PID != p.cmd.Process.Pid {
			t.Fatal("手动身份没有更新", info)
		}
		previous = info.InstanceID
		if i == 0 {
			if _, err := os.Stat(filepath.Join(packageDir, "log", "timecue.jsonl")); err != nil {
				t.Fatal("省略目录时未在可执行文件旁写入日志", err)
			}
			for _, name := range []string{"config.yaml", "db", "log"} {
				if _, err := os.Stat(filepath.Join(p.cmd.Dir, name)); !os.IsNotExist(err) {
					t.Fatal("省略目录时访问了启动工作目录", name, err)
				}
			}
		}
		_ = p.cmd.Process.Kill()
		p.wait(t, false)
	}
	if _, err := os.Stat(filepath.Join(directory, "logs", "timecue.jsonl")); err != nil {
		t.Fatal(fmt.Errorf("数据目录日志缺失: %w", err))
	}
	t.Run("old-attempt-after-restart", func(t *testing.T) {
		dir := processFixture(t, root, "valid")
		path := filepath.Join(dir, "fixture.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		planned := time.Now().Truncate(time.Minute).Add(-2 * time.Minute).UnixNano()
		created := planned - int64(time.Hour)
		if _, err := db.Exec(`INSERT INTO schedules (id,name,type,single_notify_time,created_at,updated_at,revision) VALUES (42,'测试历史','single',?,?,?,1)`, planned, created, created); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schedule_versions (schedule_id,revision,effective_from,name,type,single_notify_time,cancelled,deleted) VALUES (42,1,?,'测试历史','single',?,0,0)`, created, planned); err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		p := launchProcess(t, binary, "--data-dir", dir, "--instance-id", "old-run")
		p.ready(t)
		deadline := time.Now().Add(4 * time.Second)
		var claim notification.DeliveryClaim
		for {
			resp, err := client.Post("http://127.0.0.1:1145/api/v1/notifications/claim", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			var body struct{ Data []notification.DeliveryClaim }
			err = json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode, err)
			}
			if len(body.Data) > 0 {
				claim = body.Data[0]
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("历史任务没有开放领取")
			}
			time.Sleep(5 * time.Millisecond)
		}
		start := "http://127.0.0.1:1145/api/v1/notifications/delivery-attempts/" + claim.AttemptID + "/start"
		resp, err := client.Post(start, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
		_ = p.input.Close()
		p.wait(t, true)
		var status string
		if err := db.QueryRow(`SELECT status FROM notifications WHERE schedule_id=42`).Scan(&status); err != nil || status != "loaded" {
			t.Fatal("退出伪造了未知投递结果", status, err)
		}
		p = launchProcess(t, binary, "--data-dir", dir, "--instance-id", "new-run")
		info := p.ready(t)
		if info.InstanceID != "new-run" {
			t.Fatal(info)
		}
		resp, err = client.Post(start, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("旧运行 attempt 仍可开始", resp.StatusCode)
		}
		resp, err = client.Get("http://127.0.0.1:1145/api/v1/notifications?deliveryTaskId=" + claim.DeliveryTaskID)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("旧任务身份仍有效", resp.StatusCode)
		}
		_, _ = io.WriteString(p.input, "{\"command\":\"shutdown\"}\n")
		p.wait(t, true)
	})
}
