# syntax=docker/dockerfile:1

# The Vue frontend, built to web/dist.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# The Go server, with the frontend embedded.
FROM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/hochhuus .

# What runs: one static binary, no shell, not root.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=server /out/hochhuus /hochhuus
ENV PORT=8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/hochhuus"]
