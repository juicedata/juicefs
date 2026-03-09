#!/bin/bash
set -e

CURRENT_RESULTS=$1
OLD_RESULTS=$2
FILTER_OPS=("File read" "File stat" "File removal" "Tree removal" "Tree creation")

# Function to extract files/s from built-in mdtest output
extract_files_per_sec() {
    local mdtest_output=$1
    local files_per_sec=$(grep -oP 'Created .*files.*\(\K[0-9]+(\.[0-9]+)?(?= files/s\))' <<< "$mdtest_output" | head -1)
    if [[ -z "$files_per_sec" ]]; then
        files_per_sec=$(grep -oP '\(\K[0-9]+(\.[0-9]+)?(?= files/s\))' <<< "$mdtest_output" | head -1)
    fi
    if [[ -z "$files_per_sec" ]]; then
        echo "0"
    else
        echo "$files_per_sec"
    fi
}

# Function to extract IOPS from fio output
extract_iops() {
    local fio_output=$1
    local iops=$(grep -oP 'IOPS=\K[\d.]+[kMG]?' <<< "$fio_output" | head -1)
    # Convert to numeric value (handle k/M/G suffixes)
    if [[ "$iops" == *k ]]; then
        echo "${iops%k} * 1000" | bc -l
    elif [[ "$iops" == *M ]]; then
        echo "${iops%M} * 1000000" | bc -l
    elif [[ "$iops" == *G ]]; then
        echo "${iops%G} * 1000000000" | bc -l
    else
        echo "$iops"
    fi
}

extract_bw() {
    local fio_output=$1
    local bw=$(grep -oP 'BW=\K[^, ]+' <<< "$fio_output" | head -1)
    if [[ -z "$bw" ]]; then
        echo "N/A"
    else
        echo "$bw"
    fi
}

extract_metrics() {
    awk '{
        op_description=$1;
        op_type=$2;
        for(i=3;i<=NF;i++) if($i == ":") break;
        max=$(i+1); min=$(i+2); mean=$(i+3); stddev=$(i+4);
        print op_description, op_type, max, min, mean, stddev
    }' <<< "$1"
}

is_op_in_filter() {
    local op="$1"
    for allowed_op in "${FILTER_OPS[@]}"; do
        if [[ "$op" == "$allowed_op" ]]; then
            return 0
        fi
    done
    return 1
}

compare_with_tolerance() {
    local current=$1
    local old=$2
    local op_type=$3
    local direction=${4:-higher}
    tolerance=$(echo "$old * 0.2" | bc -l)
    lower_bound=$(echo "$old - $tolerance" | bc -l)
    upper_bound=$(echo "$old + $tolerance" | bc -l)

    # For time comparison, lower is better
    if is_op_in_filter "$op_type"; then
        echo "skip"
    elif (( $(echo "$current <= $upper_bound && $current >= $lower_bound" | bc -l) )); then
        echo "same"
    else
        if [[ "$direction" == "lower" ]]; then
            if (( $(echo "$current < $old" | bc -l) )); then
                echo "better"
            else
                echo "worse"
            fi
        else
            if (( $(echo "$current > $old" | bc -l) )); then
                echo "better"
            else
                echo "worse"
            fi
        fi
    fi
}

