FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /lang-ango-agent ./cmd/lang-ango/

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /lang-ango-agent /usr/local/bin/lang-ango-agent

RUN mkdir -p /tmp && chmod 777 /tmp

EXPOSE 8080

CMD ["lang-ango-agent"]