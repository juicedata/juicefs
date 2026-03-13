#!/bin/bash -e
[[ -z $META ]] && META=redis

prepare0() {
    target=$1
    if [[ "$target" == "BASELINE" ]]; then
        cp -f juicefs.old juicefs
    else
        cp -f juicefs.new juicefs
    fi
    source .github/scripts/start_meta_engine.sh
    meta_url=$(get_meta_url $META)
    python3 .github/scripts/flush_meta.py $meta_url
    rm -rf /var/jfs/myjfs/
    start_meta_engine $META
    create_database $meta_url
    ./juicefs format $meta_url --trash-days 1 myjfs
    ./juicefs mount -d $meta_url /tmp/jfs --no-usage-report
}

cleanup() {
    echo "cleanup" >&2
    # meta_url=$(get_meta_url $META)
    # python3 .github/scripts/flush_meta.py $meta_url
    # rm -rf /var/jfs/myjfs/
}

parse_real_time() {
    # Extract real time in seconds from: real 0m1.234s
    grep "^real" | head -1 | awk '{
        time_str = $2
        gsub(/s$/, "", time_str)
        split(time_str, parts, "m")
        printf "%.3f\n", parts[1] * 60 + parts[2]
    }'
}

parse_fio_iops(){
    # Extract IOPS value from fio output (e.g., IOPS=12.3k)
    grep -oP 'IOPS=\K[\d.]+[kMG]?' | head -1 | awk '{
        val=$1
        if (val ~ /k$/) { sub(/k$/, "", val); val=val*1000 }
        else if (val ~ /M$/) { sub(/M$/, "", val); val=val*1000000 }
        else if (val ~ /G$/) { sub(/G$/, "", val); val=val*1000000000 }
        printf "%.0f\n", val
    }'
}

parse_mpirun_ops() {
    # Extract max ops/sec for non-filtered operations from SUMMARY and average them
    # Filtered (skipped): File read, File removal, Tree removal, Tree creation
    awk '
    /SUMMARY rate:/ { in_summary=1; next }
    in_summary && /^-+/ { next }
    in_summary && /Operation/ { next }
    in_summary && /File read|File removal|Tree removal|Tree creation/ { next }
    in_summary && /:/ {
        for(i=1; i<=NF; i++) {
            if ($i == ":") {
                max = $(i+1)
                gsub(/,/, "", max)
                if (max+0 == max && max > 0) {
                    sum += max
                    count++
                }
                break
            }
        }
    }
    END { if (count > 0) printf "%.2f\n", sum/count; else print "0" }
    '
}