compare_scenario() {
    local scenario=$1
    local current_file="${CURRENT_RESULTS}.${scenario}.summary"
    local old_file="${OLD_RESULTS}.${scenario}.summary"

    echo ""
    echo "===================================================================="
    echo "Detailed Comparison for $scenario (with 20% tolerance)"
    case "$scenario" in
        "scenario1")
            echo "Command is : mpirun --use-hwthread-cpus --allow-run-as-root -np 4 mdtest -b 3 -z 1 -I 300"
            ;;
        "scenario2")
            echo "Command is : mpirun --use-hwthread-cpus --allow-run-as-root -np 4 mdtest -F -w 102400 -I 3000 -z 0"
            ;;
        "scenario3")
            echo "Command is : ./juicefs mdtest <meta-url> /mdtest_perf --threads 10 --dirs 3 --depth 3 --files 100"
            ;;
        "fio_scenario4")
            echo "Command is : fio --name=big-write --directory=/mnt/fio --group_reporting --rw=write --direct=1 --bs=64k --end_fsync=1 --numjobs=8 --nrfiles=1 --size=2G --runtime=120"
            ;;
        "fio_scenario5")
            echo "Command is : fio --name=big-write  --group_reporting --rw=randwrite --direct=1 --bs=64k --end_fsync=1 --runtime=200 --numjobs=8 --nrfiles=1 --size=2G"
            ;;
        "fio_scenario6")
            echo "Command is : fio --name=big-read-multiple  --group_reporting --runtime=300 --rw=read --direct=1 --bs=4k --numjobs=8 --nrfiles=1 --size=2G"
            ;;
        "fio_scenario7")
            echo "Command is : fio --name=big-read-multiple-concurrent  --group_reporting --rw=randread --direct=1 --bs=4k --numjobs=8 --nrfiles=1 --openfiles=1 --size=2G --output-format=normal --runtime=120"
            ;;
        "fio_scenario8")
            echo "fio --name=big-write --directory="$MNT_POINT/fio" --group_reporting \
    --rw=write --direct=1 --bs=1m --end_fsync=1 --runtime=120 \
    --numjobs=8 --nrfiles=8 --size=2G"
            ;;
        "fio_scenario9")
            echo "Command is : fio --name=big-read-multiple-concurrent --directory="$MNT_POINT/fio" --group_reporting \
    --rw=read --direct=1 --bs=1m --numjobs=8 --nrfiles=8 --openfiles=1 --size=2G --output-format=normal --runtime=120"
            ;;
    esac
    echo "===================================================================="

    # Handle built-in mdtest scenario (scenario3)
    if [[ "$scenario" == "scenario3" ]]; then
        printf "%-30s %-12s %-12s %-12s %-12s %-12s\n" "Operation" "Current files/s" "Old files/s" "Diff" "Status" "Variance"
        echo "--------------------------------------------------------------------"

        current_files_per_sec=$(extract_files_per_sec "$(cat "${current_file}")")
        old_files_per_sec=$(extract_files_per_sec "$(cat "${old_file}")")

        diff=$(echo "$current_files_per_sec - $old_files_per_sec" | bc -l)
        if (( $(echo "$old_files_per_sec == 0" | bc -l) )); then
            variance="N/A"
            comparison="same"
        else
            variance=$(echo "scale=2; ($current_files_per_sec - $old_files_per_sec)*100/$old_files_per_sec" | bc -l)
            comparison=$(compare_with_tolerance $current_files_per_sec $old_files_per_sec "builtin_mdtest")
        fi

        case $comparison in
            "worse") status="❌ Worse" ;;
            "better") status="✅ Better" ;;
            "same") status="⚖️ Same" ;;
            "skip") status="⏭️ Skipped" ;;
            *) status="⚠️ Unknown" ;;
        esac

         if [[ "$variance" == "N/A" ]]; then
             printf "%-30s %-12.2f %-12.2f %-12.2f %-12s %-12s\n" \
                 "Built-in mdtest" "$current_files_per_sec" "$old_files_per_sec" "$diff" "$status" "$variance"
         else
             printf "%-30s %-12.2f %-12.2f %-12.2f %-12s %-12s%%\n" \
                 "Built-in mdtest" "$current_files_per_sec" "$old_files_per_sec" "$diff" "$status" "$variance"
         fi
    
    # Handle fio scenarios
    elif [[ "$scenario" =~ ^fio ]]; then
        printf "%-30s %-12s %-12s %-12s %-12s %-12s\n" "Operation" "Current IOPS" "Old IOPS" "Diff" "Status" "Variance"
        echo "--------------------------------------------------------------------"

        current_iops=$(extract_iops "$(cat "${current_file}")")
        old_iops=$(extract_iops "$(cat "${old_file}")")
        current_bw=$(extract_bw "$(cat "${current_file}")")
        old_bw=$(extract_bw "$(cat "${old_file}")")

        diff=$(echo "$current_iops - $old_iops" | bc -l)
        variance=$(echo "scale=2; ($current_iops - $old_iops)*100/$old_iops" | bc -l)
        comparison=$(compare_with_tolerance $current_iops $old_iops "fio_${scenario}")

        case $comparison in
            "worse") status="❌ Worse" ;;
            "better") status="✅ Better" ;;
            "same") status="⚖️ Same" ;;
            "skip") status="⏭️ Skipped" ;;
            *) status="⚠️ Unknown" ;;
        esac

        printf "%-30s %-12.2f %-12.2f %-12.2f %-12s %-12s%%\n" \
               "FIO ${scenario}" "$current_iops" "$old_iops" "$diff" "$status" "$variance"
        printf "%-30s %-12s %-12s\n" "Bandwidth" "$current_bw" "$old_bw"
    
    # Handle mdtest scenarios
    else
        printf "%-30s %-12s %-12s %-12s %-12s %-12s\n" "Operation" "Current Max" "Old Max" "Diff" "Status" "Variance"
        echo "--------------------------------------------------------------------"

        while IFS= read -r current_line && IFS= read -r old_line <&3; do
            if [ -z "$current_line" ] || [ -z "$old_line" ]; then
                continue
            fi

            current_metrics=($(extract_metrics "$current_line"))
            old_metrics=($(extract_metrics "$old_line"))

            current_op="${current_metrics[0]} ${current_metrics[1]}"
            old_op="${old_metrics[0]} ${old_metrics[1]}"

            if [ "$current_op" != "$old_op" ]; then
                echo "Warning: Operation mismatch ('$current_op' vs '$old_op'), skipping..."
                continue
            fi

            current_max=${current_metrics[2]}
            old_max=${old_metrics[2]}

            if [[ "$current_max" =~ ^[0-9.]+$ ]] && [[ "$old_max" =~ ^[0-9.]+$ ]]; then
                diff=$(echo "$current_max - $old_max" | bc -l)
                variance=$(echo "scale=2; ($current_max - $old_max)*100/$old_max" | bc -l)
                comparison=$(compare_with_tolerance $current_max $old_max "$current_op")

                case $comparison in
                    "worse") status="❌ Worse" ;;
                    "better") status="✅ Better" ;;
                    "same") status="⚖️ Same" ;;
                    "skip") status="⏭️ Skipped" ;;
                    *) status="⚠️ Unknown" ;;
                esac

                printf "%-30s %-12.2f %-12.2f %-12.2f %-12s %-12s%%\n" \
                       "$current_op" "$current_max" "$old_max" "$diff" "$status" "$variance"
            else
                printf "%-30s %-12s %-12s %-12s %-12s %-12s\n" \
                       "$current_op" "N/A" "N/A" "N/A" "⚠️ Invalid" "N/A"
            fi
        done < "$current_file" 3< "$old_file"
    fi
}

