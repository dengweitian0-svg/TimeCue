package main

import (
	"context"
	"errors"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartupParameters(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"--instance-id", "valid"}, {"--instance-id=", "--data-dir", "."},
		{"--data-dir="}, {"--data-dir", ".", "--instance-id", "a b"},
		{"--data-dir", ".", "--instance-id", strings.Repeat("a", 129)},
		{"--data-dir", ".", "--instance-id", "中文"}, {"unexpected"}, {"--unknown"},
		{"--port", "0"}, {"--port", "65536"}, {"--port", "-1"}, {"--port", "abc"},
	} {
		if _, err := parseStartup(args); err == nil {
			t.Fatal("非法参数被接受", args)
		}
	}
	one, err := parseStartup(nil)
	if err != nil {
		t.Fatal(err)
	}
	two, _ := parseStartup(nil)
	if one.managed || one.instanceID == "" || one.instanceID == two.instanceID || one.port != 10086 {
		t.Fatal("手动运行身份未更新")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if one.dataDir != filepath.Dir(executable) {
		t.Fatal("省略目录时未使用可执行文件目录", one.dataDir)
	}
	explicit, err := parseStartup([]string{"--data-dir", "资料 空格"})
	if err != nil {
		t.Fatal(err)
	}
	workingDir, err := os.Getwd()
	if err != nil || explicit.dataDir != filepath.Join(workingDir, "资料 空格") {
		t.Fatal("显式相对目录未按启动工作目录解析", explicit.dataDir, err)
	}
	managed, err := parseStartup([]string{"--data-dir", ".", "--instance-id", "launch_A-123"})
	if err != nil || !managed.managed || managed.instanceID != "launch_A-123" {
		t.Fatal(managed, err)
	}
	customPort, err := parseStartup([]string{"--port", "1145"})
	if err != nil || customPort.port != 1145 {
		t.Fatal(customPort, err)
	}
}

func TestControlProtocol(t *testing.T) {
	for _, item := range []struct {
		input string
		valid bool
	}{
		{"", true}, {"{\"command\":\"shutdown\"}\n", true},
		{"{\"command\":\"shutdown\"}\r\n", true},
		{"{\"command\":\"shutdown\"}", false}, {"{invalid}\n", false},
		{"{\"command\":\"restart\"}\n", false}, {"{}\n", false},
		{"{\"command\":\"shutdown\",\"extra\":1}\n", false},
		{"{\"command\":\"shutdown\"} {}\n", false},
		{strings.Repeat("x", maxControlLine+1) + "\n", false},
	} {
		if err := readShutdown(strings.NewReader(item.input)); (err == nil) != item.valid {
			t.Fatal(item, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	control := startParentControl(reader, cancel)
	control.closeAndWait()
	control.closeAndWait()
	if control.err() != nil {
		t.Fatal("关闭未解除管道等待", control.err())
	}
	reader, writer = io.Pipe()
	ctx, cancel = context.WithCancel(context.Background())
	control = startParentControl(reader, cancel)
	failure := errors.New("pipe failed")
	_ = writer.CloseWithError(failure)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("读失败未取消运行")
	}
	control.closeAndWait()
	if !errors.Is(control.err(), failure) {
		t.Fatal("读取故障丢失", control.err())
	}
}

// TestOSPipeReaderCanBeJoined 验证取消后关闭真实匿名管道读端可解除等待。
func TestOSPipeReaderCanBeJoined(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	control := startParentControl(reader, cancel)
	cancel()
	joined := make(chan struct{})
	go func() { control.closeAndWait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("信号取消后真实管道读取仍阻塞")
	}
	if ctx.Err() == nil || control.err() != nil {
		t.Fatal(control.err())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReadyWriteFailure(t *testing.T) {
	if err := writeReady(failingWriter{}, app.NewRuntimeInfo("launch"), 1145); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}
