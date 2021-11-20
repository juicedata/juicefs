#!/bin/bash

set -e

CHANGED_FILES=`git diff --name-only main...${TRAVIS_COMMIT}`
echo $CHANGED_FILES
SKIP_FLAG=True
DOCS_DIR="docs/.*"

for CHANGED_FILE in $CHANGED_FILES; do
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
    SKIP_FLAG=False
    break
  fi
done

if [[ $SKIP_FLAG == True ]]; then
  echo "Only doc files modified, exiting."
  travis_terminate 0
  exit 1
fi

echo "test travis_terminate"
travis_terminate 0
exit 1