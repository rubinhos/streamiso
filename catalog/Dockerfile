# Built from the repository root (see the catalog service in docker-compose.yml).

# The web page: TypeScript and Preact, type-checked, tested and bundled into static files
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npx tsc --noEmit && npx vite build

# The service
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY catalog/go.mod ./
COPY catalog/cmd ./cmd
COPY catalog/internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /catalog ./cmd/catalog

FROM alpine:3.21

# HTTPS certificates for TMDB, MusicBrainz and the Cover Art Archive
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /catalog /app/catalog
COPY --from=web /web/dist ./static

EXPOSE 8080
CMD ["/app/catalog"]
