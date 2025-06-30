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
    mpirun --use-hwthread-cpus --allow-run-as-root -np 4 mdtest -F -w 102400 -I 3000 -z 0 -d "$MNT_POINT/mdtest" | tee "$output_file"
    process_run "$output_file" "scenario2" $i
    rm -rf "$MNT_POINT/mdtest"/*
done

# Calculate averages for both scenarios
calculate_averages "scenario1" 3
calculate_averages "scenario2" 3
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
