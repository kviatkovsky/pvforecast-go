# syntax=docker/dockerfile:1
# Builds pvforecast-api from source. Release images are built by GoReleaser
# from the already compiled binaries (see Dockerfile.release).
#
#   docker build -t pvforecast-api .
#   docker run --rm -p 8080:8080 pvforecast-api

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
ARG TARGETOS TARGETARCH VERSION=dev
# Use the image's Go instead of downloading the toolchain named in go.mod.
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src
COPY go.mod ./
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pvforecast-api ./cmd/pvforecast-api

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
LABEL org.opencontainers.image.title="pvforecast-api" \
      org.opencontainers.image.description="Physics-based PV power forecasts over HTTP" \
      org.opencontainers.image.source="https://github.com/kviatkovsky/pvforecast-go" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/pvforecast-api /pvforecast-api
# 65532 is the distroless "nonroot" user; a numeric UID lets Kubernetes
# verify runAsNonRoot.
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/pvforecast-api", "healthcheck"]
ENTRYPOINT ["/pvforecast-api"]
