package server

import (
	"bytes"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const webContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func (server *Server) serveWeb(w http.ResponseWriter, request *http.Request) {
	requestPath := request.URL.Path
	cleanPath := path.Clean(requestPath)
	if requestPath == "" || (cleanPath != requestPath && requestPath != cleanPath+"/") {
		http.NotFound(w, request)
		return
	}

	name := strings.TrimPrefix(cleanPath, "/")
	if name == "api" || strings.HasPrefix(name, "api/") || name == "healthz" || strings.HasPrefix(name, "healthz/") {
		http.NotFound(w, request)
		return
	}
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) {
		http.NotFound(w, request)
		return
	}

	assetRequest := name == "assets" || strings.HasPrefix(name, "assets/")
	contents, err := fs.ReadFile(server.webAssets, name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			server.internalError(w, request, err)
			return
		}
		if assetRequest {
			http.NotFound(w, request)
			return
		}
		contents, err = fs.ReadFile(server.webAssets, "index.html")
		name = "index.html"
	}
	if err != nil {
		server.internalError(w, request, err)
		return
	}

	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", webContentSecurityPolicy)
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else if assetRequest {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	http.ServeContent(w, request, path.Base(name), time.Time{}, bytes.NewReader(contents))
}
