FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /catalog ./cmd/catalog

FROM alpine:3.21

# HTTPS certificates for TMDB, MusicBrainz and the Cover Art Archive
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /catalog /app/catalog
COPY static ./static

EXPOSE 8080
CMD ["/app/catalog"]
