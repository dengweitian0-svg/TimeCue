package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequestTrackerWaitsAndRejectsNewRequests 验证退出先等待已接收请求。
func TestRequestTrackerWaitsAndRejectsNewRequests(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tracker := &requestTracker{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(started); <-release })}
	go func() {
		tracker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(finished)
	}()
	<-started
	tracker.stop()
	response := httptest.NewRecorder()
	tracker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("关闭入口后仍接收请求")
	}
	joined := make(chan struct{})
	go func() { tracker.active.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("活跃请求未结束便完成等待")
	default:
	}
	close(release)
	<-joined
	<-finished
}
