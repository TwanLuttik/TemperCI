# Module path and license

## Decision

| Item | Choice |
|------|--------|
| Go module path | `github.com/TwanLuttik/TemperCI` |
| SPDX license | Apache-2.0 |

## Rationale

- **Module path** matches the public GitHub repository (`TwanLuttik/TemperCI`). If the repo path changes before first release, update `go.mod` and import paths in one change.
- **Apache-2.0** is a common choice for infrastructure open source (patent grant + clear contribution terms). Full text is in the root `LICENSE` file.

## Date

2026-08-12
