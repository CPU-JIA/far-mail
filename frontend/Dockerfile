FROM node:22.20.0-alpine3.22 AS builder

WORKDIR /app
COPY package.json package-lock.json ./
ARG NPM_REGISTRY=https://registry.npmmirror.com
RUN npm ci --registry="${NPM_REGISTRY}" --fetch-retries=5 --fetch-retry-mintimeout=2000 --fetch-retry-maxtimeout=20000

COPY . .
RUN npm run build \
    && find dist/assets -type f \( -name '*.js' -o -name '*.css' -o -name '*.svg' \) -size +1024c \
       -exec sh -c 'for file do gzip -9 -c "$file" > "$file.gz"; done' sh {} +

FROM golang:1.25.12-alpine3.24 AS server-builder

WORKDIR /src
COPY server/go.mod ./
COPY server/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/frontend-server .

FROM scratch

COPY --from=server-builder /out/frontend-server /frontend-server
COPY --from=builder --chown=65532:65532 /app/dist /srv

USER 65532:65532
ENV PORT=8080 WEB_ROOT=/srv PUBLIC_API_ORIGIN=""

EXPOSE 8080

ENTRYPOINT ["/frontend-server"]
