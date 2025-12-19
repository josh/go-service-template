FROM golang:1.25.5-alpine3.23 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-extldflags "-static"' -o example .

FROM scratch
COPY --from=builder /build/example /example
EXPOSE 8080
ENTRYPOINT ["/example"]
