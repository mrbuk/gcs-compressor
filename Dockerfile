FROM golang:1.23 AS build
WORKDIR /go/src/gcs-compressor
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o ./build -ldflags '-extldflags "-static"' -v ./...

# build a minimal image
FROM scratch
# the tls certificates:
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# the actual binary
COPY --from=build /go/src/gcs-compressor/build/* /
WORKDIR /data
ENTRYPOINT ["/gcs-compressor"]