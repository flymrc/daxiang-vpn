package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRotateIPBusyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(RotateIPResult{
			Status:            "busy",
			Egress:            "jp-android-01",
			DownSeconds:       8,
			Message:           "换 IP 正在进行中，请稍后再试",
			RetryAfterSeconds: 42,
		})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	res, err := RotateIP("ZH-OK", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "busy" {
		t.Fatalf("status = %q", res.Status)
	}
	if res.RetryAfterSeconds != 42 {
		t.Fatalf("retry_after_seconds = %d", res.RetryAfterSeconds)
	}
}

func TestRotateIPTriggeredResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/rotate-ip" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RotateIPResult{
			Status:      "triggered",
			Egress:      "jp-android-01",
			DownSeconds: 8,
		})
	}))
	defer srv.Close()
	t.Setenv("ZHVPN_API_BASE", srv.URL)

	res, err := RotateIP("ZH-OK", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "triggered" {
		t.Fatalf("status = %q", res.Status)
	}
}
