#!/bin/bash

set -e

echo "commit range"
echo "$TRAVIS_COMMIT_RANGE"
if [ x"${TRAVIS_COMMIT_RANGE}" == x ] ; then
  echo "11111"
  CHANGED_FILES=`git diff --name-only HEAD~1`
else
  echo "22222"
  CHANGED_FILES=`git diff --name-only $TRAVIS_COMMIT_RANGE`
fi
echo $CHANGED_FILES
DOCS_DIR="docs/"
SKIP_TEST=true

for CHANGED_FILE in $CHANGED_FILES; do
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
    SKIP_TEST=false
    break
  fi
done