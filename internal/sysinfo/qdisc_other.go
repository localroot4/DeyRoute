//go:build !linux

package sysinfo

// RootQdisc returns the kind of the root queueing discipline of iface. Only
// Linux has rtnetlink: elsewhere it is always "".
func RootQdisc(string) string { return "" }
