FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build \
  -ldflags "-X main.version=$(git rev-parse --short HEAD)-$(date -u +%Y%m%dT%H%M%S) -X main.buildNumber=$(git rev-parse --short HEAD)" \
  -o /dumbdock .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /dumbdock /dumbdock
EXPOSE 8080
ENTRYPOINT ["/dumbdock"]
