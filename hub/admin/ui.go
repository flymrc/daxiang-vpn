package admin

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
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
		serveUIFile(w, r, static, "index.html")
		return
	}
	if info, err := fs.Stat(static, path); err != nil || info.IsDir() {
		serveUIFile(w, r, static, "index.html")
		return
	}
	r.URL.Path = "/" + path
	http.FileServer(http.FS(static)).ServeHTTP(w, r)
}

func serveUIFile(w http.ResponseWriter, r *http.Request, static fs.FS, name string) {
	data, err := fs.ReadFile(static, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ui_unavailable", "")
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
