# URL Manager

## Usage

Example command: `./url-manager --route-file example/routes.json --metrics "/metrics"`

Example route file: [routes.json](example/exaple-routes.json)

Arguments:

| Argument | Default | Description |
| - | - | - |
| `--help` | | Show usage. |
| `--debug` | | Show extra debug messages. |
| `--endpoint` | `:8080` | The address-port endpoint to bind to. |
| `--route-file` | `routes.json` | The path to the routes JSON config file. |
| `--metrics` | `""` | Metrics endpoint. Disabled if not set. |

## TODO

- Docker image.
- Accept reverse proxy headers.
