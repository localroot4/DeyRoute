package doctor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/state"
)

// StatusSection renders api.Status (tunnels, nodes with control RTT and
// versions, warnings) as indented JSON for the status section, redacted.
func StatusSection(st api.Status) string {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return redactText(fmt.Sprintf("cannot encode status: %v\n", err))
	}
	return redactText(string(data) + "\n")
}

// EventsSection renders at most EventCount events (newest first), one line
// each: time, level, type, tunnel, node, transition, code and message.
func EventsSection(events []state.Event) string {
	if len(events) > EventCount {
		events = events[:EventCount]
	}
	if len(events) == 0 {
		return "no events\n"
	}
	var b strings.Builder
	for _, e := range events {
		fields := []string{e.At.UTC().Format(time.RFC3339), e.Level, e.Type}
		if e.Tunnel != "" {
			fields = append(fields, "tunnel="+e.Tunnel)
		}
		if e.Node != "" {
			fields = append(fields, "node="+e.Node)
		}
		if e.FromTransport != "" || e.ToTransport != "" {
			fields = append(fields, "transport="+e.FromTransport+"->"+e.ToTransport)
		}
		if e.FromNode != "" || e.ToNode != "" {
			fields = append(fields, "switch_node="+e.FromNode+"->"+e.ToNode)
		}
		if e.Code != "" {
			fields = append(fields, e.Code)
		}
		line := strings.Join(fields, "  ")
		if e.Message != "" {
			line += "  " + oneLine(e.Message)
		}
		if e.Reason != "" {
			line += " (" + oneLine(e.Reason) + ")"
		}
		b.WriteString(line + "\n")
	}
	return redactText(b.String())
}
