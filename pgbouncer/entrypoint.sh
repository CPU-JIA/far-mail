#!/bin/bash
set -e

case "${POSTGRES_USER:-}" in
    ''|*[!A-Za-z0-9_]*) echo "Unsupported PostgreSQL user" >&2; exit 1 ;;
esac

escape_user=$(printf '%s' "$POSTGRES_USER" | sed 's/[\\&|]/\\&/g')
config_file=/tmp/pgbouncer.ini
sed "s|__POSTGRES_USER__|$escape_user|g" /etc/pgbouncer/pgbouncer.ini > "$config_file"

# 生成 PgBouncer 用户列表（明文密码，用于 scram-sha-256 透传）
PGBOUNCER_AUTH_FILE="/etc/pgbouncer/userlist.txt"
escape_userlist() {
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/[[:cntrl:]]//g'
}
printf '"%s" "%s"\n' "$(escape_userlist "$POSTGRES_USER")" "$(escape_userlist "${POSTGRES_PASSWORD:-}")" > "$PGBOUNCER_AUTH_FILE"

exec pgbouncer "$config_file"
