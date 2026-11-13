# Disposable "fresh VPS" for the installer end-to-end test (make install-e2e):
# plain Ubuntu 24.04 without systemd, with sshd accepting the root password.
# Docker is NOT preinstalled: server-bootstrap.sh installs it like on a VPS.
FROM ubuntu:24.04
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-server iptables \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd \
 && printf 'PermitRootLogin yes\nPasswordAuthentication yes\nKbdInteractiveAuthentication yes\n' \
      > /etc/ssh/sshd_config.d/10-e2e.conf
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]
