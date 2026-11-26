# Contributing

## Requirements

- Go 1.22 or newer
- Zero external dependencies in `go.mod` (standard library only)

## Local Checks

Run formatting, static analysis, and race-enabled unit tests before submitting changes:

```sh
gofmt -l .
go vet ./...
go test -race -v ./...
```

All HTTP calls in tests must target local `httptest.Server` instances rather than live registries or GitHub.
