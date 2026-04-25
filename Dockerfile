FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /oracle ./cmd/oracle

FROM alpine:3.20
COPY --from=builder /oracle /usr/local/bin/oracle
WORKDIR /data
ENTRYPOINT ["oracle"]
