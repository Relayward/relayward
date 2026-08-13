package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const webStyleNoncePlaceholder = "__RELAYWARD_STYLE_NONCE__"

// Radix Select injects a fixed viewport rule through a style element. Chromium
// also validates the initially empty element, so both pinned contents are listed.
const webRadixSelectStyleHashes = "'sha256-441zG27rExd4/il+NvIqyL8zFx5XmyNQtE381kSkUJk=' 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='"

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
	styleNonce := ""
	if name == "index.html" {
		styleNonce, err = newStyleNonce()
		if err != nil {
			server.internalError(w, request, err)
			return
		}
		placeholder := []byte(webStyleNoncePlaceholder)
		if bytes.Count(contents, placeholder) != 1 {
			server.internalError(w, request, errors.New("web index must contain exactly one style nonce placeholder"))
			return
		}
		contents = bytes.Replace(contents, placeholder, []byte(styleNonce), 1)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", webContentSecurityPolicy(styleNonce))
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

func newStyleNonce() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate web style nonce: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(value), nil
}

func webContentSecurityPolicy(styleNonce string) string {
	styleSource := "style-src 'self'"
	if styleNonce != "" {
		styleSource += " 'nonce-" + styleNonce + "'"
	}
	styleSource += " " + webRadixSelectStyleHashes
	return "default-src 'none'; script-src 'self'; " + styleSource + "; style-src-attr 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}
