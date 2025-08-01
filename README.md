# KrakenD trace Plugin Repository

## Contents
* **plugin/** — Minimal Go plugin compiled into `trace-plugin.so`
* **runtime.Dockerfile** — Builds a KrakenD image (`krakend:2.10.1`) that embeds the plugin.
* **.github/workflows/krakend-plugin.yml** — CI that
  1. Compiles the plugin using `krakend/builder:2.10.1`
  2. Builds and publishes the runtime image to Docker Hub on GitHub Releases.

## Secrets
| Name | Purpose |
|------|---------|
| `DOCKERHUB_USERNAME` | Your Docker Hub username |
| `DOCKERHUB_TOKEN`    | A Docker Hub access token with repo write permission |

## Local build & trace
```bash
# Compile plugin
docker run --rm -v $PWD:/src -w /src/plugin krakend/builder:2.10.1   go build -trimpath -buildmode=plugin -o /src/.dist/trace-plugin.so

# Build runtime image
docker build -f runtime.Dockerfile --build-arg KRKN_VERSION=2.10.1 -t krakend-trace-plugin:local .

# Run
docker run -p 8080:8080 krakend-trace-plugin:local
```

# Configure in the krakend config file
```
"extra_config": {
  "plugin/http-client": {
    "name": "krakend-trace-plugin",
    "krakend-trace-plugin": {
      "tracking_url":   "http://tracking.svc/api/tracking",
      "timeout_ms":     2000,      // optional
      "max_capture_kb": 256,       // optional
      "verbose":        false      // optional (default)
    }
  }
}
```