#!/bin/bash
set -e

CURRENT_RESULTS=$1
OLD_RESULTS=$2

extract_metrics() {
    awk '{
        op_description=$1; 
        op_type=$2;
        for(i=3;i<=NF;i++) if($i == ":") break;
        max=$(i+1); min=$(i+2); mean=$(i+3); stddev=$(i+4);
        print op_description, op_type, max, min, mean, stddev
    }' <<< "$1"
}

compare_with_tolerance() {
    local current=$1
    local old=$2

    tolerance=$(echo "$old * 0.1" | bc -l)
    lower_bound=$(echo "$old - $tolerance" | bc -l)
    upper_bound=$(echo "$old + $tolerance" | bc -l)

    if (( $(echo "$current <= $upper_bound && $current >= $lower_bound" | bc -l) )); then
        echo "same"
    elif (( $(echo "$current > $old" | bc -l) )); then
        echo "better"
    else
        echo "worse"
    fi
}

compare_scenario() {
    local scenario=$1
    local current_file="${CURRENT_RESULTS}.${scenario}.summary"
    local old_file="${OLD_RESULTS}.${scenario}.summary"

    echo ""
    echo "===================================================================="
    echo "Detailed Comparison for $scenario (with 10% tolerance)"
    echo "===================================================================="
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

            comparison=$(compare_with_tolerance $current_max $old_max)

            case $comparison in
                "worse") status="❌ Worse" ;;
                "better") status="✅ Better" ;;
                "same") status="⚖️ Same" ;;
                *) status="⚠️ Unknown" ;;
            esac

            printf "%-30s %-12.2f %-12.2f %-12.2f %-12s %-12s%%\n" \
                   "$current_op" "$current_max" "$old_max" "$diff" "$status" "$variance"
        else
            printf "%-30s %-12s %-12s %-12s %-12s %-12s\n" \
                   "$current_op" "N/A" "N/A" "N/A" "⚠️ Invalid" "N/A"
        fi
    done < "$current_file" 3< "$old_file"
}

compare_scenario "scenario1"
compare_scenario "scenario2"

# Check if any scenario has "worse" results
check_regression() {
    local scenario=$1
    local current_file="${CURRENT_RESULTS}.${scenario}.summary"
    local old_file="${OLD_RESULTS}.${scenario}.summary"
    local regression_detected=0

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
            comparison=$(compare_with_tolerance $current_max $old_max)
            if [ "$comparison" == "worse" ]; then
                variance=$(echo "scale=2; ($current_max - $old_max)*100/$old_max" | bc -l)
                echo "Regression detected in $scenario for $current_op: Current $current_max vs Old $old_max (Variance: ${variance}%)"
                regression_detected=1
            fi
        fi
    done < "$current_file" 3< "$old_file"

    return $regression_detected
}

echo ""
echo "===================================================================="
echo "Regression Check Summary (with 10% tolerance)"
echo "===================================================================="

regression_found=0
if ! check_regression "scenario1"; then
    regression_found=1
fi
if ! check_regression "scenario2"; then
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