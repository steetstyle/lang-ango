FROM docker.io/library/golang:1.21-alpine AS builder

RUN apk add --no-cache clang llvm make git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make bpf

RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o lang-ango ./cmd/lang-ango

FROM docker.io/library/alpine:3.19

RUN apk add --no-cache ca-certificates libbpf linux-headers

WORKDIR /app

COPY --from=builder /build/bin/lang-ango .
COPY --from=builder /build/bpf/bin/*.o ./bpf/

RUN mkdir -p /etc/lang-ango

EXPOSE 9400 4317 4318

ENTRYPOINT ["/app/lang-ango"]
