#!/bin/bash

#  Mint (C) 2017-2020 Minio, Inc.
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

# environment

os="linux"
errno=$errno
if [[ `uname  -a` =~ "Darwin" ]];then
    os="mac"
    errno=254
fi
echo "os=$os"

set -x
os="linux"
errno=$errno
if [[ `uname  -a` =~ "Darwin" ]];then
    os="mac"
    errno=254
fi
echo "os=$os"


MINT_DATA_DIR=testdata
MINT_MODE=core
SERVER_ENDPOINT="127.0.0.1:9008"
ACCESS_KEY="testUser"
SECRET_KEY="testUserPassword"
ENABLE_HTTPS=0
SERVER_REGION=us-east-1
ENABLE_VIRTUAL_STYLE=0




# create testdata
declare -A data_file_map
data_file_map["datafile-0-b"]="0"
data_file_map["datafile-1-b"]="1"
data_file_map["datafile-1-kB"]="1K"
data_file_map["datafile-10-kB"]="10K"
data_file_map["datafile-33-kB"]="33K"
data_file_map["datafile-100-kB"]="100K"
data_file_map["datafile-1.03-MB"]="1056K"
data_file_map["datafile-1-MB"]="1M"
data_file_map["datafile-5-MB"]="5M"
data_file_map["datafile-5243880-b"]="5243880"
data_file_map["datafile-6-MB"]="6M"
data_file_map["datafile-10-MB"]="10M"
data_file_map["datafile-11-MB"]="11M"
data_file_map["datafile-65-MB"]="65M"
data_file_map["datafile-129-MB"]="129M"

mkdir -p "$MINT_DATA_DIR"


if [ ! "$(ls $MINT_DATA_DIR)" ]; then
    for filename in "${!data_file_map[@]}"; do
        echo "creating $MINT_DATA_DIR/$filename"
        if ! shred -n 1 -s "${data_file_map[$filename]}" - 1>"$MINT_DATA_DIR/$filename" 2>/dev/null; then
            echo "unable to create data file $MINT_DATA_DIR/$filename"
            exit 1
        fi
    done
fi

# configuration
aws configure set aws_access_key_id "$ACCESS_KEY"
aws configure set aws_secret_access_key "$SECRET_KEY"
aws configure set default.region "$SERVER_REGION"

# run tests for virtual style if provided
if [ "$ENABLE_VIRTUAL_STYLE" -eq 1 ]; then
   # Setup endpoint scheme
   endpoint="http://$DOMAIN:$SERVER_PORT"
   if [ "$ENABLE_HTTPS" -eq 1 ]; then
       endpoint="https://$DOMAIN:$SERVER_PORT"
   fi
   dnsmasq --address="/$DOMAIN/$SERVER_IP" --user=root
   echo -e "nameserver 127.0.0.1\n$(cat /etc/resolv.conf)" > /etc/resolv.conf
   aws configure set default.s3.addressing_style virtual
#    ./test.sh "$endpoint"  1>>"$output_log_file" 2>"$error_log_file"
   ./test.sh "$endpoint"
   aws configure set default.s3.addressing_style path
fi

endpoint="http://$SERVER_ENDPOINT"
if [ "$ENABLE_HTTPS" -eq 1 ]; then
    endpoint="https://$SERVER_ENDPOINT"
fi
# run path style tests
# ./test.sh "$endpoint"  1>>"$output_log_file" 2>"$error_log_file"


# test
function get_md5() {
    if [ $os == "mac" ]; then
        md5rt=$(md5 "$1" | awk '{print $4}')
    else
        md5rt=$(md5sum "$1" | awk '{print $1}')
    fi
}

get_md5 "${MINT_DATA_DIR}/datafile-1-kB"
HASH_1_KB=$md5rt

get_md5 "${MINT_DATA_DIR}/datafile-65-MB"
HASH_65_MB=$md5rt

_init() {
    AWS="aws --endpoint-url $1"
}


function get_time() {
    date +%s%N
}

function get_duration() {
    start_time=$1
    end_time=$(get_time)

    echo $(( (end_time - start_time) / 1000000 ))
}

function log_success() {
    function=$(python -c 'import sys,json; print(json.dumps(sys.stdin.read()))' <<<"$2")
    printf '{"name": "awscli", "duration": %d, "function": %s, "status": "PASS"}\n' "$1" "$function"
}

