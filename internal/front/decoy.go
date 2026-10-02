package front

import (
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The decoy: everything that is not an upgrade of GET /<secret>/c is answered
// the same way, whatever the reason (wrong path, wrong secret, the right path
// without upgrade headers, a bad method, a bad version), so a scanner learns
// nothing from the answer. GET and HEAD on /, /index.html and /robots.txt get
// a tiny static page, everything else a fixed 404, both text/html. The
// headers (Date, Server, Connection: close) are the same for all of them; the
// Server name is one of a small neutral set chosen per server instance, never
// the product's. The URL is never logged.
const (
	decoyPage = "<!DOCTYPE html>\n<html>\n<head>\n<title>Welcome</title>\n</head>\n<body>\n<h1>Welcome</h1>\n<p>The site is under construction. Please check back later.</p>\n</body>\n</html>\n"
	decoy404  = "<!DOCTYPE html>\n<html>\n<head>\n<title>404 Not Found</title>\n</head>\n<body>\n<h1>Not Found</h1>\n<p>The requested URL was not found on this server.</p>\n</body>\n</html>\n"
)

// decoyServers are the Server header values an instance picks from.
var decoyServers = []string{"nginx", "Apache", "openresty", "nginx/1.24.0"}

// pickServerName chooses the Server header value of one instance.
func pickServerName() string {
	var b [1]byte
	_, _ = rand.Read(b[:])
	return decoyServers[int(b[0])%len(decoyServers)]
}

// isDecoyPage reports whether the request path (the request target without
// its query) is one of the three that get the 200 page.
func isDecoyPage(requestURI string) bool {
	p := requestURI
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	switch p {
	case "/", "/index.html", "/robots.txt":
		return true
	}
	return false
}

// decoyResponse renders the answer for a request. HEAD gets the headers of the
// GET answer without the body.
func decoyResponse(server, method, requestURI string, now time.Time) []byte {
	status, body := "404 Not Found", decoy404
	if (method == http.MethodGet || method == http.MethodHead) && isDecoyPage(requestURI) {
		status, body = "200 OK", decoyPage
	}
	var b strings.Builder
	b.WriteString("HTTP/1.1 " + status + "\r\n")
	b.WriteString("Server: " + server + "\r\n")
	b.WriteString("Date: " + now.UTC().Format(http.TimeFormat) + "\r\n")
	b.WriteString("Content-Type: text/html\r\n")
	b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	b.WriteString("Connection: close\r\n\r\n")
	if method != http.MethodHead {
		b.WriteString(body)
	}
	return []byte(b.String())
}

// writeDecoy answers the request and ends the connection the polite way: the
// answer is flushed, the write side is shut down and what the peer still
// sends is drained briefly, so an unread request body cannot turn the close
// into a reset that wipes the answer.
func (s *Server) writeDecoy(conn net.Conn, method, requestURI string) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(decoyResponse(s.serverName, method, requestURI, time.Now())); err != nil {
		return
	}
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, 16<<10))
}

type closeWriter interface{ CloseWrite() error }
