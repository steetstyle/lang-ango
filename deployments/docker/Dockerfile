FROM docker.io/library/golang:1.24-alpine AS builder

RUN apk add --no-cache clang llvm make git linux-headers libbpf-dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make bpf

RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o bin/lang-ango ./cmd/lang-ango

FROM docker.io/library/alpine:3.19

RUN apk add --no-cache ca-certificates libbpf linux-headers

WORKDIR /app

RUN mkdir -p /etc/lang-ango /var/run/lang-ango/bpf

COPY --from=builder /build/bin/lang-ango .
COPY --from=builder /build/bpf/bin/ /var/run/lang-ango/bpf/

EXPOSE 9400 4317 4318

ENTRYPOINT ["/app/lang-ango"]
