// Flow 构造与转换：在 http.Request / http.Response 与内部 models.Flow
// 之间互转，以及提取头部、Cookie、MIME 等展示信息。
package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"

	"wproxyman/internal/models"
)

// flowFromRequest builds the request half of a flow from an incoming
// http.Request. scheme is "http" or "https" depending on transport.
func flowFromRequest(r *http.Request, scheme string) *models.Flow {
	f := models.NewFlow()
	f.Scheme = scheme
	f.Method = r.Method
	f.HTTPVersion = httpVersion(r.Proto)
	f.TLS = scheme == "https"

	// Normalize URL.
	u := *r.URL
	if u.Scheme == "" {
		u.Scheme = scheme
	}
	if u.Host == "" {
		u.Host = r.Host
	}
	f.FullURL = u.String()
	f.Host = u.Host
	f.Path = u.Path
	if f.Path == "" {
		f.Path = "/"
	}
	f.Query = u.RawQuery

	f.RequestHeaders = headerFromTextproto(r.Header)
	f.RequestMimeType = sniffMime(r.Header.Get("Content-Type"), r.Header.Get("Accept"))
	f.RequestCookies = cookiesFromPairs(r.Cookies())
	return f
}

// fillRequestBody reads the request body (bounded by maxBytes).
// contentLength is the advertised Content-Length (-1 if unknown).
func fillRequestBody(f *models.Flow, body io.ReadCloser, contentLength, maxBytes int64) {
	if body == nil {
		if contentLength > 0 {
			f.RequestSize = contentLength
		}
		return
	}
	buf, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if int64(len(buf)) > maxBytes {
		buf = buf[:maxBytes]
		f.RequestTruncated = true
	}
	if err == nil || len(buf) > 0 {
		f.RequestBody = buf
	}
	if contentLength >= 0 {
		f.RequestSize = contentLength
	} else {
		f.RequestSize = int64(len(buf))
	}
}

// applyResponse populates the response half of the flow.
func applyResponse(f *models.Flow, resp *http.Response, maxBytes int64) {
	f.ResponseStatus = resp.StatusCode
	f.ResponseReason = resp.Status
	if i := strings.Index(f.ResponseReason, " "); i >= 0 {
		f.ResponseReason = f.ResponseReason[i+1:]
	}
	f.ResponseHeaders = headerFromTextproto(resp.Header)
	f.ResponseMimeType = sniffMime(resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	f.ResponseCookies = cookiesFromPairs(resp.Cookies())

	if resp.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if int64(len(body)) > maxBytes {
			body = body[:maxBytes]
			f.ResponseTruncated = true
		}
		f.ResponseBody = body
	}
	if resp.ContentLength >= 0 {
		f.ResponseSize = resp.ContentLength
	} else {
		f.ResponseSize = int64(len(f.ResponseBody))
	}
}

func httpVersion(proto string) string {
	switch proto {
	case "HTTP/1.0":
		return "HTTP/1.0"
	case "HTTP/2.0":
		return "HTTP/2.0"
	default:
		return "HTTP/1.1"
	}
}

// cookiesFromPairs converts net/http cookies to our model.
func cookiesFromPairs(cs []*http.Cookie) []models.Cookie {
	out := make([]models.Cookie, 0, len(cs))
	for _, c := range cs {
		out = append(out, models.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
			Secure:   c.Secure,
			HTTPOnly: c.HttpOnly,
			SameSite: sameSiteString(c.SameSite),
		})
	}
	return out
}

func sameSiteString(s http.SameSite) string {
	switch s {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return ""
	}
}

// sniffMime extracts the media type from a Content-Type header, falling back
// to a best guess.
func sniffMime(contentType, fallback string) string {
	if contentType == "" {
		return strings.Split(fallback, ";")[0]
	}
	return strings.Split(contentType, ";")[0]
}

// requestToOutgoing converts a models.Flow into a fresh http.Request for
// upstream forwarding.
func requestToOutgoing(f *models.Flow) (*http.Request, error) {
	target := f.FullURL
	if target == "" {
		target = f.Scheme + "://" + f.Host + f.Path
		if f.Query != "" {
			target += "?" + f.Query
		}
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	req := &http.Request{
		Method: f.Method,
		URL:    u,
		Host:   u.Host,
		Header: make(http.Header),
		Proto:  "HTTP/1.1",
	}
	for _, h := range f.RequestHeaders {
		// Hop-by-hop headers are handled by Go's transport automatically;
		// forward everything else.
		req.Header.Add(h.Name, h.Value)
	}
	if len(f.RequestBody) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(f.RequestBody))
		req.ContentLength = int64(len(f.RequestBody))
	}
	return req, nil
}
