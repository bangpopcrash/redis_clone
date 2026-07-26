FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/redis-server ./cmd/redis-server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/redis-server /redis-server
EXPOSE 6379
ENTRYPOINT ["/redis-server", "-addr=0.0.0.0:6379"]
