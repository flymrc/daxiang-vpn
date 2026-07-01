package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/dist/*
var uiFS embed.FS

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	static, err := fs.Sub(uiFS, "web/dist")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ui_unavailable", "")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/admin/")
	if path == "" {
		path = "index.html"
	}
	if _, err := static.Open(path); err != nil {
		r.URL.Path = "/index.html"
		http.FileServer(http.FS(static)).ServeHTTP(w, r)
		return
	}
	r.URL.Path = "/" + path
	http.FileServer(http.FS(static)).ServeHTTP(w, r)
}
