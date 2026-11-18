package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

func (h *host) serveHTTP(ctx context.Context, stop context.CancelCauseFunc, listener net.Listener, handler http.Handler) (result error) {
	requests := &requestTracker{handler: handler}
	server := &http.Server{
		Handler:           requests,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serving := &readyListener{
		Listener: listener,
		started:  make(chan struct{}),
	}
	serveDone := make(chan struct{})
	var serveErr error
	go func() {
		defer close(serveDone)
		serveErr = server.Serve(serving)
	}()
	defer func() {
		requests.stop()
		shutdown, cancel := context.WithTimeout(context.Background(), h.httpShutdown)
		defer cancel()
		stop(result)
		if err := server.Shutdown(shutdown); err != nil {
			h.log.Error("[host] HTTP shutdown exceeded graceful window", "error", err)
			_ = server.Close()
		}
		<-serveDone
		requests.active.Wait()
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-serveDone:
		return serveError(serveErr)
	case <-serving.started:
	}
	select {
	case <-serveDone:
		return serveError(serveErr)
	default:
	}
	if ctx.Err() != nil {
		return nil
	}
	if h.onReady != nil {
		if err := h.onReady(); err != nil {
			return fmt.Errorf("announce ready: %w", err)
		}
	}
	h.log.Info("[host] HTTP server listening", "address", listener.Addr().String())
	select {
	case <-ctx.Done():
		return nil
	case <-serveDone:
		return serveError(serveErr)
	}
}

func serveError(err error) error {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

// readyListener 在 Serve 进入接收循环时通知宿主。
type readyListener struct {
	net.Listener
	started chan struct{}
	once    sync.Once
}

// Accept 在首次等待连接时通知宿主。
func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.started) })
	return l.Listener.Accept()
}

// requestTracker 关闭请求入口后才允许等待，避免退出与新请求交错。
type requestTracker struct {
	handler http.Handler
	mu      sync.Mutex
	closing bool
	active  sync.WaitGroup
}

// ServeHTTP 跟踪在途请求，并在停止接入后拒绝新请求。
func (t *requestTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		http.Error(w, "service stopping", http.StatusServiceUnavailable)
		return
	}
	t.active.Add(1)
	t.mu.Unlock()
	defer t.active.Done()
	t.handler.ServeHTTP(w, r)
}

func (t *requestTracker) stop() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
}
