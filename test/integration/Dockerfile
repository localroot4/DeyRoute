# Test image for the integration scenarios (spec section 17): a Tier 1
# distribution with systemd as PID 1 plus the tools deyroute calls (nft, ip, ss)
# and the helpers the scenarios use (curl, python3 for traffic generators).
#
#   docker build --build-arg BASE=debian:12 -t deyroute-it:debian-12 test/integration
#
# Containers from this image run privileged with the host cgroup namespace
# (see compose.yml), which is what systemd needs inside Docker.
ARG BASE=ubuntu:24.04
FROM ${BASE}
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv dbus nftables iproute2 \
      ca-certificates curl procps python3 openssl kmod \
 && apt-get clean && rm -rf /var/lib/apt/lists/* \
 && rm -f /etc/systemd/system/*.wants/* /lib/systemd/system/multi-user.target.wants/getty* \
 && systemctl mask systemd-logind.service getty.target console-getty.service 2>/dev/null || true
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
