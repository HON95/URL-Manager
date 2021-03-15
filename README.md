# URL Manager

[![GitHub release](https://img.shields.io/github/v/release/HON95/URL-Manager?label=Version)](https://github.com/HON95/URL-Manager/releases)
[![CI](https://github.com/HON95/URL-Manager/workflows/CI/badge.svg?branch=master)](https://github.com/HON95/URL-Manager/actions?query=workflow%3ACI)
[![FOSSA status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FHON95%2FURL-Manager.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2FHON95%2FURL-Manager?ref=badge_shield)
[![Docker pulls](https://img.shields.io/docker/pulls/hon95/url-manager?label=Docker%20Hub)](https://hub.docker.com/r/hon95/url-manager)

Redirects URLs based on routes declared in a JSON file.

## Usage

CLI arguments:

| Argument | Default | Description |
| - | - | - |
| `--help` | | Show usage. |
| `--debug` | | Show extra debug messages. |
| `--endpoint` | `:8080` | The address-port endpoint to bind to. |
| `--route-file` | `routes.json` | The path to the routes JSON config file. |
| `--metrics` | `""` | Metrics endpoint. Disabled if not set. Should be blocked by the upstream reverse proxy to avoid leaking it. |

Route fields:

| Field | Default | Description |
| - | - | - |
| `id` | (required) | A unique ID for the route. May contain only alphanumeric characters, hyphens and underscores. |
| `source_url` | (required) | A regex pattern to match the source URL against. It should contain the full regex-escaped URL, including the beginning and end anchors (`^` and `$`), to prevent ambiguity. |
| `destination_url` | (required) | The URL to redirect to. It may reference capture groups (`()`) from the source URL pattern as `$1`, `$2` etc. to create dynamic routes. |
| `priority` | `0` | If multiple routes match the source URL, the one with the highest priority is chosen. |
| `redirect_status` | `302` | The HTTP redirect status code to use. |

Example route file: [routes.json](dev/routes.json)

### Docker

See the dev/example Docker Compose file: [docker-compose.yml](dev/docker-compose.yml)

## TODO

- CI and Docker image.
- Metrics on separate port to avoid upstream filtering.
- Simple unit tests.
