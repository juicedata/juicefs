#!/bin/bash

set -e

CHANGED_FILES=`git diff --name-only main...${TRAVIS_COMMIT}`
echo $CHANGED_FILES
SKIP_FLAG=True
DOC_MD=".md"
PIC_JPG=".jpg"
PIC_png=".png"
PIC_SVG=".svg"

for CHANGED_FILE in $CHANGED_FILES; do
  if ![[ $CHANGED_FILE =~ $DOC_DIR ]] && ![[ $CHANGED_FILE =~ $PIC_JPG ]]  &&  \
    ![[ $CHANGED_FILE =~ $PIC_PNG ]] && ![[ $CHANGED_FILE =~ $PIC_SVG ]] ; then
    SKIP_FLAG=False
    break
  fi
done

if [[ $SKIP_FLAG == True ]]; then
  echo "Only doc files modified, exiting."
  travis_terminate 0
  exit 1
fi