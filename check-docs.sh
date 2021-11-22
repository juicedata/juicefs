#!/bin/bash

set -e

CHANGED_FILES=`git diff --name-only $TRAVIS_COMMIT_RANGE`
echo $CHANGED_FILES
DOCS_DIR="docs/"
SKIP_FLAG=true

echo "TRAVIS_COMMIT_RANGE"
echo $TRAVIS_COMMIT_RANGE
echo "HEAD~1"
git diff --name-only HEAD~1
echo "TRAVIS_COMMIT_RANGE"
git diff --name-only $TRAVIS_COMMIT_RANGE

function changeFlag() {
  for CHANGED_FILE in $1; do
    if ! [[ $CHANGED_FILE =~ $DOCS_DIR ]] ; then
      SKIP_FLAG=false
      break
    fi
  done
}


#if TRAVIS_COMMIT_RANGE is empty
if [ x"${TRAVIS_COMMIT_RANGE}" == x ] ; then
  CHANGED_FILES=`git diff --name-only HEAD~1`
fi

changeFlag CHANGED_FILES


echo "skip flag"
echo $SKIP_FLAG
SKIP_FLAG=true
if [[ $SKIP_FLAG == true ]]; then
  TRAVIS=false
fi

echo "TRAVIS"
echo $TRAVIS