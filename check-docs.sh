#!/bin/bash

set -e

echo "TRAVIS_COMMIT"
echo $TRAVIS_COMMIT
CHANGED_FILES=`git diff --name-only main...${TRAVIS_COMMIT}`
echo $CHANGED_FILES
DOCS_DIR="docs/.*"
SKIP_FLAG=True
echo "before CI"
echo $TRAVIS
echo "env list"
env

for CHANGED_FILE in $CHANGED_FILES; do
  echo "change files"
  echo $CHANGED_FILE
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
    SKIP_FLAG=False
    break
  fi
done

if [[ $SKIP_FLAG == True ]]; then
  TRAVIS=false
fi




TRAVIS=true
echo "after CI"
echo $TRAVIS