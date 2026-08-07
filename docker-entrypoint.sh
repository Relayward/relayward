#!/bin/sh
set -eu

case "${1:-}" in
    admin|healthcheck|serve)
        set -- relayward "$@"
        ;;
esac

data_dir=/var/lib/relayward
if [ "$(id -u)" = "0" ]; then
    mkdir -p "$data_dir"
    if [ "$(stat -c '%u:%g' "$data_dir")" != "10001:10001" ]; then
        chown -hR relayward:relayward "$data_dir"
    fi
    chmod 0700 "$data_dir"
    exec gosu relayward:relayward "$@"
fi

exec "$@"
