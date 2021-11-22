#!/bin/bash

set -e

echo "travis branch"
echo $TRAVIS_BRANCH
CHANGED_FILES=`git diff --name-only main...${TRAVIS_BRANCH}`
echo $CHANGED_FILES
DOCS_DIR="docs/"
SKIP_FLAG=true

echo "TRAVIS_COMMIT_RANGE"
echo $TRAVIS_COMMIT_RANGE
echo "HEAD~1"
git diff --name-only HEAD~1
echo "TRAVIS_COMMIT_RANGE"
git diff --name-only $TRAVIS_COMMIT_RANGE


for CHANGED_FILE in $CHANGED_FILES; do
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
    SKIP_FLAG=false
    break
  fi
done

echo "skip flag"
echo $SKIP_FLAG
if [[ $SKIP_FLAG == true ]]; then
  TRAVIS=false
fi

echo "TRAVIS"
echo $TRAVIS