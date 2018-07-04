package httputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHSTS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	respRecorder := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "")
	})
	handlerWithMiddlewareHSTS := HSTS(handler)
	handlerWithMiddlewareHSTS.ServeHTTP(respRecorder, req)
	want := "max-age=31536000; includeSubDomains; preload"
	got := respRecorder.Header().Get("Strict-Transport-Security")
	if got != want {
		t.Error("middlewareHSTS do not add HSTS header")
	}
}
