#!/bin/bash

REMOTE_REPO=https://github.com/juicedata/juicefs.git
VERSION_FILE="cmd/version.go"

function check_version() {
    # Use remote repo as the git-fetch in CI may use --no-tags option
    local latest_taginfo="$(git ls-remote --tags $REMOTE_REPO 'refs/tags/v*' | tail -n 1)"
    local tagged_commit=$(echo "$latest_taginfo" | awk '{print $1}')
    local tagged_version=$(echo "$latest_taginfo" | awk '{print $2}' | sed 's@^refs/tags/v@@')
    if [ -z "$tagged_version" ]; then
        echo "Unable to find version tag, skip check"
        exit 0
    fi

    if ! grep "version *=" $VERSION_FILE | grep "$tagged_version"; then
        # Don't complain if HEAD is a release tag, which can be added on github web release page
        local latest_commit=$(git rev-parse HEAD)
        if [ "$latest_commit" != "$tagged_commit" ]; then
            echo "Check version failed: version value in $VERSION_FILE is not the same as latest version tag ($tagged_version)"
            exit 1
        fi
    fi
}

check_version
