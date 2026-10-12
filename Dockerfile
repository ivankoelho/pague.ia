# 1) frontend
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# 2) backend com o frontend embutido
FROM golang:1.24-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pague ./cmd/server

# 3) runtime: OCR (poppler + tesseract pt) no mesmo container do app
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata poppler-utils tesseract-ocr tesseract-ocr-por \
    && rm -rf /var/lib/apt/lists/* && useradd -r -u 10001 pague && mkdir /data && chown pague /data
COPY --from=go /pague /usr/local/bin/pague
USER pague
ENV DATA_DIR=/data ADDR=:8080 TZ=America/Bahia
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD ["/bin/sh", "-c", "exec 3<>/dev/tcp/127.0.0.1/8080 && printf 'GET /healthz HTTP/1.0\r\n\r\n' >&3 && grep -q ok <&3"]
ENTRYPOINT ["pague"]
