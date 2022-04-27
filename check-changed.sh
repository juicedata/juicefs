#!/bin/bash

set -e

if [ x"${TRAVIS_COMMIT_RANGE}" == x ] ; then
  CHANGED_FILES=`git diff --name-only HEAD~1`
else
  CHANGED_FILES=`git diff --name-only $TRAVIS_COMMIT_RANGE`
fi
echo $CHANGED_FILES
DOCS_DIR="docs/"
GITHUB_DIR=".github/"
SKIP_TEST=true

for CHANGED_FILE in $CHANGED_FILES; do
  if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] && ! [[ $CHANGED_FILE =~ $GITHUB_DIR ]] ; then
    SKIP_TEST=false
    break
  fi
done