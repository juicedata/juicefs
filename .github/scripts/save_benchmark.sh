#/bin/bash -e

mount_jfs(){
    mkdir -p /root/.juicefs
    wget -q s.juicefs.com/static/Linux/mount -O /root/.juicefs/jfsmount 
    chmod +x /root/.juicefs/jfsmount
    curl -s -L https://juicefs.com/static/juicefs -o /usr/local/bin/juicefs && sudo chmod +x /usr/local/bin/juicefs
    juicefs auth ci-coverage --access-key $AWS_ACEESS_KEY --secret-key $AWS_SECRET_KEY --token $AWS_ACCESS_TOKEN --encrypt-keys
    juicefs mount ci-coverage --subdir juicefs/ci-benchmark/ --allow-other /ci-benchmark
}  

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
        mount_jfs
        echo "save result.json to /ci-benchmark/$GITHUB_WORKFLOW/$name/$created_date/$meta-$storage.json"
        mkdir -p /ci-benchmark/$GITHUB_WORKFLOW/$name/$created_date/
        cp result.json /ci-benchmark/$GITHUB_WORKFLOW/$name/$created_date/$meta-$storage.json
    fi
}

save_benchmark $@
