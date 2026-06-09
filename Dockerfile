FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o api ./cmd/api && \
    CGO_ENABLED=0 go build -o worker ./cmd/worker

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/api /app/worker /app/
EXPOSE 3000
CMD ["./api"]