function log_failure() {
    function=$(python -c 'import sys,json; print(json.dumps(sys.stdin.read()))' <<<"$2")
    err=$(echo "$3" | tr -d '\n')
    printf '{"name": "awscli", "duration": %d, "function": %s, "status": "FAIL", "error": "%s"}\n' "$1" "$function" "$err"
}

function log_alert() {
    function=$(python -c 'import sys,json; print(json.dumps(sys.stdin.read()))' <<<"$2")
    err=$(echo "$4" | tr -d '\n')
    printf '{"name": "awscli", "duration": %d, "function": %s, "status": "FAIL", "alert": "%s", "error": "%s"}\n' "$1" "$function" "$3" "$err"
}

function make_bucket() {
    # Make bucket
    bucket_name="awscli-mint-test-bucket-$RANDOM"
    function="${AWS} s3api create-bucket --bucket ${bucket_name}"

    # execute the test
    out=$($function 2>&1)
    rv=$?

    # if command is successful print bucket_name or print error
    if [ $rv -eq 0 ]; then
        echo "${bucket_name}"
    else
        echo "${out}"
    fi

    return $rv
}

function delete_bucket() {
    # Delete bucket
    function="${AWS} s3 rb s3://${1} --force"
    out=$($function 2>&1)
    rv=$?

    # echo the output
    echo "${out}"

    return $rv
}

# Tests creating, stat and delete on a bucket.
function test_create_bucket() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?
    # save the ref to function being tested, so it can be logged
    test_function=${function}

    # if make_bucket is successful stat the bucket
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api head-bucket --bucket ${bucket_name}"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket failes, $bucket_name has the error output
        out="${bucket_name}"
    fi

     # if stat bucket is successful remove the bucket
    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "${bucket_name}")
        rv=$?
    else
        # if make bucket failes, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests creating and deleting an object.
function test_upload_object() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # if upload succeeds download the file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api get-object --bucket ${bucket_name} --key datafile-1-kB /tmp/datafile-1-kB"
        # save the ref to function being tested, so it can be logged
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        # calculate the md5 hash of downloaded file
        get_md5 "/tmp/datafile-1-kB"
        hash2=$md5rt
    fi

    # if download succeeds, verify downloaded file
    if [ $rv -eq 0 ]; then
        if [ "$HASH_1_KB" == "$hash2" ]; then
            function="delete_bucket"
            out=$(delete_bucket "$bucket_name")
            rv=$?
            # remove download file
            rm -f /tmp/datafile-1-kB
        else
            rv=1
            out="Checksum verification failed for uploaded object"
        fi
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Test lookup a directory prefix.
function test_lookup_object_prefix() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds create a directory.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --bucket ${bucket_name} --key prefix/directory/"
        # save the ref to function being tested, so it can be logged
        test_function=${function}

        out=$($function 2>&1)

        rv=$?
    else
        # if make_bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        ## Attempt an overwrite of the prefix again and should succeed as well.
        function="${AWS} s3api put-object --bucket ${bucket_name} --key prefix/directory/"
        # save the ref to function being tested, so it can be logged
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    # if upload succeeds lookup for the prefix.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api head-object --bucket ${bucket_name} --key prefix/directory/"
        # save the ref to function being tested, so it can be logged
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    # if directory create succeeds, upload the object.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key prefix/directory/datafile-1-kB"
        # save the ref to function being tested, so it can be logged
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    # Attempt a delete on prefix shouldn't delete the directory since we have an object inside it.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api delete-object --bucket ${bucket_name} --key prefix/directory/"
        # save the ref to function being tested, so it can be logged
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    # if upload succeeds lookup for the object should succeed.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api head-object --bucket ${bucket_name} --key prefix/directory/datafile-1-kB"
        # save the ref to function being tested, so it can be logged
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    # delete bucket
    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
    fi

    if [ $rv -ne 0 ]; then
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    else
        log_success "$(get_duration "$start_time")" "${test_function}"
    fi

    return $rv
}

