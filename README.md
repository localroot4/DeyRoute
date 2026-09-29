# DEYROUTE Tunnel Manager

DEYROUTE connects users inside Iran to `IRAN_IP:PORT` and carries their traffic,
without touching its TLS, through a filtering-resistant tunnel to a foreign
server. When filtering blocks the active transport it moves to the next one,
and then to a backup server, automatically.

- **Hub** — the Iran server: entry point for users and the brain (menu, state,
  failover engine).
- **Node** — a foreign server running the real VPN service and the other side
  of the tunnel. Nodes dial the hub; SSH is never used.

Documentation: [English](docs/en/README.md) · [فارسی](docs/fa/README.md) ·
[error codes](docs/ERRORS.md) · [open questions](QUESTIONS.md)

## Quick start

```bash
# On the Iran server (hub):
bash <(curl -fsSL https://get.deyroute.example/install.sh)
# The menu prints a one-line join command; run it on the foreign server.
```

## Build

```bash
make build        # ./dist/deyroute (static, CGO_ENABLED=0)
make build-all    # linux/amd64 + linux/arm64
make test lint
```
