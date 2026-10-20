# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/moyue .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /app/books /app/data
COPY --from=build /out/moyue /usr/local/bin/moyue
WORKDIR /app
EXPOSE 8080
VOLUME ["/app/data", "/app/books"]
ENTRYPOINT ["moyue", "-addr", ":8080", "-books", "/app/books", "-data", "/app/data"]
