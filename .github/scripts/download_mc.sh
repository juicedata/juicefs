#!/bin/bash -e

# Release assets include the version in their names; resolve latest at download time.
platform=$1
dest=$2
release_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/minio/mc/releases/latest)
version=${release_url##*/}
if [[ "$version" != RELEASE.* ]]; then
    echo "Unable to resolve the latest mc release: $release_url" >&2
    exit 1
fi
curl -fsSL "https://github.com/minio/mc/releases/download/$version/mc.$platform.$version" -o "$dest"
