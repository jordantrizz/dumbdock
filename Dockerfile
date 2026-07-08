FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build \
  -ldflags "-X main.version=$(git rev-parse --short HEAD)-$(date -u +%Y%m%dT%H%M%S) -X main.buildNumber=$(git rev-parse --short HEAD)" \
  -o /dumbdock .

FROM scratch
COPY --from=builder /dumbdock /dumbdock
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
ENTRYPOINT ["/dumbdock"]