# Check if any scenario has "worse" results
check_regression() {
    local scenario=$1
    local current_file="${CURRENT_RESULTS}.${scenario}.summary"
    local old_file="${OLD_RESULTS}.${scenario}.summary"
    local regression_detected=0

    # Handle built-in mdtest scenario (scenario3)
    if [[ "$scenario" == "scenario3" ]]; then
        current_files_per_sec=$(extract_files_per_sec "$(cat "${current_file}")")
        old_files_per_sec=$(extract_files_per_sec "$(cat "${old_file}")")
        if (( $(echo "$old_files_per_sec == 0" | bc -l) )); then
            comparison="same"
        else
            comparison=$(compare_with_tolerance $current_files_per_sec $old_files_per_sec "builtin_mdtest")
        fi

        if [ "$comparison" == "worse" ]; then
            variance=$(echo "scale=2; ($current_files_per_sec - $old_files_per_sec)*100/$old_files_per_sec" | bc -l)
            echo "Regression detected in $scenario for built-in mdtest (files/s): Current $current_files_per_sec vs Old $old_files_per_sec (Variance: ${variance}%)"
            regression_detected=1
        fi
    
    # Handle fio scenarios
    elif [[ "$scenario" =~ ^fio ]]; then
        current_iops=$(extract_iops "$(cat "${current_file}")")
        old_iops=$(extract_iops "$(cat "${old_file}")")
        comparison=$(compare_with_tolerance $current_iops $old_iops "fio_${scenario}")

        if [ "$comparison" == "worse" ]; then
            variance=$(echo "scale=2; ($current_iops - $old_iops)*100/$old_iops" | bc -l)
            echo "Regression detected in $scenario: Current $current_iops IOPS vs Old $old_iops IOPS (Variance: ${variance}%)"
            regression_detected=1
        fi
    
    # Handle mdtest scenarios
    else
        while IFS= read -r current_line && IFS= read -r old_line <&3; do
            # Skip empty lines
            if [ -z "$current_line" ] || [ -z "$old_line" ]; then
                continue
            fi

            current_metrics=($(extract_metrics "$current_line"))
            old_metrics=($(extract_metrics "$old_line"))

            current_op="${current_metrics[0]} ${current_metrics[1]}"
            old_op="${old_metrics[0]} ${old_metrics[1]}"

            if [ "$current_op" != "$old_op" ]; then
                continue
            fi

            current_max=${current_metrics[2]}
            old_max=${old_metrics[2]}

            if [[ "$current_max" =~ ^[0-9.]+$ ]] && [[ "$old_max" =~ ^[0-9.]+$ ]]; then
                comparison=$(compare_with_tolerance $current_max $old_max "$current_op")
                if [ "$comparison" == "worse" ]; then
                    variance=$(echo "scale=2; ($current_max - $old_max)*100/$old_max" | bc -l)
                    echo "Regression detected in $scenario for $current_op: Current $current_max vs Old $old_max (Variance: ${variance}%)"
                    regression_detected=1
                fi
            fi
        done < "$current_file" 3< "$old_file"
    fi

    return $regression_detected
}

