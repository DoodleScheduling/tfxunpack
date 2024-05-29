# Unpack terraform manifests from crossplane terraform workspaces
[![release](https://github.com/doodlescheduling/tfxunpack/actions/workflows/release.yaml/badge.svg)](https://github.com/doodlescheduling/tfxunpack/actions/workflows/release.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/doodlescheduling/tfxunpack)](https://goreportcard.com/report/github.com/doodlescheduling/tfxunpack)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/tfxunpack/badge)](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/tfxunpack)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/tfxunpack/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/tfxunpack?branch=master)

This small utility extracts terraform workspaces managed by the [crossplane terraform provider](https://github.com/upbound/provider-terraform).

Crossplane terraform workspaces are reconciled only at runtime or aka by the cluster. 
However it is good practice to validate things beforehand. This utility extracts terraform resource from crossplane managed workspaces
which makes it possible to run things like `terraform fmt` and `terraform validate` beforehand.

## Installation

### Brew
```
brew tap doodlescheduling/tfxunpack
brew install tfxunpack
```

### Docker
```
docker pull ghcr.io/doodlescheduling/tfxunpack:v0
```

## Arguments

| Flag           | Short        | Env            | Default      | Description   |
| ------------- | ------------- | ------------- | ------------- | ------------- |
| `--file`  | `-f`  | `FILE` | `/dev/stdin` | Path to input |
| `--out`  | `-o`  | `OUTPUT` | `/` | Path to output directory. If not set it will create a folder called tfmodule in the current directory. |
| `--workers`  | ``  | `WORKERS`  | `Number of CPU cores` | Number of workers to process the manifest |
| `--fail-fast`  | ``  | `FAIL_FAST` | `false` | Exit early if an error occurred |
| `--allow-failure`  | ``  | `ALLOW_FAILURE` | `false` | Do not exit > 0 if an error occurred |

## Github Action

This app works also great on CI, in fact this was the original reason why it was created.

### Example usage

```yaml
name: tfxunpack
on:
- pull_request

jobs:
  build:
    strategy:
      matrix:
        cluster: [staging, production]

    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@24cb9080177205b6e8c946b17badbe402adc938f # v3.4.0
    - run: 
        kustomize build ${{ matricx.cluster }} > out.yaml
    - uses: docker://ghcr.io/doodlescheduling/tfxunpack:v0
      env:
        FILE: out.yaml 
    - uses: hashicorp/setup-terraform@v3
    - name: Install terraform dependencies
      run: |
        cd tfmodule
        terraform validate
    - name: Validate terraform
      run: |
        terraform validate
```
