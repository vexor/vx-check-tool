#!/bin/bash

set -x
set -e

semver inc patch
git ci -am "Bump $(semver tag)"
git tag -a $(semver tag) -m $(semver tag)
git push --tags
