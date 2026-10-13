# The loadout demo: a registry full of fake teams and data, and a shell to
# try the CLI in. Built from this checkout, from the repository root:
#
#   docker compose up            the registry, on http://localhost:8080
#   docker compose run --rm cli  a shell signed in as alice
#
# See compose.yaml. Nothing here talks to the internet once built.

# --- Build the binary from the checkout -----------------------------------
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/loadout ./cmd/loadout \
    && mkdir /out/data /out/backups

# --- The registry: a static binary, no shell, not root ---------------------
# `demo up` seeds /data when it is empty and serves it. LOADOUT_DEMO=1 makes
# serve refuse anything but a localhost public address and a folder the seed
# made, because the demo's password is printed on its sign-in page. The
# container listens on every address and compose publishes it on 127.0.0.1.
FROM gcr.io/distroless/static-debian12:nonroot AS registry
COPY --from=build /out/loadout /usr/local/bin/loadout
COPY --from=build --chown=65532:65532 /out/data /data
# The copy a web delete takes first. Outside /data, so /data stays empty
# for the seed.
COPY --from=build --chown=65532:65532 /out/backups /backups
ENV HOME=/home/nonroot \
    LOADOUT_DEMO=1 \
    LOADOUT_PUBLIC_URL=http://localhost:8080 \
    LOADOUT_BACKUP_DIR=/backups
VOLUME /data
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/loadout"]
CMD ["demo", "up", "--data", "/data", "--addr", ":8080"]

# --- A shell with bash, git and loadout, signed in as alice -----------------
FROM debian:bookworm-slim AS cli
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates curl git less \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --shell /bin/bash alice \
    && mkdir /work && chown alice:alice /work
COPY --from=build /out/loadout /usr/local/bin/loadout
COPY deploy/demo/cli-entrypoint.sh /usr/local/bin/demo-shell
COPY --chown=alice:alice deploy/demo/sample-app /work/sample-app
COPY --chown=alice:alice deploy/demo/personal-memory /work/personal-memory
USER alice
ENV HOME=/home/alice
WORKDIR /work/sample-app
RUN git config --global user.name "Alice" \
    && git config --global user.email alice@example.com \
    && git init -q -b main . \
    && git add -A \
    && git commit -q -m "Start the sample app"
ENTRYPOINT ["/usr/local/bin/demo-shell"]
CMD ["bash"]
