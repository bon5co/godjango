package view

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"net/http"
	"path"
	"strings"
)

const assetURLPrefix = "/static/godjango/"

//go:embed assets/*
var assetFiles embed.FS

func Assets() http.Handler {
	assets := make(map[string]embeddedAsset)
	for _, name := range []string{
		"htmx-2.0.9.min.js",
		"alpine-3.15.12.min.js",
		"godjango.js",
		"godjango.css",
	} {
		content, err := assetFiles.ReadFile("assets/" + name)
		if err != nil {
			panic("godjango view: embedded asset missing: " + name)
		}
		sum := sha256.Sum256(content)
		contentType := "text/javascript; charset=utf-8"
		if strings.HasSuffix(name, ".css") {
			contentType = "text/css; charset=utf-8"
		}
		assets[assetURLPrefix+name] = embeddedAsset{
			content:     content,
			contentType: contentType,
			etag:        fmt.Sprintf(`"%x"`, sum),
		}
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(response, request)
			return
		}
		if path.Clean(request.URL.Path) != request.URL.Path {
			http.NotFound(response, request)
			return
		}
		asset, ok := assets[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		response.Header().Set("Content-Type", asset.contentType)
		response.Header().Set("ETag", asset.etag)
		if request.Header.Get("If-None-Match") == asset.etag {
			response.WriteHeader(http.StatusNotModified)
			return
		}
		response.Header().Set("Content-Length", fmt.Sprintf("%d", len(asset.content)))
		if request.Method == http.MethodGet {
			_, _ = response.Write(asset.content)
		}
	})
}

type embeddedAsset struct {
	content     []byte
	contentType string
	etag        string
}
