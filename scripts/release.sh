#!/bin/bash

set -e
set -x

github-release release -u vexor -r vx-systemd-check --tag $(semver tag)
github-release upload -u vexor -r vx-systemd-check --tag $(semver tag) --file vx-systemd-check --name "vx-systemd-check"
