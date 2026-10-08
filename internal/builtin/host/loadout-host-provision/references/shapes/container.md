# The container shape

A platform that runs a container for you (Fly.io is the one with a recipe here). `assets/container/Dockerfile` builds an image from a release: a static `loadout` binary on a distroless base, not root, `/data` as the volume, `loadout serve --data /data --addr :8080`. Nothing is compiled, and the build stops if the release archive does not match the release's `checksums.txt`.

## Rules of this shape

- **One instance only.** The database is SQLite in `/data`, and the sign-in limits and device sign-ins live in the process. One machine, one volume, never scale out; turn off any "high availability" default that adds a spare machine.
- **TLS is the platform's.** loadout speaks plain HTTP on 8080 behind the platform's proxy.
- **The proxy is not on the same machine**, so every request looks like it comes from the proxy and the per-address sign-in limits become one bucket for everyone. Set `LOADOUT_TRUSTED_PROXIES` (the range the platform's proxy connects from) and `LOADOUT_CLIENT_IP_HEADER` (the single-address header the platform sets). Use the range the platform documents; never `0.0.0.0/0`, and never `X-Forwarded-For`: loadout refuses both. `loadout serve --check` prints a `proxies` line saying what is in force.
- **Secrets go to the platform's secret store** (SMTP password, Google secret), never into the image or a committed file. Non-secret settings go in the platform's configuration.
- **No shell in the image.** Host commands run as the binary itself through the platform's console, for example `loadout admin grant --email E --data /data`. A password prompt needs a terminal.
- **No nightly backup unit.** Back the volume up with the platform's snapshots, and pull a copy as the provider file says.

## Flow

1. Copy `assets/container/Dockerfile` and the provider's configuration example into a scratch folder.
2. Create the app and the volume, set the non-secret settings, then the secrets (the person writes them to a file with their own editor and imports it; you never see them).
3. Deploy. Then check `https://NAME/v1/health`.
4. Add the platform's certificate for the person's own domain, if they have one, after DNS is right ([dns.md](../dns.md)).
5. Run `loadout serve --check` through the platform's console, and check the `proxies` line shows the header and range you set.

To build the image anywhere else (`--target registry` is the non-root image; without a target Docker builds the last stage, which runs as root for platforms that mount volumes root-owned):

```sh
docker build --target registry --build-arg LOADOUT_VERSION=X.Y.Z -t loadout-registry assets/container
docker run -p 8080:8080 -v loadout-data:/data -e LOADOUT_PUBLIC_URL=https://registry.example.com loadout-registry
```

The container files were written from the platforms' documentation and have not been run against a live account by loadout's tests: say so, and try a throwaway app first.
