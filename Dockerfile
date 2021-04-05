ARG APP_VERSION=0.0.0-SNAPSHOT

## Build stage
FROM golang:1.16-alpine AS build
ARG APP_VERSION
ENV CGO_ENABLED=0
WORKDIR /app

# Download deps
COPY go.mod ./
COPY go.sum ./
RUN go mod download

# Build app
COPY *.go ./
RUN go build -v -ldflags="-X 'main.appVersion=${APP_VERSION}'" -o url-manager

# Test
RUN go test -v .

## Runtime stage
FROM alpine:3 AS runtime
WORKDIR /app

COPY --from=build /app/url-manager ./

ENTRYPOINT ["./url-manager"]
CMD [""]