# Tests listing objects for both v1 and v2 API.
function test_list_objects() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # if upload objects succeeds, list objects with existing prefix
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api list-objects --bucket ${bucket_name} --prefix datafile-1-kB"
        test_function=${function}
        out=$($function)
        rv=$?
        key_name=$(echo "$out" | jq -r .Contents[].Key)
        if [ $rv -eq 0 ] && [ "$key_name" != "datafile-1-kB" ]; then
            rv=1
            # since rv is 0, command passed, but didn't return expected value. In this case set the output
            out="list-objects with existing prefix failed"
        fi
    fi

    # if upload objects succeeds, list objects without existing prefix
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api list-objects --bucket ${bucket_name} --prefix linux"
        out=$($function)
        rv=$?
        key_name=$(echo "$out" | jq -r .Contents[].Key)
        if [ $rv -eq 0 ] && [ "$key_name" != "" ]; then
            rv=1
            out="list-objects without existing prefix failed"
        fi
    fi

    # if upload objects succeeds, list objectsv2 with existing prefix
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api list-objects-v2 --bucket ${bucket_name} --prefix datafile-1-kB"
        out=$($function)
        rv=$?
        key_name=$(echo "$out" | jq -r .Contents[].Key)
        if [ $rv -eq 0 ] && [ "$key_name" != "datafile-1-kB" ]; then
            rv=1
            out="list-objects-v2 with existing prefix failed"
        fi
    fi

    # if upload objects succeeds, list objectsv2 without existing prefix
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api list-objects-v2 --bucket ${bucket_name} --prefix linux"
        out=$($function)
        rv=$?
        key_name=$(echo "$out" | jq -r .Contents[].Key)
        if [ $rv -eq 0 ] && [ "$key_name" != "" ]; then
            rv=1
            out="list-objects-v2 without existing prefix failed"
        fi
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
        # remove download file
        rm -f /tmp/datafile-1-kB
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        rm -f /tmp/datafile-1-kB
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests multipart API with 0 byte part.
function test_multipart_upload_0byte() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    object_name=${bucket_name}"-object"
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-0-b --bucket ${bucket_name} --key datafile-0-b"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        # create multipart
        function="${AWS} s3api create-multipart-upload --bucket ${bucket_name} --key ${object_name}"
        test_function=${function}
        out=$($function)
        rv=$?
        upload_id=$(echo "$out" | jq -r .UploadId)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 1
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-0-b --upload-id ${upload_id} --part-number 1"
        out=$($function)
        rv=$?
        etag1=$(echo "$out" | jq -r .ETag)
    fi

    if [ $rv -eq 0 ]; then
        # Create a multipart struct file for completing multipart transaction
        echo "{
            \"Parts\": [
                {
                    \"ETag\": ${etag1},
                    \"PartNumber\": 1
                }
            ]
        }" >> /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        # Use saved etags to complete the multipart transaction
        function="${AWS} s3api complete-multipart-upload --multipart-upload file:///tmp/multipart --bucket ${bucket_name} --key ${object_name} --upload-id ${upload_id}"
        out=$($function)
        rv=$?
        etag=$(echo "$out" | jq -r .ETag | sed -e 's/^"//' -e 's/"$//')
        if [ "${etag}" == "" ]; then
            rv=1
            out="complete-multipart-upload failed"
        fi
    fi

    if [ $rv -eq 0 ]; then
        function="${AWS} s3api get-object --bucket ${bucket_name} --key ${object_name} /tmp/datafile-0-b"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        ret_etag=$(echo "$out" | jq -r .ETag | sed -e 's/^"//' -e 's/"$//')
        # match etag
        if [ "$etag" != "$ret_etag" ]; then
            rv=1
            out="Etag mismatch for multipart 0 byte object"
        fi
        rm -f /tmp/datafile-0-b
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
        # remove temp file
        rm -f /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        rm -f /tmp/multipart
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests multipart API by making each individual calls.
function test_multipart_upload() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    object_name=${bucket_name}"-object"
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        # create multipart
        function="${AWS} s3api create-multipart-upload --bucket ${bucket_name} --key ${object_name}"
        test_function=${function}
        out=$($function)
        rv=$?
        upload_id=$(echo "$out" | jq -r .UploadId)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 1
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-5-MB --upload-id ${upload_id} --part-number 1"
        out=$($function)
        rv=$?
        etag1=$(echo "$out" | jq -r .ETag)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 2
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-1-kB --upload-id ${upload_id} --part-number 2"
        out=$($function)
        rv=$?
        etag2=$(echo "$out" | jq -r .ETag)
        # Create a multipart struct file for completing multipart transaction
        echo "{
            \"Parts\": [
                {
                    \"ETag\": ${etag1},
                    \"PartNumber\": 1
                },
                {
                    \"ETag\": ${etag2},
                    \"PartNumber\": 2
                }
            ]
        }" >> /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        # Use saved etags to complete the multipart transaction
        function="${AWS} s3api complete-multipart-upload --multipart-upload file:///tmp/multipart --bucket ${bucket_name} --key ${object_name} --upload-id ${upload_id}"
        out=$($function)
        rv=$?
        finalETag=$(echo "$out" | jq -r .ETag | sed -e 's/^"//' -e 's/"$//')
        if [ "${finalETag}" == "" ]; then
            rv=1
            out="complete-multipart-upload failed"
        fi
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
        # remove temp file
        rm -f /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        rm -f /tmp/multipart
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# List number of objects based on the maxKey
# value set.
function test_max_key_list() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-b --bucket ${bucket_name} --key datafile-1-b"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # copy object server side
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api copy-object --bucket ${bucket_name} --key datafile-1-b-copy --copy-source ${bucket_name}/datafile-1-b"
        out=$($function)
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        function="${AWS} s3api list-objects-v2 --bucket ${bucket_name} --max-keys 1"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        if [ $rv -eq 0 ]; then
            out=$(echo "$out" | jq '.KeyCount')
            rv=$?
        fi
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
        # The command passed, but the delete_bucket failed
        out="delete_bucket for test_max_key_list failed"
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Copy object tests for server side copy
# of the object, validates returned md5sum.
function test_copy_object() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # copy object server side
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api copy-object --bucket ${bucket_name} --key datafile-1-kB-copy --copy-source ${bucket_name}/datafile-1-kB"
        test_function=${function}
        out=$($function)
        rv=$?
        hash2=$(echo "$out" | jq -r .CopyObjectResult.ETag | sed -e 's/^"//' -e 's/"$//')
        if [ $rv -eq 0 ] && [ "$HASH_1_KB" != "$hash2" ]; then
            # Verification failed
            rv=1
            out="Hash mismatch expected $HASH_1_KB, got $hash2"
        fi
    fi

    ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Copy object tests for server side copy
