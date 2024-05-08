#/bin/bash -e

save_benchmark(){
    while [[ $# -gt 0 ]]; do
        key="$1"
        case $key in
            --name)
                name="$2"
                shift
                ;;
            --result)
                result="$2"
                shift
                ;;
            --meta)
                meta="$2"
                shift
                ;;
            --storage)
                storage="$2"
                shift
                ;;
            --extra)
                extra="$2"
                shift
                ;;
            *)
                # Unknown option
                ;;
        esac
        shift
    done
    [[ -z $name ]] && echo "name is required" && exit 1
    [[ -z $result ]] && echo "result is required" && exit 1
    [[ -z $meta ]] && echo "meta is required" && exit 1
    [[ -z $storage ]] && storage='unknown'

    version=$(./juicefs -V | cut -b 17- | sed 's/:/-/g')
    created_date=$(date +"%Y-%m-%d")
    cat <<EOF > result.json
    {
        "workflow": "$GITHUB_WORKFLOW",
        "name": "$name",
        "result": "$result",
        "meta": "$meta",
        "storage": "$storage",
        "extra": "$extra",
        "version": "$version",
        "created_date": "$created_date",
        "github_repo": "$GITHUB_REPOSITORY",
        "github_ref_name": "$GITHUB_REF_NAME",
        "github_run_id": "$GITHUB_RUN_ID",
        "github_sha": "$GITHUB_SHA",
        "workflow_url": "https://github.com/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID",
    }
EOF
    cat result.json
    if [[ "$GITHUB_EVENT_NAME" == "schedule" || "$GITHUB_EVENT_NAME" == "workflow_dispatch"   ]]; then
        echo "save benchmark"
        AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY ./juicefs sync --force-update result.json s3://juicefs-ci-aws.s3.us-east-1.amazonaws.com/ci-report/$GITHUB_WORKFLOW/$name/$created_date/$meta-$storage.json
    fi
}

save_benchmark $@
