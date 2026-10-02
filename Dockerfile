# ---- Сборка ----
FROM golang:1.24-alpine AS build
# Версия сборки (git short hash): видна в /api/health и в версии кэша SW.
ARG OTKLIK_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath \
    -ldflags="-s -w -X otklik/internal/buildinfo.Version=${OTKLIK_VERSION}" \
    -o /out/otklik ./cmd/server

# ---- Рантайм ----
FROM alpine:3.20
RUN adduser -D -u 10001 app && apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/otklik /app/otklik
RUN mkdir -p /app/data/attachments && chown -R app:app /app/data
USER app
EXPOSE 8080 8081
ENTRYPOINT ["/app/otklik"]
