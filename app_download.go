package main

// 文件说明： 内容下载：把 Flow 的请求/响应体保存到用户选择的文件（含 MIME 建议文件名）。

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"wproxyman/internal/models"
)

// mimeExt maps common MIME types to file extensions for download naming.
var mimeExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/svg+xml": ".svg",
	"image/x-icon": ".ico",
	"audio/mpeg":  ".mp3",
	"audio/wav":   ".wav",
	"audio/ogg":   ".ogg",
	"audio/aac":   ".aac",
	"audio/mp4":   ".m4a",
	"video/mp4":   ".mp4",
	"video/webm":  ".webm",
	"video/quicktime": ".mov",
	"application/pdf":  ".pdf",
	"application/zip":  ".zip",
	"application/gzip": ".gz",
	"application/json": ".json",
	"application/xml":  ".xml",
	"application/javascript": ".js",
	"application/x-www-form-urlencoded": ".txt",
	"text/html":  ".html",
	"text/css":   ".css",
	"text/plain": ".txt",
	"text/xml":   ".xml",
}

// suggestFileName builds a sensible default download filename from the flow.
func suggestFileName(f *models.Flow, side string) string {
	u, err := url.Parse(f.FullURL)
	if err != nil {
		u = &url.URL{}
	}
	base := filepath.Base(u.Path)
	if base == "" || base == "/" || base == "." {
		base = "download"
	}
	// Strip any existing extension and re-apply from MIME for consistency.
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	mime := f.ResponseMimeType
	if side == "request" {
		mime = f.RequestMimeType
	}
	if mapped, ok := mimeExt[strings.ToLower(strings.Split(mime, ";")[0])]; ok {
		return stem + mapped
	}
	if ext != "" {
		return base
	}
	return stem + ".bin"
}

// SaveFlowContent opens a save dialog and writes the request/response body of
// a flow to the chosen path. Returns the saved path, or an error.
func (a *App) SaveFlowContent(flowID, side string) (string, error) {
	a.mu.RLock()
	f := a.flowIdx[flowID]
	a.mu.RUnlock()
	if f == nil {
		return "", fmt.Errorf("flow not found")
	}

	var body []byte
	if side == "request" {
		body = f.RequestBody
	} else {
		body = f.ResponseBody
	}
	if len(body) == 0 {
		return "", fmt.Errorf("the %s body is empty", side)
	}

	name := suggestFileName(f, side)
	title := "Save response content"
	if side == "request" {
		title = "Save request content"
	}
	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: name,
		Filters: []wruntime.FileFilter{
			{DisplayName: "All Files", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("save cancelled")
	}

	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
