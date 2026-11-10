# Multi-stage build for the partner-demo facade.
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/partner-demo .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
  && adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=build /out/partner-demo /app/partner-demo
USER app
EXPOSE 18088
ENV LISTEN_ADDR=:18088
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:18088/healthz >/dev/null || exit 1
CMD ["/app/partner-demo"]
