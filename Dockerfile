# Build
FROM golang:1.26.10 AS build
WORKDIR /src
# go.mod first for layer caching (no go.sum: stdlib-only module).
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

# Run — assets are embedded, so the image is just the binary.
FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
