FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG SERVICE
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/service ./services/${SERVICE}/

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/service /usr/local/bin/service

ENTRYPOINT ["/usr/local/bin/service"]
