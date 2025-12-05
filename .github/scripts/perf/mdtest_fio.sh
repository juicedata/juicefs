#!/bin/bash
set -e

MNT_POINT=$1
RESULTS_FILE=$2
VERSION=$3

mkdir -p "$(dirname "$RESULTS_FILE")"

process_run() {
    local output=$1
    local scenario=$2
    local attempt=$3

    # For built-in mdtest (scenario3) and fio tests, we just capture the output
    if [[ "$scenario" == "scenario3" || "$scenario" =~ ^fio ]]; then
        cp "$output" "${output}.summary"
        return
    fi

    grep -A 100 "SUMMARY rate:" "$output" | \
    grep -v "SUMMARY rate:" | \
    grep -v "\-\-\-" | \
    grep -v "Command line used:" | \
    grep -v "Path:" | \
    grep -v "FS:" | \
    grep -v "Nodemap:" | \
    grep -v "tasks," | \
    awk 'NF' > "${output}.tmp"

    # Convert to CSV format for easier processing
    awk '{
        # Skip lines that don'\''t contain operation metrics
        if ($0 ~ /:/ && $0 !~ /^-+$/) {
            op="";
            for(i=1;i<=NF;i++) {
                if ($i == ":") {
                    # Join all words before ":" as operation name
                    for(j=1;j<i;j++) op=op (j>1?" ":"") $j;
                    # Extract metrics
                    max=$(i+1); min=$(i+2); mean=$(i+3); stddev=$(i+4);
                    print op "," max "," min "," mean "," stddev;
                    break;
                }
            }
        }
    }' "${output}.tmp" > "${output}.csv"

    rm -f "${output}.tmp"
}

calculate_averages() {
    local scenario=$1
    local runs=$2

    # Skip averaging for built-in mdtest (scenario3) and fio tests
    if [[ "$scenario" == "scenario3" || "$scenario" =~ ^fio ]]; then
        return
    fi

    declare -A ops max_sum min_sum mean_sum stddev_sum count
    declare -a op_order  # To maintain operation order

    for ((i=1; i<=runs; i++)); do
        while IFS=, read -r op max min mean stddev; do
            # Skip empty lines
            [ -z "$op" ] && continue

            # Add to op_order if not already present
            if [[ -z "${ops[$op]}" ]]; then
                ops["$op"]=1
                op_order+=("$op")
            fi

            max=$(echo "$max" | tr -d ',')
            min=$(echo "$min" | tr -d ',')
            mean=$(echo "$mean" | tr -d ',')
            stddev=$(echo "$stddev" | tr -d ',')

            max_sum["$op"]=$(echo "${max_sum[$op]:-0} + $max" | bc -l)
            min_sum["$op"]=$(echo "${min_sum[$op]:-0} + $min" | bc -l)
            mean_sum["$op"]=$(echo "${mean_sum[$op]:-0} + $mean" | bc -l)
            stddev_sum["$op"]=$(echo "${stddev_sum[$op]:-0} + $stddev" | bc -l)
            count["$op"]=$(( ${count[$op]:-0} + 1 ))
        done < "${RESULTS_FILE}.${scenario}.run${i}.csv"
    done

    > "${RESULTS_FILE}.${scenario}.summary"  # Clear the file
    for op in "${op_order[@]}"; do
        cnt=${count[$op]:-1}  # Avoid division by zero
        avg_max=$(echo "scale=2; ${max_sum[$op]:-0} / $cnt" | bc -l)
        avg_min=$(echo "scale=2; ${min_sum[$op]:-0} / $cnt" | bc -l)
        avg_mean=$(echo "scale=2; ${mean_sum[$op]:-0} / $cnt" | bc -l)
        avg_stddev=$(echo "scale=2; ${stddev_sum[$op]:-0} / $cnt" | bc -l)

        printf "%-25s : %12.2f %12.2f %12.2f %12.2f\n" \
               "$op" "$avg_max" "$avg_min" "$avg_mean" "$avg_stddev" \
               >> "${RESULTS_FILE}.${scenario}.summary"
    done
}

