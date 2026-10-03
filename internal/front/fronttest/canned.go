package fronttest

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Canned is a response the edge answers with instead of contacting the origin
// (Options.Canned), shaped like the answers a misconfigured Cloudflare zone
// gives. Header is added to the standard edge headers.
type Canned struct {
	Status int
	Header http.Header
	Body   string
}

// Redirect is a redirect rule or "Always Use HTTPS" (status 301, 302, 307 or 308).
func Redirect(status int, location string) *Canned {
	if status == 0 {
		status = http.StatusMovedPermanently
	}
	h := http.Header{}
	h.Set("Location", location)
	return &Canned{Status: status, Header: h, Body: "<html><head><title>Redirect</title></head><body>Moved</body></html>"}
}

// Challenge is a managed challenge (403 with cf-mitigated).
func Challenge() *Canned {
	h := http.Header{}
	h.Set("cf-mitigated", "challenge")
	return &Canned{Status: http.StatusForbidden, Header: h, Body: "<!DOCTYPE html><html><head><title>Just a moment...</title></head><body>Enable JavaScript and cookies to continue</body></html>"}
}

// Blocked is a WAF or browser-integrity block: 403 with an "Error 1010" page
// and no cf-mitigated header.
func Blocked(code int) *Canned {
	if code == 0 {
		code = 1010
	}
	return &Canned{Status: http.StatusForbidden, Body: errorPage("Access denied", code)}
}

// RateLimited is a rate limit answer (429 with Retry-After, in seconds).
func RateLimited(retryAfter string) *Canned {
	h := http.Header{}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	return &Canned{Status: http.StatusTooManyRequests, Header: h, Body: errorPage("Too many requests", 1015)}
}

// OriginError is a Cloudflare 5xx origin error page (520-527).
func OriginError(status int) *Canned {
	return &Canned{Status: status, Body: errorPage("Web server is returning an unknown error", status)}
}

// CloudflareError is a 530 with an "Error 1xxx" page (for example 1016, origin
// DNS error, or 1033).
func CloudflareError(code int) *Canned {
	return &Canned{Status: 530, Body: errorPage("Cloudflare error", code)}
}

// NotFound is a plain 404 (a wrong path or a front that is off).
func NotFound() *Canned {
	return &Canned{Status: http.StatusNotFound, Body: "<html><head><title>404 Not Found</title></head><body><h1>Not Found</h1></body></html>"}
}

// Status answers a bare status with a tiny body.
func Status(code int) *Canned {
	return &Canned{Status: code, Body: "<html><body>" + strconv.Itoa(code) + "</body></html>"}
}

func errorPage(title string, code int) string {
	return fmt.Sprintf("<!DOCTYPE html>\n<html><head><title>%s | Error %d</title></head>\n<body><h1>Error %d</h1><p>%s</p><p>Cloudflare Ray ID: 0123456789abcdef</p></body></html>\n", title, code, code, title)
}

// bytes renders the response with the standard edge headers.
func (c *Canned) bytes(ray string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", c.Status, statusText(c.Status))
	fmt.Fprintf(&b, "Server: cloudflare\r\nCF-RAY: %s\r\nContent-Type: text/html; charset=UTF-8\r\n", ray)
	for k, vs := range c.Header {
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\nConnection: close\r\n\r\n%s", len(c.Body), c.Body)
	return []byte(b.String())
}

func statusText(code int) string {
	if t := http.StatusText(code); t != "" {
		return t
	}
	switch code {
	case 520:
		return "Web Server Returned an Unknown Error"
	case 521:
		return "Web Server Is Down"
	case 522:
		return "Connection Timed Out"
	case 523:
		return "Origin Is Unreachable"
	case 524:
		return "A Timeout Occurred"
	case 525:
		return "SSL Handshake Failed"
	case 526:
		return "Invalid SSL Certificate"
	case 527:
		return "Railgun Error"
	case 530:
		return "Origin DNS Error"
	}
	return "Status"
}
