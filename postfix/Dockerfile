# ============================================================
# Postfix 收件容器
# ============================================================
FROM alpine:3.24.1

RUN apk add --no-cache bash ca-certificates curl postfix postfix-pcre tzdata

ENV TZ=Asia/Shanghai

COPY main.cf /etc/postfix/main.cf
COPY master.cf /etc/postfix/master.cf
COPY entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh
RUN touch /etc/postfix/virtual_domains && postmap /etc/postfix/virtual_domains

EXPOSE 25
CMD ["/entrypoint.sh"]
