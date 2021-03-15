## Build stage
FROM golang:1.16-alpine AS build
WORKDIR /app
ENV CGO_ENABLED=0

# Download deps
COPY go.mod ./
COPY go.sum ./
RUN go mod download

# Build app
COPY main.go ./
RUN go build -v -o url-manager

# Test
RUN go test -v .

## Runtime stage
FROM alpine:3 AS runtime
WORKDIR /app

COPY --from=build /app/url-manager ./

ENTRYPOINT ["./url-manager"]
CMD [""]
