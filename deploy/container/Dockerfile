# A loadout registry as a container, from a release (nothing is compiled
# here). Build it for the machine that will run it:
#
#   docker build --target registry --build-arg LOADOUT_VERSION=0.2.0 -t loadout-registry deploy/container
#   docker run -p 8080:8080 -v loadout-data:/data \
#     -e LOADOUT_PUBLIC_URL=https://registry.example.com loadout-registry
#
# Run exactly one instance: the database is SQLite in /data, and the rate
# limits and device sign-ins live in the process. Put TLS in front (the
# platform's, or a proxy) and tell loadout which proxy to trust, see
# README.md.

# --- Download the release and check it against the release's checksums -----
FROM alpine:3 AS fetch
ARG LOADOUT_VERSION
ARG TARGETARCH
ARG LOADOUT_RELEASE_URL=https://github.com/petervlad/loadout/releases/download
RUN apk add --no-cache ca-certificates curl
WORKDIR /dl
RUN set -eu; \
    [ -n "$LOADOUT_VERSION" ] || { echo "set --build-arg LOADOUT_VERSION=X.Y.Z" >&2; exit 1; }; \
    case "$TARGETARCH" in amd64|arm64) ;; *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; esac; \
    archive="loadout_${LOADOUT_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    curl -fsSL -o "$archive" "$LOADOUT_RELEASE_URL/v${LOADOUT_VERSION}/$archive"; \
    curl -fsSL -o checksums.txt "$LOADOUT_RELEASE_URL/v${LOADOUT_VERSION}/checksums.txt"; \
    line=$(awk -v f="$archive" '$2 == f' checksums.txt); \
    [ -n "$line" ] || { echo "checksums.txt has no entry for $archive" >&2; exit 1; }; \
    echo "$line" | sha256sum -c -; \
    mkdir /out /data; \
    tar -xzf "$archive" -C /out loadout

# --- The image: a static binary, no shell, not root -----------------------
FROM gcr.io/distroless/static-debian12:nonroot AS registry
COPY --from=fetch /out/loadout /usr/local/bin/loadout
# /data is the registry's folder: owned by the image's user (uid 65532).
COPY --from=fetch --chown=65532:65532 /data /data
ENV HOME=/data
VOLUME /data
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/loadout"]
CMD ["serve", "--data", "/data", "--addr", ":8080"]

# --- Same image, run as root, for platforms that mount volumes root-owned --
# (Fly Machines do). Build it with --target root-volume. It is still one
# static binary in a container with no shell.
FROM registry AS root-volume
USER 0:0
