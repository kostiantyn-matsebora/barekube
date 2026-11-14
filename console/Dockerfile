# Build on the host platform and cross-compile. Under buildx the default runs
# the whole compile in an emulated arm64 container, which takes minutes.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /console ./cmd/console
# The state lives here; an empty volume mounted on it takes this owner.
RUN mkdir /config

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /console /console
COPY --from=build --chown=nonroot:nonroot /config /config
# A soft limit for the garbage collector; the console is small.
ENV GOMEMLIMIT=96MiB CONSOLE_DB=/config/console.db
VOLUME /config
EXPOSE 8080
ENTRYPOINT ["/console"]
CMD ["-config", "/etc/console/config.yaml"]
