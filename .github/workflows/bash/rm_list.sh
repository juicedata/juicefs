#!/bin/bash
LIST=`cat $1`

for LINE in $LIST; do
      # should remove empty line and comment line
      sed -i -e "\!^${LINE}.*!d" -e "\!^#!d" -e "\!^\s*\$!d" $2
done