# of the object, validates returned md5sum.
# validates change in storage class as well
function test_copy_object_storage_class() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # copy object server side
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api copy-object --bucket ${bucket_name} --storage-class REDUCED_REDUNDANCY --key datafile-1-kB-copy --copy-source ${bucket_name}/datafile-1-kB"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        # if this functionality is not implemented return right away.
        if [ $rv -ne 0 ]; then
            if echo "$out" | grep -q "NotImplemented"; then
                ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
                return 0
            fi
        fi
        hash2=$(echo "$out" | jq -r .CopyObjectResult.ETag | sed -e 's/^"//' -e 's/"$//')
        if [ $rv -eq 0 ] && [ "$HASH_1_KB" != "$hash2" ]; then
            # Verification failed
            rv=1
            out="Hash mismatch expected $HASH_1_KB, got $hash2"
        fi
    fi

    ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Copy object tests for server side copy
# to itself by changing storage class
function test_copy_object_storage_class_same() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # copy object server side
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api copy-object --bucket ${bucket_name} --storage-class REDUCED_REDUNDANCY --key datafile-1-kB --copy-source ${bucket_name}/datafile-1-kB"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        # if this functionality is not implemented return right away.
        if [ $rv -ne 0 ]; then
            if echo "$out" | grep -q "NotImplemented"; then
                ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
                return 0
            fi
        fi
        hash2=$(echo "$out" | jq -r .CopyObjectResult.ETag | sed -e 's/^"//' -e 's/"$//')
        if [ $rv -eq 0 ] && [ "$HASH_1_KB" != "$hash2" ]; then
            # Verification failed
            rv=1
            out="Hash mismatch expected $HASH_1_KB, got $hash2"
        fi
    fi

    ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests for presigned URL success case, presigned URL
