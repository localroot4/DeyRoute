package sysinfo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// Container types Virt reports. A VM or bare metal is VirtNone: its kernel
// is its own, so kernel tuning applies.
const (
	VirtNone      = ""
	VirtOpenVZ    = "openvz"
	VirtLXC       = "lxc"
	VirtDocker    = "docker"
	VirtContainer = "container" // any other container (podman, nspawn, ...)
)

// Virt returns the container type the host runs in (Virt*), from
// /proc/vz without /proc/bc (an OpenVZ container; the OpenVZ host has
// both), /run/systemd/container, container= in /proc/1/environ,
// /.dockerenv and /run/.containerenv.
func Virt(root string) string {
	if exists(root, "/proc/vz") && !exists(root, "/proc/bc") {
		return VirtOpenVZ
	}
	if v := readTrim(filepath.Join(root, "/run/systemd/container")); v != "" {
		return containerType(v)
	}
	environ, err := os.ReadFile(filepath.Join(root, "/proc/1/environ")) // #nosec G304 -- procfs below root
	if err == nil {
		for _, kv := range bytes.Split(environ, []byte{0}) {
			if v, ok := bytes.CutPrefix(kv, []byte("container=")); ok && len(v) > 0 {
				return containerType(string(v))
			}
		}
	}
	if exists(root, "/.dockerenv") {
		return VirtDocker
	}
	if exists(root, "/run/.containerenv") {
		return VirtContainer
	}
	return VirtNone
}

// containerType maps a systemd container name to a Virt* value.
func containerType(v string) string {
	switch v = strings.ToLower(strings.TrimSpace(v)); {
	case v == "":
		return VirtNone
	case strings.HasPrefix(v, "lxc"): // lxc, lxc-libvirt
		return VirtLXC
	case v == "docker":
		return VirtDocker
	case v == "openvz":
		return VirtOpenVZ
	default:
		return VirtContainer
	}
}

// exists reports whether the path below root exists.
func exists(root, p string) bool {
	_, err := os.Lstat(filepath.Join(root, p))
	return err == nil
}
