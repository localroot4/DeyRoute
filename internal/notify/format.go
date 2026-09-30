package notify

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// maxField bounds every variable part of a message (Telegram allows 4096
// characters; a notification is meant to be short).
const maxField = 300

// Format renders the message for e without a hub name, e.g.
//
//	DEYROUTE · main · switch_transport
//	backhaul/tcpmux -> backhaul/wssmux
//	reason: probe failed 3x (timeout)
//	2026-09-29 12:41:03 UTC
//
// Secrets are masked (log.Redact).
func Format(e state.Event) string { return format("", e, 0) }

// FormatWithHub is Format with the hub name in the first line
// ("DEYROUTE ir-1 · main · switch_transport").
func FormatWithHub(hub string, e state.Event) string { return format(hub, e, 0) }

// format builds the message. suppressed > 0 appends "(+N suppressed)" to the
// first line.
func format(hub string, e state.Event, suppressed int) string {
	var lines []string

	head := "DEYROUTE"
	if h := clean(hub); h != "" {
		head += " " + h
	}
	tunnel, node := clean(e.Tunnel), clean(e.Node)
	subject := tunnel
	if subject == "" {
		subject = node
	}
	if subject != "" {
		head += " · " + subject
	}
	if typ := clean(e.Type); typ != "" {
		head += " · " + typ
	}
	if suppressed > 0 {
		head += " (+" + strconv.Itoa(suppressed) + " suppressed)"
	}
	lines = append(lines, head)

	transition := transitionLine(e)
	if transition != "" {
		lines = append(lines, transition)
	} else if tunnel != "" && node != "" {
		lines = append(lines, "node: "+node)
	}
	reason := clean(e.Reason)
	if msg := clean(e.Message); msg != "" && transition == "" && reason == "" {
		lines = append(lines, msg)
	}
	if reason != "" {
		lines = append(lines, "reason: "+reason)
	}
	if code := clean(e.Code); code != "" {
		lines = append(lines, "code: "+code)
	}
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	lines = append(lines, at.UTC().Format("2006-01-02 15:04:05")+" UTC")
	return log.Redact(strings.Join(lines, "\n"))
}

// transitionLine renders "from -> to" (or the single side that is set).
// Nodes are shown only when the node changed (switch_node).
func transitionLine(e state.Event) string {
	fromNode, toNode := clean(e.FromNode), clean(e.ToNode)
	showNodes := fromNode != toNode
	from := candidate(showNodes, fromNode, clean(e.FromTransport))
	to := candidate(showNodes, toNode, clean(e.ToTransport))
	switch {
	case from != "" && to != "":
		return from + " -> " + to
	case to != "":
		return to
	}
	return from
}

// candidate joins node and transport ("de-1 backhaul/wssmux").
func candidate(withNode bool, node, transport string) string {
	parts := make([]string, 0, 2)
	if withNode && node != "" {
		parts = append(parts, node)
	}
	if transport != "" {
		parts = append(parts, transport)
	}
	return strings.Join(parts, " ")
}

// clean makes s a single trimmed line of at most maxField characters.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxField {
		r := []rune(s)
		s = string(r[:maxField-1]) + "…"
	}
	return s
}