# Scenario 1: -b 3 -z 1 -I 1000
for i in {1..3}; do
    echo "Running scenario 1 (attempt $i)..."
    output_file="${RESULTS_FILE}.scenario1.run${i}"
    echo 3 | sudo tee /proc/sys/vm/drop_caches
    mpirun --use-hwthread-cpus --allow-run-as-root -np 4 mdtest -b 3 -z 1 -I 300 -d "$MNT_POINT/mdtest" | tee "$output_file"
    process_run "$output_file" "scenario1" $i
    rm -rf "$MNT_POINT/mdtest"/*
done

# Scenario 2: -F -w 102400 -I 1000 -z 0
for i in {1..3}; do
    echo "Running scenario 2 (attempt $i)..."
    output_file="${RESULTS_FILE}.scenario2.run${i}"
    echo 3 | sudo tee /proc/sys/vm/drop_caches
    mpirun --use-hwthread-cpus --allow-run-as-root -np 4 mdtest -F -w 102400 -I 2000 -z 0 -d "$MNT_POINT/mdtest" | tee "$output_file"
    process_run "$output_file" "scenario2" $i
    rm -rf "$MNT_POINT/mdtest"/*
done

# Scenario 3: JuiceFS built-in mdtest (run only once)
echo "Running scenario 3 (built-in mdtest)..."
output_file="${RESULTS_FILE}.scenario3.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
{ time sudo ./juicefs mdtest "$MNT_POINT" --threads 10 --dirs 3 --depth 3 --files 100 --create; } 2>&1 | tee "$output_file"
process_run "$output_file" "scenario3" 1

# Fio Scenario 4: Concurrent sequential write of 1 big file per thread (16 threads)
echo "Running fio scenario 4..."
output_file="${RESULTS_FILE}.fio_scenario4.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
mkdir -p "$MNT_POINT/fio"

fio --name=big-write --filename="${MNT_POINT}/fio/fio_test_$(date +%Y%m%d_%H%M%S).dat" --group_reporting \
    --rw=write --direct=1 --bs=64k --end_fsync=1 --runtime=200 \
    --numjobs=8 --nrfiles=1 --size=1G --output-format=normal | tee "$output_file"
process_run "$output_file" "fio_scenario4" 1
rm -rf "$MNT_POINT/fio"/*

# Fio Scenario 5: Concurrent sequential write of multiple big files (16 threads, 64 files each)
echo "Running fio scenario 5..."
output_file="${RESULTS_FILE}.fio_scenario5.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
mkdir -p "$MNT_POINT/fio"
fio --name=big-write --filename="${MNT_POINT}/fio/fio_test_$(date +%Y%m%d_%H%M%S).dat" --group_reporting \
    --rw=randwrite --direct=1 --bs=64k --end_fsync=1 --runtime=200 \
    --numjobs=8 --nrfiles=1 --size=1G --output-format=normal | tee "$output_file"
process_run "$output_file" "fio_scenario5" 1
rm -rf "$MNT_POINT/fio"/*

# Fio Scenario 6: Sequential read of multiple big files (single thread)
echo "Running fio scenario 6..."
output_file="${RESULTS_FILE}.fio_scenario6.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
mkdir -p "$MNT_POINT/fio"
fio --name=big-read-multiple --filename="${MNT_POINT}/fio/fio_test_$(date +%Y%m%d_%H%M%S).dat" --group_reporting --runtime=300 \
    --rw=read --direct=1 --bs=4k --numjobs=8 --nrfiles=1 --size=1G --output-format=normal | tee "$output_file"
process_run "$output_file" "fio_scenario6" 1
rm -rf "$MNT_POINT/fio"/*

# Fio Scenario 7: Concurrent sequential read of multiple big files (64 threads)
echo "Running fio scenario 7..."
output_file="${RESULTS_FILE}.fio_scenario7.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
mkdir -p "$MNT_POINT/fio"
fio --name=big-read-multiple-concurrent --filename="${MNT_POINT}/fio/fio_test_$(date +%Y%m%d_%H%M%S).dat" --group_reporting \
    --rw=randread --direct=1 --bs=4k --numjobs=8 --nrfiles=1 --openfiles=1 --size=1G --output-format=normal --runtime=120 | tee "$output_file"
process_run "$output_file" "fio_scenario7" 1
rm -rf "$MNT_POINT/fio"/*

# Fio Scenario 8: Concurrent sequential write of multiple big files (8 threads, 8 files each)
echo "Running fio scenario 8..."
output_file="${RESULTS_FILE}.fio_scenario8.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
mkdir -p "$MNT_POINT/fio"
fio --name=big-write --directory="$MNT_POINT/fio" --group_reporting \
    --rw=write --direct=1 --bs=1m --end_fsync=1 --runtime=120 \
    --numjobs=8 --nrfiles=8 --size=1G --output-format=normal | tee "$output_file"
process_run "$output_file" "fio_scenario8" 1
rm -rf "$MNT_POINT/fio"/*


# Fio Scenario 9: Concurrent sequential read of multiple big files (8 threads)
echo "Running fio scenario 9..."
output_file="${RESULTS_FILE}.fio_scenario9.run1"
echo 3 | sudo tee /proc/sys/vm/drop_caches
mkdir -p "$MNT_POINT/fio"
fio --name=big-read-multiple-concurrent --directory="$MNT_POINT/fio" --group_reporting \
    --rw=read --direct=1 --bs=1m --numjobs=8 --nrfiles=8 --openfiles=1 --size=1G --output-format=normal --runtime=120 | tee "$output_file"
process_run "$output_file" "fio_scenario9" 1
rm -rf "$MNT_POINT/fio"/*



# Calculate averages for scenario1 and scenario2
calculate_averages "scenario1" 3
calculate_averages "scenario2" 3

# For scenario3 and fio scenarios, just rename the single run file to .summary
mv "${RESULTS_FILE}.scenario3.run1.summary" "${RESULTS_FILE}.scenario3.summary"
for scenario in fio_scenario4 fio_scenario5 fio_scenario6 fio_scenario7 fio_scenario8 fio_scenario9; do
    mv "${RESULTS_FILE}.${scenario}.run1.summary" "${RESULTS_FILE}.${scenario}.summary"
done

rm -f "${RESULTS_FILE}"*.run*.csv "${RESULTS_FILE}"*.run[1-3]

# Print summary results
echo ""
echo "Summary Results for $VERSION:"
for scenario in scenario1 scenario2; do
    echo ""
    echo "$scenario Results:"
    printf "%-25s %-12s %-12s %-12s %-12s\n" "Operation" "Max" "Min" "Mean" "Std Dev"
    cat "${RESULTS_FILE}.${scenario}.summary"
done

# Print built-in mdtest results
echo ""
echo "Scenario3 (Built-in mdtest) Results:"
cat "${RESULTS_FILE}.scenario3.summary"

# Print fio results
for scenario in fio_scenario4 fio_scenario5 fio_scenario6 fio_scenario7 fio_scenario8 fio_scenario9; do
    echo ""
    echo "${scenario} Results:"
    cat "${RESULTS_FILE}.${scenario}.summary"
done
