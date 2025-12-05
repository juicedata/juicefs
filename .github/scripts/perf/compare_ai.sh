#!/bin/bash
# fixed_compare.sh

current_file="$1"
old_file="$2"
TOLERANCE=${TOLERANCE:-0.3}
EXIT_ON_REGRESSION=${EXIT_ON_REGRESSION:-true}

echo "===================================================================="
echo "Fixed Performance Comparison Summary ($(echo "$TOLERANCE * 100" | bc)% tolerance):"
echo "===================================================================="
echo "Current: $current_file"
echo "Old:     $old_file"
echo "===================================================================="

regression_detected=false

keys=$(jq -r '.summary | keys[]' "$current_file")

declare -A categories
categories["lmdb"]="Lmdb"
categories["pytorch_weights"]="PyTorch Weights"
categories["tensorflow_h5"]="TensorFlow H5"
categories["huggingface"]="HuggingFace Bin"
categories["tensorflow_checkpoint"]="TensorFlow Checkpoint"
categories["tfrecord"]="TFRecord Dataset"
categories["hdf5_dataset"]="HDF5 Dataset"
categories["parquet"]="Parquet Dataset"

echo "Available keys in results:"
echo "$keys"
echo ""

for category_pattern in "${!categories[@]}"; do
    category_name="${categories[$category_pattern]}"
    echo "=== $category_name ==="
    category_keys=$(echo "$keys" | grep "^${category_pattern}_")
    if [ -z "$category_keys" ]; then
        echo "  No tests found for this category (pattern: $category_pattern)"
        echo ""
        continue
    fi
    
    while read -r key; do
        current_throughput=$(jq -r ".summary.\"$key\".throughput_mb_s" "$current_file")
        old_throughput=$(jq -r ".summary.\"$key\".throughput_mb_s" "$old_file")

        if [ "$current_throughput" = "null" ] || [ "$old_throughput" = "null" ] || [ "$current_throughput" = "" ] || [ "$old_throughput" = "" ]; then
            continue
        fi

        diff=$(echo "scale=1; $current_throughput - $old_throughput" | bc)
        diff_pct=$(echo "scale=1; ($diff / $old_throughput) * 100" | bc)
        abs_diff_pct=$(echo $diff_pct | awk '{if ($1<0) print -$1; else print $1}')
        current_formatted=$(printf "%.1f" "$current_throughput")
        old_formatted=$(printf "%.1f" "$old_throughput")
        diff_pct_formatted=$(printf "%.1f" "$diff_pct")

        status="✓ OK"
        if (( $(echo "$abs_diff_pct > $TOLERANCE * 100" | bc -l) )); then
            if (( $(echo "$current_throughput < $old_throughput" | bc -l) )); then
                status="❌ Worse"
                regression_detected=true
            else
                status="✅ Better"
            fi
        fi

        test_size=$(echo "$key" | awk -F_ '{print $(NF-1)}')
        test_operation=$(echo "$key" | awk -F_ '{print $NF}')
        
        echo "  ${test_size}_${test_operation}:"
        echo "    Current: $current_formatted MB/s"
        echo "    Old:     $old_formatted MB/s"
        echo "    Diff:    $diff_pct_formatted%"
        echo "    Status:  $status"
        echo ""

    done <<< "$category_keys"
done

echo "===================================================================="
echo "Summary:"
if [ "$regression_detected" = true ]; then
    echo "❌ PERFORMANCE REGRESSION DETECTED!"
    if [ "$EXIT_ON_REGRESSION" = true ]; then
        exit 1
    else
        exit 0
    fi
else
    echo "✅ No performance regression detected."
    exit 0
fi
