package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

func TestHealthRuntimeInfo(t *testing.T) {
	info := service.NewRuntimeInfo("launch-123")
	for _, item := range []struct {
		failure error
		status  int
		code    string
	}{
		{nil, 200, ""}, {errors.New("unavailable"), 503, "database_unavailable"}, {store.ErrSchemaInvalid, 503, "schema_invalid"},
	} {
		router := NewRouter(nil, func(context.Context) error { return item.failure }, info)
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:10086/health", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		var body struct {
			Data struct {
				service.RuntimeInfo
				Status string `json:"status"`
				Error  *struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != item.status || !reflect.DeepEqual(body.Data.RuntimeInfo, info) {
			t.Fatal(response.Code, response.Body.String())
		}
		if item.code != "" && (body.Data.Error == nil || body.Data.Error.Code != item.code) {
			t.Fatal(body)
		}
		req.Host = "evil.example"
		response = httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != 403 {
			t.Fatal("健康检查绕过 HostGuard")
		}
	}
}
