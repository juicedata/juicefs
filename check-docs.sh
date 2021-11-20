#!/bin/bash

set -e

CHANGED_FILES=`git diff --name-only main...${TRAVIS_COMMIT}`
echo $CHANGED_FILES
DOCS_DIR="docs/.*"
SKIP_FLAG=True
echo "before CI"
echo $CI

for CHANGED_FILE in $CHANGED_FILES; do
  echo "change files"
  echo $CHANGED_FILE
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
    SKIP_FLAG=False
    break
  fi
done

if [[ $SKIP_FLAG == True ]]; then
  CI=false
fi

CI=false
echo "after CI"
echo ${CI}