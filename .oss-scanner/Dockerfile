# Scanner image for https://github.com/afjcjsbx/picoclaw (main).
#
# PicoClaw is a Go agent runtime with chat channels, an LLM tool loop, MCP clients, plugins and a Web
# launcher. The audit runs offline, so this build fetches every Go module, compiles every package and
# its tests, and runs the full unit suite once so the log shows the image works.
# threat_model.md, next to this file, says what matters and how to exercise it.

# Same Go release as go.mod (go 1.27.3). trixie ships the recent git the tests expect.
FROM golang:1.27.3-trixie
ENV DEBIAN_FRONTEND=noninteractive

# git for version stamping and for tests that shell out to git; gcc because some tests build helpers.
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git make build-essential curl python3 \
    && rm -rf /var/lib/apt/lists/*

# scanner contract: the checkout lives inside the image, at /src
COPY . /src
WORKDIR /src

# Fetch every dependency the build or the tests need; after this step there is no network.
RUN go mod download

ENV GOPROXY=off \
    GOSUMDB=off \
    GOTOOLCHAIN=local

# Build every package and compile the tests, as `go test -tags goolm,stdjson ./...` runs them. The
# goolm tag selects the pure-Go Matrix crypto implementation, so CGO is not needed.
RUN go build -tags goolm,stdjson ./... \
    && go test -tags goolm,stdjson -run '^$' ./...

# Tests that inject failures through read-only files or directories rely on the permission check, which
# root bypasses. Run them as an unprivileged user that owns /src and the Go caches, like CI does.
RUN useradd --create-home --shell /bin/bash scanner \
    && chown -R scanner:scanner /src /go \
    && git config --system --add safe.directory /src

# Run the full unit suite so the build log proves the image works. A failing test must not abort the
# build; the scanner re-runs anything it needs from its root shell, see threat_model.md.
RUN runuser -u scanner -- env HOME=/home/scanner timeout 1800 \
      go test -tags goolm,stdjson ./... || echo "WARNING: go test reported failures"
