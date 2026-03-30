#!/bin/bash -e

[[ -z "$META" ]] && META=redis
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
source .github/scripts/common/common.sh

AWS_BUCKET=${AWS_BUCKET:-tiertest-${META}}
AWS_BUCKET=$(printf '%s' "$AWS_BUCKET" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9.-' '-')
AWS_BUCKET=${AWS_BUCKET#-}
AWS_BUCKET=${AWS_BUCKET%-}
AWS_REGION=${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}
AWS_ACCESS_KEY_VALUE=${AWS_ACCESS_KEY_ID:-${AWS_ACEESS_KEY:-}}
AWS_SECRET_KEY_VALUE=${AWS_SECRET_ACCESS_KEY:-}
AWS_SESSION_TOKEN_VALUE=${AWS_SESSION_TOKEN:-${AWS_ACCESS_TOKEN:-}}
ASSERT_RETRY_TIMES=${ASSERT_RETRY_TIMES:-30}
ASSERT_RETRY_INTERVAL=${ASSERT_RETRY_INTERVAL:-1}
if [[ "$AWS_REGION" == cn-* ]]; then
    DEFAULT_AWS_ENDPOINT_URL="https://s3.${AWS_REGION}.amazonaws.com.cn"
else
    DEFAULT_AWS_ENDPOINT_URL="https://s3.${AWS_REGION}.amazonaws.com"
fi
AWS_ENDPOINT_URL=${AWS_ENDPOINT_URL:-${AWS_S3_ENDPOINT_URL:-$DEFAULT_AWS_ENDPOINT_URL}}
AWS_BUCKET_URL=${AWS_BUCKET_URL:-${AWS_ENDPOINT_URL}/${AWS_BUCKET}}

ensure_aws_cli()
{
    if command -v aws >/dev/null 2>&1; then
        return 0
    fi
    if [[ "$PLATFORM" == "linux" ]]; then
        sudo .github/scripts/apt_install.sh awscli
    elif [[ "$PLATFORM" == "mac" ]]; then
        brew install awscli
    else
        echo "<FATAL>: unsupported platform for aws cli installation: $PLATFORM"
        exit 1
    fi
}

setup_aws_credentials()
{
    [[ -z "$AWS_ACCESS_KEY_VALUE" ]] && echo "<FATAL>: AWS access key is empty, set AWS_ACCESS_KEY_ID (or AWS_ACEESS_KEY)" && exit 1
    [[ -z "$AWS_SECRET_KEY_VALUE" ]] && echo "<FATAL>: AWS secret key is empty, set AWS_SECRET_ACCESS_KEY" && exit 1

    aws configure set aws_access_key_id "$AWS_ACCESS_KEY_VALUE"
    aws configure set aws_secret_access_key "$AWS_SECRET_KEY_VALUE"
    aws configure set default.region "$AWS_REGION"
    aws configure set default.output json
    if [[ -n "$AWS_SESSION_TOKEN_VALUE" ]]; then
        aws configure set aws_session_token "$AWS_SESSION_TOKEN_VALUE"
    fi

    aws sts get-caller-identity >/tmp/aws.identity.json
    local ak
    ak=$(aws configure get aws_access_key_id || true)
    echo "aws configured: region=$(aws configure get default.region || true), endpoint=$AWS_ENDPOINT_URL, bucket=$AWS_BUCKET"
    [[ -n "$ak" ]] && echo "aws configured access key prefix: ${ak:0:4}****"
    cat /tmp/aws.identity.json || true
}

recreate_aws_bucket_once()
{
    echo "recreate aws bucket: $AWS_BUCKET in region $AWS_REGION"
    aws s3 rb "s3://$AWS_BUCKET" --force --endpoint-url "$AWS_ENDPOINT_URL" >/dev/null 2>&1 || true

    if [[ "$AWS_REGION" == "us-east-1" ]]; then
        aws s3api create-bucket --bucket "$AWS_BUCKET" --endpoint-url "$AWS_ENDPOINT_URL" >/dev/null
    else
        aws s3api create-bucket \
            --bucket "$AWS_BUCKET" \
            --region "$AWS_REGION" \
            --endpoint-url "$AWS_ENDPOINT_URL" \
            --create-bucket-configuration LocationConstraint="$AWS_REGION" >/dev/null
    fi

    aws s3api wait bucket-exists --bucket "$AWS_BUCKET" --endpoint-url "$AWS_ENDPOINT_URL"
    aws s3api head-bucket --bucket "$AWS_BUCKET" --endpoint-url "$AWS_ENDPOINT_URL" >/tmp/aws.head_bucket.log 2>/tmp/aws.head_bucket.err || {
        cat /tmp/aws.head_bucket.err || true
        echo "<FATAL>: head-bucket failed for $AWS_BUCKET"
        exit 1
    }
    echo "aws bucket is ready: $AWS_BUCKET"
}

init_aws_bucket()
{
    ensure_aws_cli
    setup_aws_credentials
    recreate_aws_bucket_once
}

setup_tier_volume()
{
    prepare_test
    recreate_aws_bucket_once
    local format_cmd=(
        ./juicefs format "$META_URL" myjfs
        --storage s3
        --bucket "$AWS_BUCKET_URL"
        --access-key "$AWS_ACCESS_KEY_VALUE"
        --secret-key "$AWS_SECRET_KEY_VALUE"
        --trash-days 0
    )
    [[ -n "$AWS_SESSION_TOKEN_VALUE" ]] && format_cmd+=(--session-token "$AWS_SESSION_TOKEN_VALUE")

    "${format_cmd[@]}"
    ./juicefs mount -d "$META_URL" /jfs --heartbeat 2s

    # configure tier 1~3 before using juicefs tier commands
    ./juicefs config "$META_URL" --tier-id 1 --tier-sc STANDARD_IA -y
    ./juicefs config "$META_URL" --tier-id 2 --tier-sc INTELLIGENT_TIERING -y
    ./juicefs config "$META_URL" --tier-id 3 --tier-sc GLACIER_IR -y
}

get_tier_token()
{
    local path=$1
    local token
    token=$(./juicefs info "$path" | awk '/tier:/ {print $2; exit}')
    [[ -z "$token" ]] && return 1
    echo "$token"
}

assert_tier_id()
{
    local path=$1
    local expected=$2
    local token actual attempt
    for attempt in $(seq 1 "$ASSERT_RETRY_TIMES"); do
        token=$(get_tier_token "$path" 2>/dev/null || true)
        actual=${token%%->*}
        if [[ -n "$token" && "$actual" == "$expected" ]]; then
            return 0
        fi
        echo "wait tier id for $path, expect=$expected actual=${actual:-<empty>} attempt=$attempt/$ASSERT_RETRY_TIMES"
        sleep "$ASSERT_RETRY_INTERVAL"
    done
    echo "<FATAL>: tier id mismatch for $path, expect=$expected actual=${actual:-<empty>}"
    exit 1
}

assert_tier_sc()
{
    local path=$1
    local expected=$2
    local token sc actual attempt
    for attempt in $(seq 1 "$ASSERT_RETRY_TIMES"); do
        token=$(get_tier_token "$path" 2>/dev/null || true)
        sc=${token#*->}
        if [[ "$sc" == expected\(*\),actual\(*\) ]]; then
            actual=${sc##*actual(}
            actual=${actual%)}
        elif [[ "$sc" == actual\(*\) ]]; then
            actual=${sc#actual(}
            actual=${actual%)}
        else
            actual=$sc
        fi
        if [[ -n "$token" && "$actual" == "$expected" ]]; then
            return 0
        fi
        echo "wait tier storage class for $path, expect=$expected token_sc=${sc:-<empty>} actual=${actual:-<empty>} attempt=$attempt/$ASSERT_RETRY_TIMES"
        sleep "$ASSERT_RETRY_INTERVAL"
    done
    echo "<FATAL>: tier storage class mismatch for $path, expect=$expected actual=${actual:-<empty>}"
    exit 1
}

assert_tier_sc_expected_actual()
{
    local path=$1
    local expected_sc=$2
    local actual_sc=$3
    local token sc attempt
    local expect_token="expected(${expected_sc}),actual(${actual_sc})"
    for attempt in $(seq 1 "$ASSERT_RETRY_TIMES"); do
        token=$(get_tier_token "$path" 2>/dev/null || true)
        sc=${token#*->}
        if [[ -n "$token" && "$sc" == "$expect_token" ]]; then
            return 0
        fi
        echo "wait tier expected/actual for $path, expect=$expect_token got=${sc:-<empty>} attempt=$attempt/$ASSERT_RETRY_TIMES"
        sleep "$ASSERT_RETRY_INTERVAL"
    done
    echo "<FATAL>: tier expected/actual mismatch for $path, expect=$expect_token got=${sc:-<empty>}"
    exit 1
}

assert_config_tier_sc_fail()
{
    local id=$1
    local sc=$2
    if ./juicefs config "$META_URL" --tier-id "$id" --tier-sc "$sc" -y; then
        echo "<FATAL>: expect config failure but succeeded, id=$id storage-class=$sc"
        exit 1
    fi
}

assert_tier_list_storage_class()
{
    local id=$1
    local expected=$2
    local actual

    ./juicefs tier list "$META_URL" | tee /tmp/tier_list_output.log
    actual=$(awk -F'|' -v target_id="$id" '
        function trim(s) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
            return s
        }
        /^\|/ {
            current_id = trim($2)
            current_sc = trim($3)
            if (current_id == target_id) {
                print current_sc
                exit
            }
        }
    ' /tmp/tier_list_output.log)

    if [[ "$actual" != "$expected" ]]; then
        echo "<FATAL>: tier list verification failed for id=$id expect=$expected actual=${actual:-<empty>}"
        cat /tmp/tier_list_output.log
        exit 1
    fi
}

tier_set_no_err()
{
    local tmpout=/tmp/tier_set_last.log
    local status
    ./juicefs tier set "$@" 2>&1 | tee "$tmpout"
    status=${PIPESTATUS[0]}
    if grep -qF '<ERROR>' "$tmpout"; then
        echo "<FATAL>: juicefs tier set produced unexpected ERROR logs:"
        grep -F '<ERROR>' "$tmpout"
        exit 1
    fi
    return "$status"
}

get_first_object_key()
{
    local path=$1
    local obj
    obj=$(./juicefs info "$path" | grep -o 'myjfs/chunks/[^[:space:]|]*' | head -n 1)
    [[ -z "$obj" ]] && return 1
    echo "$obj"
}

get_object_storage_class()
{
    local key=$1
    local storage_class
    storage_class=$(aws s3api head-object \
        --bucket "$AWS_BUCKET" \
        --key "$key" \
        --endpoint-url "$AWS_ENDPOINT_URL" \
        --query 'StorageClass' \
        --output text 2>/tmp/tier_head_object.err) || return 1
    [[ "$storage_class" == "None" || "$storage_class" == "null" || "$storage_class" == "" ]] && storage_class="STANDARD"
    echo "$storage_class"
}

assert_object_storage_class_by_path()
{
    local path=$1
    local expected=$2
    local key actual attempt
    for attempt in $(seq 1 "$ASSERT_RETRY_TIMES"); do
        key=$(get_first_object_key "$path" 2>/dev/null || true)
        if [[ -z "$key" && "$attempt" -eq 1 ]]; then
            echo "debug: no chunk object key parsed from juicefs info for $path"
            ./juicefs info "$path" | tee /tmp/tier_info_missing_key.log || true
        fi
        if [[ -n "$key" ]]; then
            actual=$(get_object_storage_class "$key" 2>/dev/null || true)
        else
            actual=""
        fi
        if [[ -n "$key" && "$actual" == "$expected" ]]; then
            return 0
        fi
        echo "wait object storage class for $path, key=${key:-<empty>} expect=$expected actual=${actual:-<empty>} attempt=$attempt/$ASSERT_RETRY_TIMES"
        sleep "$ASSERT_RETRY_INTERVAL"
    done
    [[ -f /tmp/tier_head_object.err ]] && cat /tmp/tier_head_object.err || true
    echo "<FATAL>: object storage class mismatch for $path key=${key:-<empty>} expect=$expected actual=${actual:-<empty>}"
    exit 1
}

assert_info_no_empty_object_name()
{
    local path=$1
    local info_out=/tmp/tier_info_check.log
    ./juicefs info "$path" | tee "$info_out"
    # Detect rows in the objects table where objectName column is blank.
    # Bug example:
    #   |  0 | myjfs/chunks/0/9/9248_0_2 |  2 |  0 |  2 |  0 |
    #   |  0 |                           |  0 |  2 | 5722 |  2 |
    # The second row has empty objectName which indicates a chunk info corruption.
    if awk -F'|' '
        /^[[:space:]]*\|/ && NF >= 7 {
            idx = $2; name = $3
            gsub(/[[:space:]]/, "", idx)
            gsub(/[[:space:]]/, "", name)
            if (idx ~ /^[0-9]+$/ && name == "") { exit 0 }
        }
        END { exit 1 }
    ' "$info_out"; then
        echo "<FATAL>: juicefs info for $path has empty objectName in chunk table:"
        cat "$info_out"
        exit 1
    fi
}

test_tier_list_and_file_set_conversion()
{
    setup_tier_volume

    ./juicefs tier list "$META_URL" | tee /tmp/tier.list.log
    mkdir -p /jfs/file_case
    dd if=/dev/urandom of=/jfs/file_case/f1 bs=1M count=8 status=none

    tier_set_no_err "$META_URL" --id 1 /file_case/f1
    sleep 5
    assert_tier_id /jfs/file_case/f1 1
    ./juicefs info /jfs/file_case/f1
    assert_tier_sc /jfs/file_case/f1 STANDARD_IA
    assert_object_storage_class_by_path /jfs/file_case/f1 STANDARD_IA
    cat /jfs/file_case/f1 >/dev/null

    tier_set_no_err "$META_URL" --id 2 /file_case/f1
    sleep 5
    assert_tier_id /jfs/file_case/f1 2
    assert_tier_sc /jfs/file_case/f1 INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/file_case/f1 INTELLIGENT_TIERING
    cat /jfs/file_case/f1 >/dev/null

    ./juicefs tier set "$META_URL" --id 0 /file_case/f1
    sleep 5
    assert_tier_id /jfs/file_case/f1 0
    assert_object_storage_class_by_path /jfs/file_case/f1 STANDARD
    cat /jfs/file_case/f1 >/dev/null
}

test_tier_config_all_storage_classes()
{
    setup_tier_volume

    local storage_classes=(
        "STANDARD"
        "INTELLIGENT_TIERING"
        "STANDARD_IA"
        "ONEZONE_IA"
        "GLACIER_IR"
        "GLACIER"
        "DEEP_ARCHIVE"
    )

    for sc in "${storage_classes[@]}"; do
        echo "Testing config with storage class: $sc"
        ./juicefs config "$META_URL" --tier-id 2 --tier-sc "$sc" -y || {
            echo "<FATAL>: config failed for storage class $sc"
            exit 1
        }
        sleep 1
        assert_tier_list_storage_class 2 "$sc"
        echo "✓ config storage class $sc verified via tier list"
    done
}

test_tier_dir_recursive_and_non_recursive()
{
    setup_tier_volume

    command -v git >/dev/null 2>&1 || {
        echo "<FATAL>: git is required for test_tier_dir_recursive_and_non_recursive"
        exit 1
    }

    mkdir -p /jfs/dir_case
    git clone --depth 1 https://github.com/juicedata/juicefs.git /jfs/dir_case/juicefs

    tier_set_no_err "$META_URL" --id 1 /dir_case/juicefs
    assert_tier_id /jfs/dir_case/juicefs 1
    assert_tier_id /jfs/dir_case/juicefs/cmd 0
    assert_tier_id /jfs/dir_case/juicefs/pkg 0
    assert_tier_id /jfs/dir_case/juicefs/docs 0
    assert_tier_id /jfs/dir_case/juicefs/README.md 0
    assert_tier_id /jfs/dir_case/juicefs/go.mod 0
    assert_tier_id /jfs/dir_case/juicefs/Makefile 0

    tier_set_no_err "$META_URL" --id 2 /dir_case/juicefs -r
    assert_tier_id /jfs/dir_case/juicefs 2
    assert_tier_id /jfs/dir_case/juicefs/cmd 2
    assert_tier_id /jfs/dir_case/juicefs/pkg 2
    assert_tier_id /jfs/dir_case/juicefs/docs 2
    assert_tier_id /jfs/dir_case/juicefs/README.md 2
    assert_tier_id /jfs/dir_case/juicefs/go.mod 2
    assert_tier_id /jfs/dir_case/juicefs/Makefile 2
    assert_object_storage_class_by_path /jfs/dir_case/juicefs/README.md INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/dir_case/juicefs/go.mod INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/dir_case/juicefs/Makefile INTELLIGENT_TIERING
}

test_tier_clone_after_dir_set()
{
    setup_tier_volume

    mkdir -p /jfs/clone_src/a/b
    for i in $(seq 1 20); do
        echo "data_$i" > /jfs/clone_src/a/b/file_$i
    done

    tier_set_no_err "$META_URL" --id 2 /clone_src -r
    ./juicefs clone /jfs/clone_src /jfs/clone_dst
    diff -ur /jfs/clone_src /jfs/clone_dst --no-dereference

    src_tier=$(get_tier_token /jfs/clone_src/a/b/file_1)
    dst_tier=$(get_tier_token /jfs/clone_dst/a/b/file_1)
    [[ "$src_tier" != "$dst_tier" ]] && echo "<FATAL>: clone tier mismatch src=$src_tier dst=$dst_tier" && exit 1
    assert_object_storage_class_by_path /jfs/clone_src/a/b/file_1 INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/clone_dst/a/b/file_1 INTELLIGENT_TIERING
}

test_tier_change_mapping_after_set()
{
    setup_tier_volume

    mkdir -p /jfs/reconf
    echo "reconf" > /jfs/reconf/file

    tier_set_no_err "$META_URL" --id 2 /reconf/file
    assert_tier_id /jfs/reconf/file 2
    assert_tier_sc /jfs/reconf/file INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/reconf/file INTELLIGENT_TIERING

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc STANDARD_IA -y
    sleep 5
    assert_tier_id /jfs/reconf/file 2
    assert_tier_sc_expected_actual /jfs/reconf/file STANDARD_IA INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/reconf/file INTELLIGENT_TIERING
    tier_set_no_err "$META_URL" --id 2 /reconf/file --force
    assert_tier_id /jfs/reconf/file 2
    assert_tier_sc /jfs/reconf/file STANDARD_IA
    assert_object_storage_class_by_path /jfs/reconf/file STANDARD_IA
    cat /jfs/reconf/file >/dev/null
}

test_tier_invalid_mapping_reapply()
{
    setup_tier_volume

    mkdir -p /jfs/invalid_map_case/a/b
    dd if=/dev/urandom of=/jfs/invalid_map_case/root.bin bs=1M count=8 status=none
    dd if=/dev/urandom of=/jfs/invalid_map_case/a/b/child.bin bs=1M count=8 status=none

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc GLACIER_IR -y
    sleep 5
    tier_set_no_err "$META_URL" --id 2 /invalid_map_case -r
    assert_tier_sc /jfs/invalid_map_case GLACIER_IR
    assert_tier_sc /jfs/invalid_map_case/a GLACIER_IR
    assert_tier_sc /jfs/invalid_map_case/a/b GLACIER_IR
    assert_tier_sc /jfs/invalid_map_case/root.bin GLACIER_IR
    assert_tier_sc /jfs/invalid_map_case/a/b/child.bin GLACIER_IR
    assert_object_storage_class_by_path /jfs/invalid_map_case/root.bin GLACIER_IR
    assert_object_storage_class_by_path /jfs/invalid_map_case/a/b/child.bin GLACIER_IR

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc INTELLIGENT_TIERING -y
    sleep 5
    assert_tier_sc_expected_actual /jfs/invalid_map_case/root.bin INTELLIGENT_TIERING GLACIER_IR
    assert_tier_sc_expected_actual /jfs/invalid_map_case/a/b/child.bin INTELLIGENT_TIERING GLACIER_IR
    assert_object_storage_class_by_path /jfs/invalid_map_case/root.bin GLACIER_IR
    assert_object_storage_class_by_path /jfs/invalid_map_case/a/b/child.bin GLACIER_IR
    tier_set_no_err "$META_URL" --id 2 /invalid_map_case -r --force
    assert_tier_sc /jfs/invalid_map_case INTELLIGENT_TIERING
    assert_tier_sc /jfs/invalid_map_case/a INTELLIGENT_TIERING
    assert_tier_sc /jfs/invalid_map_case/a/b INTELLIGENT_TIERING
    assert_tier_sc /jfs/invalid_map_case/root.bin INTELLIGENT_TIERING
    assert_tier_sc /jfs/invalid_map_case/a/b/child.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/invalid_map_case/root.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/invalid_map_case/a/b/child.bin INTELLIGENT_TIERING
}

test_tier_invalid_storage_class()
{
    setup_tier_volume

    mkdir -p /jfs/invalid_set_case/dir
    dd if=/dev/urandom of=/jfs/invalid_set_case/file.bin bs=1M count=8 status=none
    dd if=/dev/urandom of=/jfs/invalid_set_case/dir/sub.bin bs=1M count=8 status=none

    tier_set_no_err "$META_URL" --id 1 /invalid_set_case/file.bin
    tier_set_no_err "$META_URL" --id 1 /invalid_set_case/dir -r
    assert_tier_sc /jfs/invalid_set_case/file.bin STANDARD_IA
    assert_tier_sc /jfs/invalid_set_case/dir STANDARD_IA
    assert_tier_sc /jfs/invalid_set_case/dir/sub.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/invalid_set_case/file.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/invalid_set_case/dir/sub.bin STANDARD_IA

    assert_config_tier_sc_fail 2 WRONG_STORAGE_CLASS
    tier_set_no_err "$META_URL" --id 2 /invalid_set_case/file.bin
    tier_set_no_err "$META_URL" --id 2 /invalid_set_case/dir -r

    assert_tier_sc /jfs/invalid_set_case/file.bin INTELLIGENT_TIERING
    assert_tier_sc /jfs/invalid_set_case/dir INTELLIGENT_TIERING
    assert_tier_sc /jfs/invalid_set_case/dir/sub.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/invalid_set_case/file.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/invalid_set_case/dir/sub.bin INTELLIGENT_TIERING
}

test_tier_config_change_then_set()
{
    setup_tier_volume

    mkdir -p /jfs/rewrite_case
    dd if=/dev/urandom of=/jfs/rewrite_case/file.bin bs=1M count=8 status=none

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc INTELLIGENT_TIERING -y
    sleep 5
    tier_set_no_err "$META_URL" --id 2 /rewrite_case/file.bin
    assert_tier_id /jfs/rewrite_case/file.bin 2
    assert_tier_sc /jfs/rewrite_case/file.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/rewrite_case/file.bin INTELLIGENT_TIERING

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc STANDARD_IA -y
    sleep 5
    assert_tier_sc_expected_actual /jfs/rewrite_case/file.bin STANDARD_IA INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/rewrite_case/file.bin INTELLIGENT_TIERING
    tier_set_no_err "$META_URL" --id 2 /rewrite_case/file.bin --force
    assert_tier_id /jfs/rewrite_case/file.bin 2
    assert_tier_sc /jfs/rewrite_case/file.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/rewrite_case/file.bin STANDARD_IA

    dd if=/dev/urandom of=/jfs/rewrite_case/file.bin bs=1M count=8 status=none
    cat /jfs/rewrite_case/file.bin >/dev/null
    assert_tier_id /jfs/rewrite_case/file.bin 2
    assert_tier_sc /jfs/rewrite_case/file.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/rewrite_case/file.bin STANDARD_IA
}

test_tier_mixed_tree_reapply_after_mapping_change()
{
    setup_tier_volume

    mkdir -p /jfs/mixed_case/dir1/dir2
    dd if=/dev/urandom of=/jfs/mixed_case/dir1/old.bin bs=1M count=8 status=none

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc STANDARD_IA -y
    sleep 5
    tier_set_no_err "$META_URL" --id 2 /mixed_case -r
    assert_tier_sc /jfs/mixed_case/dir1/old.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/mixed_case/dir1/old.bin STANDARD_IA

    ./juicefs config "$META_URL" --tier-id 2 --tier-sc INTELLIGENT_TIERING -y
    sleep 5
    assert_tier_sc_expected_actual /jfs/mixed_case/dir1/old.bin INTELLIGENT_TIERING STANDARD_IA
    assert_object_storage_class_by_path /jfs/mixed_case/dir1/old.bin STANDARD_IA
    dd if=/dev/urandom of=/jfs/mixed_case/dir1/dir2/new.bin bs=1M count=8 status=none
    tier_set_no_err "$META_URL" --id 2 /mixed_case -r --force

    assert_tier_sc /jfs/mixed_case INTELLIGENT_TIERING
    assert_tier_sc /jfs/mixed_case/dir1 INTELLIGENT_TIERING
    assert_tier_sc /jfs/mixed_case/dir1/dir2 INTELLIGENT_TIERING
    assert_tier_sc /jfs/mixed_case/dir1/old.bin INTELLIGENT_TIERING
    assert_tier_sc /jfs/mixed_case/dir1/dir2/new.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/mixed_case/dir1/old.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/mixed_case/dir1/dir2/new.bin INTELLIGENT_TIERING
}

test_tier_glacier_deep_archive_restore()
{
    setup_tier_volume

    mkdir -p /jfs/archive_case/sub
    echo "archivedata1" > /jfs/archive_case/a.txt
    echo "archivedata2" > /jfs/archive_case/sub/b.txt

    ./juicefs config "$META_URL" --tier-id 3 --tier-sc GLACIER -y
    sleep 5
    tier_set_no_err "$META_URL" --id 3 /archive_case -r
    assert_tier_id /jfs/archive_case/a.txt 3
    assert_tier_sc /jfs/archive_case/a.txt GLACIER
    assert_object_storage_class_by_path /jfs/archive_case/a.txt GLACIER

    # GLACIER objects are not directly readable, so restore first.
    ./juicefs tier restore "$META_URL" /archive_case -r

    ./juicefs config "$META_URL" --tier-id 3 --tier-sc DEEP_ARCHIVE -y
    sleep 5
    tier_set_no_err "$META_URL" --id 3 /archive_case/sub/b.txt
    assert_tier_id /jfs/archive_case/sub/b.txt 3
    assert_tier_sc /jfs/archive_case/sub/b.txt DEEP_ARCHIVE
    assert_object_storage_class_by_path /jfs/archive_case/sub/b.txt DEEP_ARCHIVE

    # DEEP_ARCHIVE objects are not directly readable, so restore first.
    ./juicefs tier restore "$META_URL" /archive_case/sub/b.txt
}

test_tier_overwrite_after_recursive_set()
{
    setup_tier_volume

    # Create directory tree with files of different sizes
    mkdir -p /jfs/ow_case/sub1/sub2
    dd if=/dev/urandom of=/jfs/ow_case/f1.bin bs=1K count=64 status=none
    dd if=/dev/urandom of=/jfs/ow_case/sub1/f2.bin bs=1M count=4 status=none
    dd if=/dev/urandom of=/jfs/ow_case/sub1/sub2/f3.bin bs=1K count=256 status=none

    # Set tier 1 recursively on the whole tree
    tier_set_no_err "$META_URL" --id 1 /ow_case -r
    assert_tier_id /jfs/ow_case/f1.bin 1
    assert_tier_id /jfs/ow_case/sub1/f2.bin 1
    assert_tier_id /jfs/ow_case/sub1/sub2/f3.bin 1
    assert_tier_sc /jfs/ow_case/f1.bin STANDARD_IA
    assert_tier_sc /jfs/ow_case/sub1/f2.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/ow_case/f1.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/ow_case/sub1/f2.bin STANDARD_IA

    # Scenario A: short echo overwrite (original 64KB -> 6 bytes)
    echo "short" > /jfs/ow_case/f1.bin
    sleep 2
    assert_tier_id /jfs/ow_case/f1.bin 0
    assert_info_no_empty_object_name /jfs/ow_case/f1.bin
    local content
    content=$(cat /jfs/ow_case/f1.bin)
    [[ "$content" == "short" ]] || { echo "<FATAL>: f1.bin content mismatch after short overwrite, got='$content'"; exit 1; }

    # Scenario B: long dd overwrite (original 4MB -> 8MB)
    dd if=/dev/urandom of=/jfs/ow_case/sub1/f2.bin bs=1M count=8 status=none
    sleep 2
    assert_tier_id /jfs/ow_case/sub1/f2.bin 0
    assert_info_no_empty_object_name /jfs/ow_case/sub1/f2.bin
    cat /jfs/ow_case/sub1/f2.bin > /dev/null

    # Untouched file should still have tier 1
    assert_tier_id /jfs/ow_case/sub1/sub2/f3.bin 1
    assert_tier_sc /jfs/ow_case/sub1/sub2/f3.bin STANDARD_IA

    # Directories should still have tier 1 (not affected by file overwrite)
    assert_tier_id /jfs/ow_case 1
    assert_tier_id /jfs/ow_case/sub1 1
    assert_tier_id /jfs/ow_case/sub1/sub2 1
}

test_tier_reset_to_zero()
{
    setup_tier_volume

    mkdir -p /jfs/reset_case/sub
    dd if=/dev/urandom of=/jfs/reset_case/f1.bin bs=1M count=4 status=none
    dd if=/dev/urandom of=/jfs/reset_case/sub/f2.bin bs=1M count=4 status=none
    echo "small" > /jfs/reset_case/sub/f3.txt

    # Set files to different tiers
    tier_set_no_err "$META_URL" --id 1 /reset_case/f1.bin
    tier_set_no_err "$META_URL" --id 2 /reset_case/sub -r
    assert_tier_id /jfs/reset_case/f1.bin 1
    assert_tier_sc /jfs/reset_case/f1.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/reset_case/f1.bin STANDARD_IA
    assert_tier_id /jfs/reset_case/sub/f2.bin 2
    assert_tier_sc /jfs/reset_case/sub/f2.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/reset_case/sub/f2.bin INTELLIGENT_TIERING
    assert_tier_id /jfs/reset_case/sub/f3.txt 2

    # Reset individual file back to tier 0
    ./juicefs tier set "$META_URL" --id 0 /reset_case/f1.bin
    sleep 5
    assert_tier_id /jfs/reset_case/f1.bin 0
    assert_object_storage_class_by_path /jfs/reset_case/f1.bin STANDARD
    cat /jfs/reset_case/f1.bin > /dev/null

    # Reset directory recursively back to tier 0
    ./juicefs tier set "$META_URL" --id 0 /reset_case -r
    sleep 5
    assert_tier_id /jfs/reset_case 0
    assert_tier_id /jfs/reset_case/sub 0
    assert_tier_id /jfs/reset_case/f1.bin 0
    assert_tier_id /jfs/reset_case/sub/f2.bin 0
    assert_tier_id /jfs/reset_case/sub/f3.txt 0
    assert_object_storage_class_by_path /jfs/reset_case/f1.bin STANDARD
    assert_object_storage_class_by_path /jfs/reset_case/sub/f2.bin STANDARD
    cat /jfs/reset_case/f1.bin > /dev/null
    cat /jfs/reset_case/sub/f2.bin > /dev/null
    cat /jfs/reset_case/sub/f3.txt > /dev/null
}

test_tier_overwrite_roundtrip()
{
    setup_tier_volume

    mkdir -p /jfs/rt_case
    dd if=/dev/urandom of=/jfs/rt_case/file.bin bs=1M count=4 status=none

    # Cycle 1: set tier 1 -> short overwrite -> verify tier resets to 0
    tier_set_no_err "$META_URL" --id 1 /rt_case/file.bin
    assert_tier_id /jfs/rt_case/file.bin 1
    assert_tier_sc /jfs/rt_case/file.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/rt_case/file.bin STANDARD_IA

    echo "cycle1" > /jfs/rt_case/file.bin
    sleep 2
    assert_tier_id /jfs/rt_case/file.bin 0
    assert_info_no_empty_object_name /jfs/rt_case/file.bin
    [[ "$(cat /jfs/rt_case/file.bin)" == "cycle1" ]] || { echo "<FATAL>: content mismatch after cycle1 overwrite"; exit 1; }

    # Cycle 2: set tier 2 -> long dd overwrite -> verify tier resets to 0
    tier_set_no_err "$META_URL" --id 2 /rt_case/file.bin
    assert_tier_id /jfs/rt_case/file.bin 2
    assert_tier_sc /jfs/rt_case/file.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/rt_case/file.bin INTELLIGENT_TIERING

    dd if=/dev/urandom of=/jfs/rt_case/file.bin bs=1M count=8 status=none
    sleep 2
    assert_tier_id /jfs/rt_case/file.bin 0
    assert_info_no_empty_object_name /jfs/rt_case/file.bin

    # Cycle 3: set tier 3 -> verify (final state, no overwrite)
    tier_set_no_err "$META_URL" --id 3 /rt_case/file.bin
    assert_tier_id /jfs/rt_case/file.bin 3
    assert_tier_sc /jfs/rt_case/file.bin GLACIER_IR
    assert_object_storage_class_by_path /jfs/rt_case/file.bin GLACIER_IR
}

test_tier_truncate_and_append_after_set()
{
    setup_tier_volume

    mkdir -p /jfs/ta_case
    dd if=/dev/urandom of=/jfs/ta_case/file.bin bs=1M count=4 status=none

    # Truncate to 0 bytes after tier set -> tier should reset to 0
    tier_set_no_err "$META_URL" --id 1 /ta_case/file.bin
    assert_tier_id /jfs/ta_case/file.bin 1
    assert_tier_sc /jfs/ta_case/file.bin STANDARD_IA

    : > /jfs/ta_case/file.bin
    sleep 2
    assert_tier_id /jfs/ta_case/file.bin 0

    # Rewrite and set tier again, then append
    dd if=/dev/urandom of=/jfs/ta_case/file.bin bs=1M count=2 status=none
    tier_set_no_err "$META_URL" --id 2 /ta_case/file.bin
    assert_tier_id /jfs/ta_case/file.bin 2
    assert_tier_sc /jfs/ta_case/file.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/ta_case/file.bin INTELLIGENT_TIERING

    # Append 2MB to the tiered 2MB file -> tier should reset to 0
    dd if=/dev/urandom bs=1M count=2 status=none >> /jfs/ta_case/file.bin
    sleep 2
    assert_tier_id /jfs/ta_case/file.bin 0
    assert_info_no_empty_object_name /jfs/ta_case/file.bin
    local actual_size
    actual_size=$(stat -c%s /jfs/ta_case/file.bin 2>/dev/null || stat -f%z /jfs/ta_case/file.bin)
    [[ "$actual_size" -eq $((4 * 1024 * 1024)) ]] || {
        echo "<FATAL>: file size mismatch after append, expect=4194304 got=$actual_size"
        exit 1
    }
    cat /jfs/ta_case/file.bin > /dev/null
}

test_tier_mixed_tree_partial_overwrite()
{
    setup_tier_volume

    # Create a deep directory tree with files at every level
    mkdir -p /jfs/pt_case/a/b/c
    dd if=/dev/urandom of=/jfs/pt_case/root.bin bs=1M count=2 status=none
    dd if=/dev/urandom of=/jfs/pt_case/a/mid.bin bs=1M count=2 status=none
    dd if=/dev/urandom of=/jfs/pt_case/a/b/deep.bin bs=1M count=2 status=none
    dd if=/dev/urandom of=/jfs/pt_case/a/b/c/leaf.bin bs=1M count=2 status=none
    echo "keep me" > /jfs/pt_case/a/b/c/small.txt

    # Set all to tier 1 recursively
    tier_set_no_err "$META_URL" --id 1 /pt_case -r
    for f in root.bin a/mid.bin a/b/deep.bin a/b/c/leaf.bin a/b/c/small.txt; do
        assert_tier_id "/jfs/pt_case/$f" 1
    done
    assert_tier_sc /jfs/pt_case/root.bin STANDARD_IA
    assert_object_storage_class_by_path /jfs/pt_case/root.bin STANDARD_IA

    # Overwrite only some files at different levels
    echo "x" > /jfs/pt_case/root.bin                                            # short overwrite at root
    dd if=/dev/urandom of=/jfs/pt_case/a/b/deep.bin bs=1M count=4 status=none    # long overwrite in middle
    sleep 2

    # Overwritten files should reset to tier 0 with correct chunk info
    assert_tier_id /jfs/pt_case/root.bin 0
    assert_tier_id /jfs/pt_case/a/b/deep.bin 0
    assert_info_no_empty_object_name /jfs/pt_case/root.bin
    assert_info_no_empty_object_name /jfs/pt_case/a/b/deep.bin

    # Untouched files should still have tier 1
    assert_tier_id /jfs/pt_case/a/mid.bin 1
    assert_tier_id /jfs/pt_case/a/b/c/leaf.bin 1
    assert_tier_id /jfs/pt_case/a/b/c/small.txt 1
    assert_tier_sc /jfs/pt_case/a/mid.bin STANDARD_IA
    assert_tier_sc /jfs/pt_case/a/b/c/leaf.bin STANDARD_IA

    # Re-set entire tree to tier 2 recursively (mix of tier-0 and tier-1 files all become tier-2)
    tier_set_no_err "$META_URL" --id 2 /pt_case -r
    for f in root.bin a/mid.bin a/b/deep.bin a/b/c/leaf.bin a/b/c/small.txt; do
        assert_tier_id "/jfs/pt_case/$f" 2
        assert_tier_sc "/jfs/pt_case/$f" INTELLIGENT_TIERING
    done
    assert_object_storage_class_by_path /jfs/pt_case/root.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/pt_case/a/b/deep.bin INTELLIGENT_TIERING
    assert_object_storage_class_by_path /jfs/pt_case/a/b/c/leaf.bin INTELLIGENT_TIERING

    # Newly created file after recursive set should have default tier 0
    dd if=/dev/urandom of=/jfs/pt_case/a/b/new_after_set.bin bs=1M count=1 status=none
    assert_tier_id /jfs/pt_case/a/b/new_after_set.bin 0
}

init_aws_bucket
source .github/scripts/common/run_test.sh && run_test "$@"
