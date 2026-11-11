ARG GO_VERSION=1.27.4
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
ENV GOWORK=off
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 make binary VERSION="${VERSION}"

# Typed operations use go/packages and need the Go toolchain at runtime.
FROM golang:${GO_VERSION}-bookworm
COPY --from=build /src/bin/gopatch /usr/local/bin/gopatch
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/gopatch"]
CMD ["--help"]
