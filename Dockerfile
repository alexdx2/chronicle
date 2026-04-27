FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /chronicle ./cmd/chronicle

FROM alpine:3.20
COPY --from=builder /chronicle /usr/local/bin/chronicle
WORKDIR /data
ENTRYPOINT ["chronicle"]
