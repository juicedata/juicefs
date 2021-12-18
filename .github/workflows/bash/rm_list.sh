#!/bin/bash
IFS=$'\n\n'

LIST=`cat $1`


for LINE in $LIST; do
      sudo sed -i "s/^$LINE.*//g" $2
done

