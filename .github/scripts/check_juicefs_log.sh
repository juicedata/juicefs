#!/bin/bash -e
for log_file in /var/log/juicefs.log $HOME/.juicefs/juicefs.log; do
    if [ -f $log_file ]; then
        break
    fi
done
echo "tail -1000 $log_file"
tail -1000 $log_file
grep -i "<FATAL>\|panic" $log_file && exit 1 || true