# is correct and accessible - we calculate md5sum of
# the object and validate it against a local files md5sum.
function test_presigned_object() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        function="${AWS} s3 presign s3://${bucket_name}/datafile-1-kB"
        test_function=${function}
        url=$($function)
        rv=$?
        curl -sS -X GET "${url}" > /tmp/datafile-1-kB
        get_md5 /tmp/datafile-1-kB
        hash2=$md5rt
        if [ "$HASH_1_KB" == "$hash2" ]; then
            function="delete_bucket"
            out=$(delete_bucket "$bucket_name")
            rv=$?
            # remove download file
            rm -f /tmp/datafile-1-kB
        else
            rv=1
            out="Checksum verification failed for downloaded object"
        fi
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests creating and deleting an object - 10MiB
function test_upload_object_10() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-10-MB --bucket ${bucket_name} --key datafile-10-MB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests multipart API by making each individual calls with 10MiB part size.
function test_multipart_upload_10() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    object_name=${bucket_name}"-object"
    rv=$?

    if [ $rv -eq 0 ]; then
        # create multipart
        function="${AWS} s3api create-multipart-upload --bucket ${bucket_name} --key ${object_name}"
        test_function=${function}
        out=$($function)
        rv=$?
        upload_id=$(echo "$out" | jq -r .UploadId)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 1
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-10-MB --upload-id ${upload_id} --part-number 1"
        out=$($function)
        rv=$?
        etag1=$(echo "$out" | jq -r .ETag)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 2
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-10-MB --upload-id ${upload_id} --part-number 2"
        out=$($function)
        rv=$?
        etag2=$(echo "$out" | jq -r .ETag)
        # Create a multipart struct file for completing multipart transaction
        echo "{
            \"Parts\": [
                {
                    \"ETag\": ${etag1},
                    \"PartNumber\": 1
                },
                {
                    \"ETag\": ${etag2},
                    \"PartNumber\": 2
                }
            ]
        }" >> /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        # Use saved etags to complete the multipart transaction
        function="${AWS} s3api complete-multipart-upload --multipart-upload file:///tmp/multipart --bucket ${bucket_name} --key ${object_name} --upload-id ${upload_id}"
        out=$($function)
        rv=$?
        finalETag=$(echo "$out" | jq -r .ETag | sed -e 's/^"//' -e 's/"$//')
        if [ "${finalETag}" == "" ]; then
            rv=1
            out="complete-multipart-upload failed"
        fi
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
        # remove temp file
        rm -f /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        rm -f /tmp/multipart
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests lifecycle of a bucket.
function test_bucket_lifecycle() {
    # log start time
    start_time=$(get_time)

    echo "{ \"Rules\": [ { \"Expiration\": { \"Days\": 365 },\"ID\": \"Bucketlifecycle test\", \"Filter\": { \"Prefix\": \"\" }, \"Status\": \"Enabled\" } ] }" >> /tmp/lifecycle.json

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds put bucket lifecycle
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-bucket-lifecycle-configuration --bucket ${bucket_name} --lifecycle-configuration file:///tmp/lifecycle.json"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -ne 0 ]; then
        # if this functionality is not implemented return right away.
        if echo "$out" | grep -q "NotImplemented"; then
            ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
            return 0
        fi
    fi

    # if put bucket lifecycle succeeds get bucket lifecycle
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api get-bucket-lifecycle-configuration --bucket ${bucket_name}"
        out=$($function 2>&1)
        rv=$?
    fi

    # if get bucket lifecycle succeeds delete bucket lifecycle
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api delete-bucket-lifecycle --bucket ${bucket_name}"
        out=$($function 2>&1)
        rv=$?
    fi

    # delete lifecycle.json
    rm -f /tmp/lifecycle.json

    # delete bucket
    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "test_bucket_lifecycle"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests `aws s3 cp` by uploading a local file.
function test_aws_s3_cp() {
    file_name="${MINT_DATA_DIR}/datafile-65-MB"

    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file using cp
    if [ $rv -eq 0 ]; then
        function="${AWS} s3 cp $file_name s3://${bucket_name}/$(basename "$file_name")"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        function="${AWS} s3 rm s3://${bucket_name}/$(basename "$file_name")"
        out=$($function 2>&1)
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        function="${AWS} s3 rb s3://${bucket_name}/"
        out=$($function 2>&1)
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# Tests `aws s3 sync` by mirroring all the
# local content to remove bucket.
function test_aws_s3_sync() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds sync all the files in a directory
    if [ $rv -eq 0 ]; then
        function="${AWS} s3 sync --no-progress $MINT_DATA_DIR s3://${bucket_name}/"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # remove files recusively
    if [ $rv -eq 0 ]; then
        function="${AWS} s3 rm --recursive s3://${bucket_name}/"
        out=$($function 2>&1)
        rv=$?
    fi

    # delete bucket
    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# list objects negative test - tests for following conditions.
# v1 API with max-keys=-1 and max-keys=0
# v2 API with max-keys=-1 and max-keys=0
function test_list_objects_error() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    if [ $rv -eq 0 ]; then
        # Server replies an error for v1 with max-key=-1
        function="${AWS} s3api list-objects --bucket ${bucket_name} --prefix datafile-1-kB --max-keys=-1"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        if [ $rv -ne $errno ]; then
            rv=1
        else
            rv=0
        fi
    fi

    if [ $rv -eq 0 ]; then
        # Server replies an error for v2 with max-keys=-1
        function="${AWS} s3api list-objects-v2 --bucket ${bucket_name} --prefix datafile-1-kB --max-keys=-1"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        if [ $rv -ne $errno ]; then
            rv=1
        else
            rv=0
        fi
    fi

    if [ $rv -eq 0 ]; then
        # Server returns success with no keys when max-keys=0
        function="${AWS} s3api list-objects-v2 --bucket ${bucket_name} --prefix datafile-1-kB --max-keys=0"
        out=$($function 2>&1)
        rv=$?
        if [ $rv -eq 0 ]; then
            function="delete_bucket"
            out=$(delete_bucket "$bucket_name")
            rv=$?
        fi
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# put object negative test - tests for following conditions.
# - invalid object name.
# - invalid Content-Md5
# - invalid Content-Length
function test_put_object_error() {
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload an object without content-md5.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB --content-md5 invalid"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        if [ $rv -ne $errno ]; then
            rv=1
        else
            rv=0
        fi
    fi

    # upload an object without content-length.
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB --content-length -1"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
        if [ $rv -ne $errno ]; then
            rv=1
        else
            rv=0
        fi
    fi

    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}
# tests server side encryption headers for get and put calls
function test_serverside_encryption() {
    #skip server side encryption tests if HTTPS disabled.
    if [ "$ENABLE_HTTPS" != "1" ]; then
        return 0
    fi
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # put object with server side encryption headers
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    # now get encrypted object from server
    if [ $rv -eq 0 ]; then
        etag1=$(echo "$out" | jq -r .ETag)
        sse_customer_key1=$(echo "$out" | jq -r .SSECustomerKeyMD5)
        sse_customer_algo1=$(echo "$out" | jq -r .SSECustomerAlgorithm)

        function="${AWS} s3api get-object --bucket ${bucket_name} --key datafile-1-kB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg== /tmp/datafile-1-kB"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    if [ $rv -eq 0 ]; then
        etag2=$(echo "$out" | jq -r .ETag)
        sse_customer_key2=$(echo "$out" | jq -r .SSECustomerKeyMD5)
        sse_customer_algo2=$(echo "$out" | jq -r .SSECustomerAlgorithm)
        get_md5 "/tmp/datafile-1-kB"
        hash2=$md5rt
        # match downloaded object's hash to original
        if [ "$HASH_1_KB" == "$hash2" ]; then
            function="delete_bucket"
            out=$(delete_bucket "$bucket_name")
            rv=$?
            # remove download file
            rm -f /tmp/datafile-1-kB
        else
            rv=1
            out="Checksum verification failed for downloaded object"
        fi
        # match etag and SSE headers
        if [ "$etag1" != "$etag2" ]; then
            rv=1
            out="Etag mismatch for object encrypted with server side encryption"
        fi
        if [ "$sse_customer_algo1" != "$sse_customer_algo2" ]; then
            rv=1
            out="sse customer algorithm mismatch"
        fi
        if [ "$sse_customer_key1" != "$sse_customer_key2" ]; then
            rv=1
            out="sse customer key mismatch"
        fi
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# tests server side encryption headers for multipart put
function test_serverside_encryption_multipart() {
    #skip server side encryption tests if HTTPS disabled.
    if [ "$ENABLE_HTTPS" != "1" ]; then
        return 0
    fi
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # put object with server side encryption headers
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-65-MB --bucket ${bucket_name} --key datafile-65-MB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    # now get encrypted object from server
    if [ $rv -eq 0 ]; then
        etag1=$(echo "$out" | jq -r .ETag)
        sse_customer_key1=$(echo "$out" | jq -r .SSECustomerKeyMD5)
        sse_customer_algo1=$(echo "$out" | jq -r .SSECustomerAlgorithm)

        function="${AWS} s3api get-object --bucket ${bucket_name} --key datafile-65-MB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg== /tmp/datafile-65-MB"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    if [ $rv -eq 0 ]; then
        etag2=$(echo "$out" | jq -r .ETag)
        sse_customer_key2=$(echo "$out" | jq -r .SSECustomerKeyMD5)
        sse_customer_algo2=$(echo "$out" | jq -r .SSECustomerAlgorithm)
        get_md5 "${MINT_DATA_DIR}/datafile-65-MB"
        hash2=$md5rt
        # match downloaded object's hash to original
        if [ "$HASH_65_MB" == "$hash2" ]; then
            function="delete_bucket"
            out=$(delete_bucket "$bucket_name")
            rv=$?
            # remove download file
            rm -f /tmp/datafile-65-MB
        else
            rv=1
            out="Checksum verification failed for downloaded object"
        fi
        # match etag and SSE headers
        if [ "$etag1" != "$etag2" ]; then
            rv=1
            out="Etag mismatch for object encrypted with server side encryption"
        fi
        if [ "$sse_customer_algo1" != "$sse_customer_algo2" ]; then
            rv=1
            out="sse customer algorithm mismatch"
        fi
        if [ "$sse_customer_key1" != "$sse_customer_key2" ]; then
            rv=1
            out="sse customer key mismatch"
        fi
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}

# tests encrypted copy from multipart encrypted object to
# single part encrypted object. This test in particular checks if copy
# succeeds for the case where encryption overhead for individually
# encrypted parts vs encryption overhead for the original datastream
# differs.
function test_serverside_encryption_multipart_copy() {
    #skip server side encryption tests if HTTPS disabled.
    if [ "$ENABLE_HTTPS" != "1" ]; then
        return 0
    fi
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    object_name=${bucket_name}"-object"
    rv=$?

    if [ $rv -eq 0 ]; then
        # create multipart
        function="${AWS} s3api create-multipart-upload --bucket ${bucket_name} --key ${object_name} --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        out=$($function)
        rv=$?
        upload_id=$(echo "$out" | jq -r .UploadId)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 1
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-5243880-b --upload-id ${upload_id} --part-number 1 --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        out=$($function)
        rv=$?
        etag1=$(echo "$out" | jq -r .ETag)
    fi

    if [ $rv -eq 0 ]; then
        # Capture etag for part-number 2
        function="${AWS} s3api upload-part --bucket ${bucket_name} --key ${object_name} --body ${MINT_DATA_DIR}/datafile-5243880-b --upload-id ${upload_id} --part-number 2 --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        out=$($function)
        rv=$?
        etag2=$(echo "$out" | jq -r .ETag)
        # Create a multipart struct file for completing multipart transaction
        echo "{
            \"Parts\": [
                {
                    \"ETag\": ${etag1},
                    \"PartNumber\": 1
                },
                {
                    \"ETag\": ${etag2},
                    \"PartNumber\": 2
                }
            ]
        }" >> /tmp/multipart
    fi

    if [ $rv -eq 0 ]; then
        # Use saved etags to complete the multipart transaction
        function="${AWS} s3api complete-multipart-upload --multipart-upload file:///tmp/multipart --bucket ${bucket_name} --key ${object_name} --upload-id ${upload_id}"
        out=$($function)
        rv=$?
        finalETag=$(echo "$out" | jq -r .ETag | sed -e 's/^"//' -e 's/"$//')
        if [ "${finalETag}" == "" ]; then
            rv=1
            out="complete-multipart-upload failed"
        fi
    fi

     # copy object server side
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api copy-object --bucket ${bucket_name} --key ${object_name}-copy --copy-source ${bucket_name}/${object_name} --copy-source-sse-customer-algorithm AES256 --copy-source-sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --copy-source-sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg== --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        test_function=${function}
        out=$($function)
        rv=$?
        if [ $rv -ne $errno ]; then
            rv=1
        else
            rv=0
        fi
    fi

    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        rm -f /tmp/multipart
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}
# tests server side encryption headers for range get calls
function test_serverside_encryption_get_range() {
    #skip server side encryption tests if HTTPS disabled.
    if [ "$ENABLE_HTTPS" != "1" ]; then
        return 0
    fi
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?
    # put object with server side encryption headers
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-10-kB --bucket ${bucket_name} --key datafile-10-kB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    # now get encrypted object from server for range 500-999
    if [ $rv -eq 0 ]; then
        etag1=$(echo "$out" | jq -r .ETag)
        sse_customer_key1=$(echo "$out" | jq -r .SSECustomerKeyMD5)
        sse_customer_algo1=$(echo "$out" | jq -r .SSECustomerAlgorithm)
        function="${AWS} s3api get-object --bucket ${bucket_name} --key datafile-10-kB --range bytes=500-999 --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg== /tmp/datafile-10-kB"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    if [ $rv -eq 0 ]; then
        cnt=$(stat -c%s /tmp/datafile-10-kB)
        if [ "$cnt" -ne 500 ]; then
            rv=1
        fi
    fi
    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi
    return $rv
}

# tests server side encryption error for get and put calls
function test_serverside_encryption_error() {
    #skip server side encryption tests if HTTPS disabled.
    if [ "$ENABLE_HTTPS" != "1" ]; then
        return 0
    fi
    # log start time
    start_time=$(get_time)

    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # put object with server side encryption headers  with MD5Sum mismatch for sse-customer-key-md5 header
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    if [ $rv -ne $errno ]; then
        rv=1
    else
        rv=0
    fi
    # put object with missing server side encryption header sse-customer-algorithm
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB  --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    if [ $rv -ne $errno ]; then
        rv=1
    else
        rv=0
    fi

    # put object with server side encryption headers successfully
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc2xvbmdzZWNyZXRrZXltdXN0cHJvdmlkZWQ= --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg=="
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi

    # now test get on encrypted object with nonmatching sse-customer-key and sse-customer-md5 headers
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api get-object --bucket ${bucket_name} --key datafile-1-kB --sse-customer-algorithm AES256 --sse-customer-key MzJieXRlc --sse-customer-key-md5 7PpPLAK26ONlVUGOWlusfg== /tmp/datafile-1-kB"
        test_function=${function}
        out=$($function 2>&1)
        rv=$?
    fi
    if [ $rv -ne $errno ]; then
        rv=1
    else
        rv=0
    fi
    # delete bucket
    if [ $rv -eq 0 ]; then
        function="delete_bucket"
        out=$(delete_bucket "$bucket_name")
        rv=$?
    fi
    if [ $rv -eq 0 ]; then
        log_success "$(get_duration "$start_time")" "${test_function}"
    else
        # clean up and log error
        ${AWS} s3 rb s3://"${bucket_name}" --force > /dev/null 2>&1
        log_failure "$(get_duration "$start_time")" "${function}" "${out}"
    fi

    return $rv
}


# test GetObjectInfo http code is 404
function test_get_object_error(){
    # log start time
    start_time=$(get_time)
    function="make_bucket"
    bucket_name=$(make_bucket)
    rv=$?

    # if make bucket succeeds upload a file
    if [ $rv -eq 0 ]; then
        function="${AWS} s3api put-object --body ${MINT_DATA_DIR}/datafile-1-kB --bucket ${bucket_name} --key datafile-1-kB"
        out=$($function 2>&1)
        rv=$?
    else
        # if make bucket fails, $bucket_name has the error output
        out="${bucket_name}"
    fi

    # if upload succeeds download the file
        if [ $rv -eq 0 ]; then
            function="${AWS} s3api get-object --bucket ${bucket_name} --key datafile-1-kB/ /tmp/datafile-1-kB"
            # save the ref to function being tested, so it can be logged
            test_function=${function}
            out=$($function 2>&1)
            if [ $? -eq $errno ];then
                rv=0
            fi
            if ! [[ "$out" =~ "The specified key does not exist" ]];then
                log_failure "$(get_duration "$start_time")" "${function}" "${out}"
                rv=1
            fi
        fi
    return $rv
}


# main handler for all the tests.
main() {
    # Success tests
    test_create_bucket && \
    test_upload_object && \
    test_lookup_object_prefix && \
    test_list_objects && \
    test_multipart_upload_0byte && \
    test_multipart_upload && \
    test_max_key_list && \
    test_copy_object && \
    test_copy_object_storage_class && \
    test_copy_object_storage_class_same && \
    test_presigned_object && \
    test_upload_object_10 && \
    test_multipart_upload_10 && \
#     test_bucket_lifecycle && \
    test_serverside_encryption && \
    test_serverside_encryption_get_range && \
    test_serverside_encryption_multipart && \
    test_serverside_encryption_multipart_copy && \
    # Success cli ops.
    test_aws_s3_cp && \
    test_aws_s3_sync && \
    # Error tests
    test_list_objects_error && \
    test_put_object_error && \
    test_serverside_encryption_error && \
    # test_worm_bucket && \
    # test_legal_hold
    test_get_object_error

    return $?
}

_init "$endpoint" && main
