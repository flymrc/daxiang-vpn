package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminUnknownRequestFieldsRemainCompatible(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/admin/api/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password","future_field":true}`))
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.20")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login with unknown field = %d", w.Code)
	}
	var cookie *http.Cookie
	for _, candidate := range w.Result().Cookies() {
		if candidate.Name == "zhhub_admin_session" {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("compatible login did not establish a session")
	}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || me.CSRF == "" {
		t.Fatalf("compatible login response: %v", err)
	}
	// Exercise the existing rotate consumer with the same session's CSRF value.
	r = httptest.NewRequest(http.MethodPost, "/admin/api/egress/jp-android-01/rotate-ip", strings.NewReader(`{"down_seconds":8,"future_field":true}`))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", me.CSRF)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate with unknown field = %d", w.Code)
	}
}
