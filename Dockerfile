# Build
FROM golang:1.24 AS build
WORKDIR /src
# go.mod first for layer caching (no go.sum: stdlib-only module).
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

# Run — assets are embedded, so the image is just the binary.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
