#!/bin/sh
# Entrypoint of the disposable test server: prepare cgroup v2 for nested
# Docker (as the official dind image does) and run sshd in the foreground.
# The test sets the root password afterwards with "docker exec ... chpasswd",
# so it never appears in the container configuration.
set -eu

if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	# Move every process out of the root cgroup so controllers can be
	# delegated to the nested dockerd.
	mkdir -p /sys/fs/cgroup/init
	xargs -rn1 </sys/fs/cgroup/cgroup.procs >/sys/fs/cgroup/init/cgroup.procs 2>/dev/null || true
	sed -e 's/ / +/g' -e 's/^/+/' </sys/fs/cgroup/cgroup.controllers \
		>/sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true
fi

# The installer clones a local snapshot of the repository; it is owned by
# the host user, so let root's git use it.
git_cfg=/root/.gitconfig
printf '[safe]\n\tdirectory = *\n' >"$git_cfg"

ssh-keygen -A >/dev/null
exec /usr/sbin/sshd -D -e
