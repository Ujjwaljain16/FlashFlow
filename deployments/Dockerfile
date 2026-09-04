# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod ./
# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build binaries
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/http-origin ./cmd/http-origin
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/edge ./cmd/edge
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/proxy ./cmd/proxy

# Final lean runtime image
FROM alpine:3.20

RUN apk --no-cache add curl ca-certificates

COPY --from=builder /bin/http-origin /usr/local/bin/http-origin
COPY --from=builder /bin/edge /usr/local/bin/edge
COPY --from=builder /bin/proxy /usr/local/bin/proxy

CMD ["/usr/local/bin/proxy"]
