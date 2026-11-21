package http

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/yolo"
	"github.com/cu3rd/yc-hack-horses/backend/internal/modelassets"
	"github.com/go-chi/chi/v5"
)

func modelStatus(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		files := []modelassets.Status{}
		if d.Models != nil {
			files = d.Models.Status()
		}
		runtime, err := os.Stat(d.RuntimePath)
		runtimeReady := yolo.NativeRuntime && err == nil && runtime.Mode().IsRegular()
		ready := d.ModelMode == "external"
		if d.ModelMode == "local" && d.Models != nil {
			ready = runtimeReady
			for _, item := range files {
				ready = ready && item.State == "ready"
			}
		}
		writeJSON(w, 200, map[string]any{"mode": d.ModelMode, "ready": ready, "runtime_ready": runtimeReady, "models": files})
	}
}
func changeModel(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if d.Models == nil || !(host == "localhost" || (ip != nil && ip.IsLoopback())) || !localOrigin(r.Header.Get("Origin")) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeError(w, 403, "Установка доступна на локальной ноде.")
			return
		}
		id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
		switch action {
		case "download":
			var input struct {
				URL string `json:"url"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
				writeError(w, 400, "Некорректная ссылка.")
				return
			}
			if err := d.Models.Start(id, input.URL); err != nil {
				writeError(w, 422, err.Error())
				return
			}
		case "cancel":
			d.Models.Cancel(id)
		default:
			notFoundJSON(w, r)
			return
		}
		modelStatus(d)(w, r)
	}
}

func localOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
