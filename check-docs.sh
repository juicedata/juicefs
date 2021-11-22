#!/bin/bash

set -e

echo "travis branch"
echo $TRAVIS_BRANCH
CHANGED_FILES=`git diff --name-only main...${TRAVIS_BRANCH}`
echo $CHANGED_FILES
DOCS_DIR="docs/.*"
SKIP_FLAG=true



for CHANGED_FILE in $CHANGED_FILES; do
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
    SKIP_FLAG=false
    break
  fi
done

if [[ $SKIP_FLAG == true ]]; then
  TRAVIS=false
fi