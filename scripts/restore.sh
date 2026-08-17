#!/bin/sh
set -eu

usage() {
    echo "Usage: $0 BACKUP_PATH TARGET_DATABASE CONFIRM_DATABASE [--allow-production] [--drop-after-verify]" >&2
    exit 2
}

[ "$#" -ge 3 ] || usage
backup_path=$1
target_database=$2
confirm_database=$3
shift 3
allow_production=0
drop_after_verify=0
for option in "$@"; do
    case "$option" in
        --allow-production) allow_production=1 ;;
        --drop-after-verify) drop_after_verify=1 ;;
        *) usage ;;
    esac
done

[ "$target_database" = "$confirm_database" ] || { echo "Confirmation must exactly match target database" >&2; exit 1; }
case "$target_database" in
    postgres|template0|template1|*[!A-Za-z0-9_]*) echo "Unsafe target database" >&2; exit 1 ;;
esac
[ -f "$backup_path" ] || { echo "Backup not found: $backup_path" >&2; exit 1; }
checksum_path="$backup_path.sha256"
[ -f "$checksum_path" ] || { echo "Checksum not found: $checksum_path" >&2; exit 1; }

expected_hash=$(awk 'NR == 1 {print $1}' "$checksum_path")
if command -v sha256sum >/dev/null 2>&1; then
    actual_hash=$(sha256sum "$backup_path" | awk '{print $1}')
else
    actual_hash=$(shasum -a 256 "$backup_path" | awk '{print $1}')
fi
[ "$expected_hash" = "$actual_hash" ] || { echo "Backup checksum mismatch" >&2; exit 1; }

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
docker compose config --quiet
production_database=$(docker compose exec -T postgres printenv POSTGRES_DB | tr -d '\r\n')
user=$(docker compose exec -T postgres printenv POSTGRES_USER | tr -d '\r\n')

is_production=0
stopped_production=0
if [ "$target_database" = "$production_database" ]; then
    is_production=1
    [ "$allow_production" -eq 1 ] || { echo "Refusing to replace live database without --allow-production" >&2; exit 1; }
    [ "$drop_after_verify" -eq 0 ] || { echo "Cannot drop a production restore target" >&2; exit 1; }
    docker compose stop api postfix pgbouncer
    stopped_production=1
fi

restart_production() {
    if [ "$stopped_production" -eq 1 ]; then
        docker compose up -d pgbouncer api postfix
    fi
}
trap restart_production EXIT HUP INT TERM

docker compose exec -T postgres psql "--username=$user" --dbname=postgres --no-psqlrc --set=ON_ERROR_STOP=1 \
    --command="SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$target_database' AND pid <> pg_backend_pid();" >/dev/null
docker compose exec -T postgres dropdb "--username=$user" --if-exists "$target_database"
docker compose exec -T postgres createdb "--username=$user" "$target_database"
docker compose exec -T postgres pg_restore "--username=$user" "--dbname=$target_database" \
    --exit-on-error --no-owner --no-privileges < "$backup_path"

table_count=$(docker compose exec -T postgres psql "--username=$user" "--dbname=$target_database" \
    --no-psqlrc --tuples-only --no-align --set=ON_ERROR_STOP=1 \
    --command="SELECT COUNT(*) FROM (VALUES (to_regclass('public.accounts')), (to_regclass('public.domains')), (to_regclass('public.mailboxes')), (to_regclass('public.emails')), (to_regclass('public.account_tokens')), (to_regclass('public.domain_donations'))) AS required(table_name) WHERE table_name IS NOT NULL;" | tr -d '\r\n ')
[ "$table_count" = "6" ] || { echo "Restore verification failed: $table_count of 6 tables found" >&2; exit 1; }

printf 'Restore verified in database: %s\nSHA-256: %s\n' "$target_database" "$actual_hash"
if [ "$drop_after_verify" -eq 1 ]; then
    docker compose exec -T postgres dropdb "--username=$user" --if-exists "$target_database"
    printf 'Verification database removed: %s\n' "$target_database"
fi

restart_production
stopped_production=0
trap - EXIT HUP INT TERM
