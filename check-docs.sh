#!/bin/bash

set -e

CHANGED_FILES=`git diff main...${TRAVIS_COMMIT}`
echo $CHANGED_FILES
ONLY_DOCS=True
DOC_DIR="docs/.*"

for CHANGED_FILE in $CHANGED_FILES; do
  if ! [[ $CHANGED_FILE =~ $DOC_DIR ]]; then
    ONLY_DOCS=False
    break
  fi
done

if [[ $ONLY_DOCS == True ]]; then
  echo "Only doc files modified, exiting."
  travis_terminate 0
  exit 1
fi