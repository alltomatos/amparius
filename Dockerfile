# syntax=docker/dockerfile:1

# Build multi-estágio dos binários do SaaS.
# Uso:
#   docker build --build-arg SERVICE=api    -t amparius-api:dev .
#   docker build --build-arg SERVICE=worker -t amparius-worker:dev .
# Em PowerShell: .\scripts\build.ps1 docker

FROM golang:1.24-alpine AS build

ARG SERVICE=api
WORKDIR /src

COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/service \
    ./cmd/${SERVICE}

FROM alpine:3.20

RUN addgroup -S amparius && adduser -S -G amparius amparius

USER amparius
COPY --from=build /out/service /usr/local/bin/service

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/service"]
