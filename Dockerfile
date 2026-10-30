# Build the React UI, then the Go server, and ship one small binary.
FROM node:24-alpine AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24-alpine AS build

WORKDIR /src
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.versionOverride=${VERSION}" -o /out/redline ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/redline /redline
EXPOSE 8787
USER nonroot
ENTRYPOINT ["/redline"]
