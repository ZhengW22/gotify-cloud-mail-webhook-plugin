# Gotify Cloud Mail Webhook Plugin

[中文](README.md) | **English**

A native Gotify plugin for receiving the existing email webhooks from [cloud-mail](https://github.com/maillab/cloud-mail). **No changes to cloud-mail, additional service, or Gotify application token are required.** The plugin receives requests and submits notifications inside the Gotify process.

## Features

- Uses the email subject as the notification title and includes the sender, recipient, verification code, and email timestamp.
- Prefers the `text` field. When plain text is unavailable, converts HTML from `content` into readable text while preserving ordinary HTTP/HTTPS links.
- Omits scripts, styles, and images. Does not execute scripts or fetch external resources, and does not preserve the original email layout.
- Supports cloud-mail's optional webhook secret by validating the exact value of the `Authorization` header.
- Configurable notification body limit: 6,000 Unicode characters by default, including email metadata. Truncated messages are explicitly marked.
- Maximum request size: 8 MiB. Returns HTTP 200 only for a valid request successfully submitted to Gotify; failures return a non-2xx response so cloud-mail can retry.
- Deduplicates successful requests by positive integer `emailId` within each plugin instance. Stores up to 1,024 IDs in memory for 24 hours. State is cleared on restart, so delivery is not guaranteed to be exactly once across restarts. Use one plugin instance per cloud-mail instance to avoid ID collisions.

## Supported version and architectures

Currently targets the official **`gotify/server:3.1.1`** Docker image, using the official **Go 1.26.0** build environment and matching shared dependency versions.

Native Go plugins are sensitive to the toolchain, shared dependencies, architecture, and build options. A `.so` built with a different local Go version may not load in the official image. Pin the Gotify image version rather than assuming an existing plugin will continue working after updating `latest`.

`compatibility/server.go.mod` and `compatibility/GO_VERSION` are snapshots of the official Gotify v3.1.1 source files. The project's `go.mod` was derived by pruning that dependency graph.

The build script also preserves the official `/go/pkg/mod` dependency path. Go plugin package fingerprints can depend on source paths, so matching versions alone may not be sufficient. Downloaded modules are cached in the project's `.build-cache/` directory, which is excluded from Git.

Available build targets:

| Server architecture | Build command |
| --- | --- |
| ARM64 / aarch64 | `make build-arm64` |
| 32-bit ARMv7 / armv7l | `make build-arm7` |
| x86_64 | `make build-amd64` |

Run `uname -m` on the Gotify server to distinguish ARM64 from ARMv7. Also verify the architecture of the image actually running in the container.

The build script runs Gotify's official cross-compilation images on an **amd64 build machine**, including the CGO toolchain required for plugins. It does not run the build inside an ARM container. Running it directly on an ARM host requires amd64 emulation. Build on an amd64 computer and copy the resulting `.so` to your server, or upload the repository to GitHub, manually run the **Build plugins** workflow, and download the appropriate artifact.

```bash
make test
make vet
make build-arm64
```

Example output:

```text
build/cloud-mail-webhook-gotify-v3.1.1-linux-arm64.so
```

Before upgrading Gotify, check the new release's `GO_VERSION`, `go.mod`, and official Dockerfile. Update the compatibility snapshots, project dependencies, and build version, then rebuild and verify the plugin. See the [official plugin build documentation](https://gotify.net/docs/plugin-deploy).

## Docker installation

Create a `plugins` directory beside your existing Gotify Compose file. Place **only one `.so` matching your server architecture** in that directory. Do not include checksum files, READMEs, archives, or plugins for other architectures: Gotify attempts to load files from its plugin directory.

Add the following settings to your existing Gotify service, preserving its database and data volumes:

```yaml
services:
  gotify:
    image: gotify/server:3.1.1
    environment:
      GOTIFY_PLUGINSDIR: /app/plugins
    volumes:
      # Keep your existing /app/data mount unchanged.
      - ./plugins:/app/plugins:ro
```

This is a configuration fragment to merge into your existing service. Preserve other environment variables and volume mounts. Hosts running SELinux may need appropriate volume labels, such as `:ro,Z` for a directory used by one container.

`compose.example.yml` is an example for a **new installation**. Do not use it to replace an existing Gotify data directory.

Recreate the Gotify container after updating its configuration; adjust the service name if necessary:

```bash
docker compose up -d gotify
docker compose logs --tail=100 gotify
```

An error containing `built with a different version` usually indicates a Go or shared dependency mismatch. For ELF or architecture errors, check that the `.so` matches the container image architecture.

## Configuration and cloud-mail integration

1. Sign in to Gotify, open the plugins page, and find **Cloud Mail Webhook**.
2. Review and save the configuration, then enable the plugin. A random secret is generated by default. You can replace it with your own random value of at least 16 bytes.

```yaml
secret: "replace-with-a-random-secret"
priority: 5
max_body_chars: 6000
```

Use visible ASCII characters without spaces for `secret`; keeping the generated random value is recommended. Valid ranges are 0–10 for `priority` and 100–20,000 for `max_body_chars`.

3. The plugin's instructions page displays a receiving URL specific to the current user, for example:

```text
https://gotify.example.com/plugin/<plugin-id>/custom/<plugin-token>/receive
```

If Gotify is hosted under a URL subpath, verify that the generated URL matches your reverse proxy configuration.

4. In **cloud-mail → System settings → Webhook**, configure:

| Setting | Value |
| --- | --- |
| Status | Enabled |
| Request URL | The complete HTTPS receiving URL from the plugin's instructions page |
| Secret | The exact `secret` from the plugin configuration; **do not add a `Bearer ` prefix** |
| Retries | 2 recommended |

Do not place the secret in the URL. No changes to cloud-mail or manual selection of a Gotify application are needed. Each Gotify user's plugin instance sends messages through its own plugin application.

**If the Gotify hostname is protected by Cloudflare Access email verification, configure appropriate machine access for this receiving endpoint.** The current cloud-mail webhook does not send the two dedicated Access Service Token headers; copying the URL alone cannot complete an email verification login.

## Verify the integration

Send an email containing a subject, sender, and verification code to cloud-mail. Then send an email containing only an HTML body. Check both notifications in Gotify.

You can also test the plugin endpoint directly. The following Bash commands read the secret interactively rather than embedding it in the command line. Replace `<plugin-id>` and `<plugin-token>`; preferably copy the complete URL from the plugin's instructions page:

```bash
read -rsp 'Webhook secret: ' MAIL_WEBHOOK_SECRET
echo
printf 'Authorization: %s\n' "$MAIL_WEBHOOK_SECRET" | \
  curl --fail-with-body --header @- \
    --header 'Content-Type: application/json' \
    --data '{"emailId":10001,"sendEmail":"sender@example.com","sendName":"Test sender","toEmail":"me@example.com","subject":"HTML email test","text":"","content":"<h1>Hello</h1><p>Verification code: <b>123456</b></p>","code":"123456"}' \
    'https://gotify.example.com/plugin/<plugin-id>/custom/<plugin-token>/receive'
unset MAIL_WEBHOOK_SECRET
```

A successful request returns `{"status":"success"}`. Repeating the same positive `emailId` returns `{"status":"duplicate"}`; use a different ID for a new test. Requests without an `emailId`, or with an ID of 0, are not deduplicated.

Error responses: 401 for an incorrect secret, 400 for invalid JSON or email data, 413 for an oversized request, 415 for an incorrect Content-Type, 503 when the plugin is disabled or its message handler is unavailable, and 502 when message submission fails.

The plugin's Gotify configuration instructions and generated notification metadata labels currently use Chinese. This English document does not change the language of the plugin itself.

## Development and validation

```bash
go test -race ./...
go vet ./...
```

Run the official-container end-to-end test on an amd64 build machine:

```bash
make build-amd64
python3 scripts/smoke.py
```

The script creates a temporary Gotify 3.1.1 container and checks plugin loading, authentication, plain-text and HTML notifications, sender and recipient metadata, and retry deduplication. Test messages are sent only to this temporary local container, which is removed afterward. It does not connect to an existing Gotify server or modify existing data. Docker access is required; Python is used only for this development test.

Tests cover the existing webhook fields, authentication, failed delivery retries, concurrent deduplication, malformed and oversized requests, plain-text preference, HTML-to-text conversion, and Unicode truncation. Passing tests with a local Go version does not prove compatibility with the official image; build the `.so` with the official environment and verify container loading.

The current implementation has passed `go test -race -cover ./...` with 91.5% statement coverage and `go vet ./...`. An amd64 plugin built with Go 1.26.0 has passed the end-to-end test in the official `gotify/server:3.1.1` container. Build scripts and workflows are provided for ARM64 and ARMv7, but runtime behavior has not yet been verified on ARM hardware.

Native Gotify plugins require a system supported by Go's plugin mechanism; this project's distribution targets Linux. The plugin requires no additional listening port, separate container, or database.
