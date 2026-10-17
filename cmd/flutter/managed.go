package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dengweitian0-svg/TimeCue/internal/app"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
)

const maxControlLine = 1024

type startupOptions struct {
	dataDir, instanceID string
	managed             bool
}

// parseStartup 在创建任何配置、日志或数据库资源前校验启动契约。
func parseStartup(args []string) (startupOptions, error) {
	flags := flag.NewFlagSet("timecue", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", "", "配置和运行文件目录，默认使用可执行文件目录")
	instanceID := flags.String("instance-id", "", "父进程为本次启动提供的身份")
	if err := flags.Parse(args); err != nil {
		return startupOptions{}, err
	}
	if flags.NArg() != 0 {
		return startupOptions{}, fmt.Errorf("unexpected positional arguments")
	}
	var hasDir, managed bool
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "data-dir" {
			hasDir = true
		}
		if f.Name == "instance-id" {
			managed = true
		}
	})
	if hasDir && strings.TrimSpace(*dataDir) == "" {
		return startupOptions{}, fmt.Errorf("data-dir must not be empty")
	}
	if managed {
		if !hasDir {
			return startupOptions{}, fmt.Errorf("managed startup requires data-dir")
		}
		if len(*instanceID) == 0 || len(*instanceID) > 128 {
			return startupOptions{}, fmt.Errorf("instance-id must contain 1–128 safe ASCII characters")
		}
		for _, c := range *instanceID {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return startupOptions{}, fmt.Errorf("instance-id allows only ASCII letters, digits, hyphen and underscore")
			}
		}
	} else {
		*instanceID = rand.Text()
	}
	base := *dataDir
	if !hasDir {
		executable, err := os.Executable()
		if err != nil {
			return startupOptions{}, fmt.Errorf("resolve executable path: %w", err)
		}
		base = filepath.Dir(executable)
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return startupOptions{}, fmt.Errorf("resolve data-dir: %w", err)
	}
	return startupOptions{dataDir: abs, instanceID: *instanceID, managed: managed}, nil
}

func writeReady(output io.Writer, info app.RuntimeInfo) error {
	return json.NewEncoder(output).Encode(struct {
		Event string `json:"event"`
		app.RuntimeInfo
		Address string `json:"address"`
	}{Event: "ready", RuntimeInfo: info, Address: "http://127.0.0.1:10086"})
}

// parentControl 只拥有管道读取工作；结果在 done 关闭后读取，停止请求始终取消原 context。
type parentControl struct {
	reader    io.ReadCloser
	done      chan struct{}
	closeOnce sync.Once
	failure   error
	closing   atomic.Bool
}

func startParentControl(reader io.ReadCloser, cancel context.CancelFunc) *parentControl {
	reader = newControlReader(reader)
	p := &parentControl{reader: reader, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		reason, failure := readParentCommand(reader)
		p.failure = failure
		// 只忽略由本进程关闭读端引起的错误，父端故障仍保留非零退出语义。
		if p.closing.Load() && (errors.Is(p.failure, io.ErrClosedPipe) || errors.Is(p.failure, os.ErrClosed)) {
			p.failure = nil
			return
		}
		logger.Info("[main] parent control requested stop", "reason", reason, "error", p.failure)
		cancel()
	}()
	return p
}

func (p *parentControl) closeAndWait() {
	p.closeOnce.Do(func() { p.closing.Store(true); _ = p.reader.Close() })
	<-p.done
}

func (p *parentControl) err() error {
	<-p.done
	return p.failure
}

// readShutdown 只接受完整且有界的一行命令，空管道 EOF 是正常停止。
func readShutdown(reader io.Reader) error {
	_, err := readParentCommand(reader)
	return err
}

func readParentCommand(reader io.Reader) (string, error) {
	input := bufio.NewReaderSize(reader, maxControlLine+1)
	line, err := input.ReadSlice('\n')
	if err == io.EOF && len(line) == 0 {
		return "parent-eof", nil
	}
	if err != nil {
		return "control-read-error", fmt.Errorf("parent control read: %w", err)
	}
	if len(line) > maxControlLine {
		return "protocol-error", fmt.Errorf("parent control line exceeds %d bytes", maxControlLine)
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var message struct {
		Command string `json:"command"`
	}
	if err := decoder.Decode(&message); err != nil {
		return "protocol-error", fmt.Errorf("invalid parent control JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "protocol-error", fmt.Errorf("parent control expects one JSON command")
	}
	if message.Command != "shutdown" {
		return "protocol-error", fmt.Errorf("unknown parent control command %q", message.Command)
	}
	return "shutdown", nil
}
