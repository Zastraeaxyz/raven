#!/bin/sh
set -eu

# Railway mounts volumes as root. Fix ownership of the data directory before
# dropping privileges so the persona can write its SOUL and memory files.
if [ "$(id -u)" = "0" ]; then
	mkdir -p "${MEMORY_DIR:-/data}"
	chown -R 10001:10001 "${MEMORY_DIR:-/data}"
	exec su-exec 10001:10001 /app/raven
fi

exec /app/raven
