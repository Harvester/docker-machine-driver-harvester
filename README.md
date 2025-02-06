docker-machine-driver-harvester
========
[![Build Status](https://github.com/harvester/docker-machine-driver-harvester/actions/workflows/release.yml/badge.svg)](https://github.com/harvester/docker-machine-driver-harvester/actions)

The [Harvester](https://github.com/harvester/harvester) machine driver for Docker.

## Branches

- `master` branch is used for development and release.
- `v0.7.x` branch is used for Rancher versions 2.10.x and below.

## Development

### Building
```bash
make
```

The binary is placed in the `bin` directory.

The compressed binary is placed in the `dist/artifacts` directory.


## Usage

Put the binary to your $PATH directory

```bash
docker-machine create --driver harvester
```
