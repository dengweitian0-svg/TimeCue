package main

import (
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var peekNamedPipe = syscall.NewLazyDLL("kernel32.dll").NewProc("PeekNamedPipe")

// Windows 匿名管道采用同步读取；先查看可读字节再读取，避免 Close 与 ReadFile 竞争阻塞。
// 控制输入只有本读取者，窥视与有界读取之间不会被其他读取者消耗数据。
type controlPipeReader struct {
	file   *os.File
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func newControlReader(reader io.ReadCloser) io.ReadCloser {
	if file, ok := reader.(*os.File); ok {
		return &controlPipeReader{file: file, done: make(chan struct{})}
	}
	return reader
}

func (r *controlPipeReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return 0, os.ErrClosed
		}
		var available uint32
		ok, _, err := peekNamedPipe.Call(r.file.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
		if ok == 0 {
			r.mu.Unlock()
			if err == syscall.ERROR_BROKEN_PIPE {
				return 0, io.EOF
			}
			return 0, err
		}
		if available > 0 {
			count := len(buffer)
			if uint64(count) > uint64(available) {
				count = int(available)
			}
			n, err := r.file.Read(buffer[:count])
			r.mu.Unlock()
			return n, err
		}
		r.mu.Unlock()
		select {
		case <-r.done:
			return 0, os.ErrClosed
		case <-timer.C:
		}
	}
}

func (r *controlPipeReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	close(r.done)
	return r.file.Close()
}
