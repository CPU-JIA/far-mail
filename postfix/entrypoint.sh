#!/bin/bash
set -e

echo "==> Starting Postfix LMTP front..."

MAIL_HOSTNAME="${SMTP_HOSTNAME:-mail.example.com}"
MAIL_DOMAIN="${MAIL_HOSTNAME#*.}"
[ "$MAIL_DOMAIN" = "$MAIL_HOSTNAME" ] && MAIL_DOMAIN="$MAIL_HOSTNAME"

echo "$MAIL_HOSTNAME     OK" > /etc/postfix/virtual_domains
cat > /etc/postfix/virtual_domains.regexp <<EOF
/^(.*\\.)?$(printf '%s' "$MAIL_HOSTNAME" | sed -e 's/[.[\\*^$()+?{|]/\\&/g')$/    OK
EOF

cat > /usr/local/bin/sync-domains.sh << 'SCRIPT'
#!/bin/bash
set -e
TMP=$(mktemp)
TMP_REGEX=$(mktemp)
if curl -fsS -H "X-Internal-Sync-Key: ${INTERNAL_SYNC_KEY:-}" http://api:8080/internal/domains.txt > "$TMP" 2>/dev/null; then
    if [ -s "$TMP" ]; then
        mv "$TMP" /etc/postfix/virtual_domains
        : > "$TMP_REGEX"
        while IFS= read -r line || [ -n "$line" ]; do
            domain=$(printf '%s' "$line" | awk '{print $1}')
            [ -z "$domain" ] && continue
            escaped=$(printf '%s' "$domain" | sed -e 's/[.[\\*^$()+?{|]/\\&/g')
            printf '/^(.*\\.)?%s$/    OK\n' "$escaped" >> "$TMP_REGEX"
        done < /etc/postfix/virtual_domains
        mv "$TMP_REGEX" /etc/postfix/virtual_domains.regexp
        postfix reload 2>/dev/null || true
    else
        rm -f "$TMP"
        rm -f "$TMP_REGEX"
    fi
else
    rm -f "$TMP"
    rm -f "$TMP_REGEX"
fi
SCRIPT
chmod +x /usr/local/bin/sync-domains.sh

postmap /etc/postfix/virtual_domains
/usr/local/bin/sync-domains.sh || true
(while true; do sleep 60; /usr/local/bin/sync-domains.sh; done) &

postconf -e "myhostname=$MAIL_HOSTNAME"
postconf -e "mydomain=$MAIL_DOMAIN"
postconf -e "virtual_mailbox_domains=regexp:/etc/postfix/virtual_domains.regexp"
postconf -e "virtual_transport=lmtp:inet:api:2527"
postconf -e "lmtp_destination_recipient_limit=1"
postconf -e "lmtp_destination_concurrency_limit=${POSTFIX_LMTP_DESTINATION_CONCURRENCY_LIMIT:-100}"
postconf -e "default_destination_concurrency_limit=${POSTFIX_DEFAULT_DESTINATION_CONCURRENCY_LIMIT:-120}"
postconf -e "lmtp_connection_cache_destinations=${POSTFIX_LMTP_CONNECTION_CACHE_DESTINATIONS:-static:all}"
postconf -e "lmtp_connection_cache_time_limit=${POSTFIX_LMTP_CONNECTION_CACHE_TIME_LIMIT:-30s}"
postconf -e "connection_cache_ttl_limit=${POSTFIX_CONNECTION_CACHE_TTL_LIMIT:-30s}"
postconf -e "import_environment=MAIL_CONFIG MAIL_DEBUG SMTP_HOSTNAME"
postconf -e "export_environment=SMTP_HOSTNAME"

exec postfix start-fg
