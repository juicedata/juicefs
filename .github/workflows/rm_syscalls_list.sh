#!/bin/bash

LIST="cat $1"

echo $LIST
echo "before modified"
cat $2
for LINE in $LIST; do
      sudo sed -i "s/$LINE//g" $2
done

echo "after modified"
cat $2

