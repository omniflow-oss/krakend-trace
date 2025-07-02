# KrakenD Test Plugin Repository (fixed)

## Contents
* **plugin/** — Minimal Go plugin compiled into `test-plugin.so`
* **runtime.Dockerfile** — Builds a KrakenD image (`krakend:2.10.1`) that embeds the plugin.
* **.github/workflows/krakend-plugin.yml** — CI that
  1. Compiles the plugin using `krakend/builder:2.10.1`
  2. Builds and publishes the runtime image to Docker Hub on GitHub Releases.

## Secrets
| Name | Purpose |
|------|---------|
| `DOCKERHUB_USERNAME` | Your Docker Hub username |
| `DOCKERHUB_TOKEN`    | A Docker Hub access token with repo write permission |

## Local build & test
```bash
# Compile plugin
docker run --rm -v $PWD:/src -w /src/plugin krakend/builder:2.10.1   go build -trimpath -buildmode=plugin -o /src/.dist/test-plugin.so

# Build runtime image
docker build -f runtime.Dockerfile --build-arg KRKN_VERSION=2.10.1 -t krakend-test-plugin:local .

# Run
docker run -p 8080:8080 krakend-test-plugin:local
```