#!/bin/sh
set -eu

umask 077
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${1:-"$project_root/backups"}

cd "$project_root"
docker compose config --quiet
database=$(docker compose exec -T postgres printenv POSTGRES_DB | tr -d '\r\n')
user=$(docker compose exec -T postgres printenv POSTGRES_USER | tr -d '\r\n')
case "$database:$user" in
    *[!A-Za-z0-9_.:-]*) echo "Unsupported PostgreSQL database or user" >&2; exit 1 ;;
esac

mkdir -p "$output_dir"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
file_name="far-mail-$database-$timestamp.dump"
backup_path="$output_dir/$file_name"
partial_path="$backup_path.partial.$$"
trap 'rm -f "$partial_path"' EXIT HUP INT TERM

docker compose exec -T postgres pg_dump \
    "--username=$user" "--dbname=$database" \
    --format=custom --compress=6 --no-owner --no-privileges > "$partial_path"
mv "$partial_path" "$backup_path"
trap - EXIT HUP INT TERM

if command -v sha256sum >/dev/null 2>&1; then
    hash=$(sha256sum "$backup_path" | awk '{print $1}')
else
    hash=$(shasum -a 256 "$backup_path" | awk '{print $1}')
fi
printf '%s  %s\n' "$hash" "$file_name" > "$backup_path.sha256"
printf '{"format":"postgresql-custom","database":"%s","created_at":"%s","file":"%s","bytes":%s,"sha256":"%s","redis_included":false}\n' \
    "$database" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$file_name" "$(wc -c < "$backup_path" | tr -d ' ')" "$hash" > "$backup_path.json"

printf 'Backup created: %s\nSHA-256: %s\n' "$backup_path" "$hash"