echo ""
echo "===================================================================="
echo "Performance Comparison Summary (with 20% tolerance)"
echo "===================================================================="

compare_scenario "scenario1"
compare_scenario "scenario2"
compare_scenario "scenario3"
compare_scenario "fio_scenario4"
compare_scenario "fio_scenario5"
compare_scenario "fio_scenario6"
compare_scenario "fio_scenario7"
compare_scenario "fio_scenario8"
compare_scenario "fio_scenario9"

echo ""
echo "===================================================================="
echo "Regression Check Summary (with 20% tolerance)"
echo "===================================================================="

regression_found=0
if ! check_regression "scenario1"; then
    regression_found=1
fi
if ! check_regression "scenario2"; then
    regression_found=1
fi
if ! check_regression "scenario3"; then
    regression_found=1
fi
if ! check_regression "fio_scenario4"; then
    regression_found=1
fi
if ! check_regression "fio_scenario5"; then
    regression_found=1
fi
if ! check_regression "fio_scenario6"; then
    regression_found=1
fi
if ! check_regression "fio_scenario7"; then
    regression_found=1
fi
if ! check_regression "fio_scenario8"; then
    regression_found=1
fi
if ! check_regression "fio_scenario9"; then
    regression_found=1
fi

if [ $regression_found -eq 1 ]; then
    echo ""
    echo "ERROR: Performance regression detected compared to old version!"
    exit 1
else
    echo ""
    echo "SUCCESS: No performance regression detected."
    exit 0
fi
