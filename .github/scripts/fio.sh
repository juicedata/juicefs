#/bin/bash -e 
get_fio_job_options(){
    fio_job_name=$1
    case "$fio_job_name" in
        "big-file-sequential-read") fio_job="big-file-sequential-read:  --rw=read --refill_buffers --bs=256k --size=1G"
        ;;
        "big-file-sequential-write") fio_job="big-file-sequential-write:  --rw=write --refill_buffers --bs=256k  --size=1G"
        ;;
        "big-file-multi-read-1") fio_job="big-file-multi-read-1:  --rw=read --refill_buffers --bs=256k --size=1G --numjobs=1"
        ;;
        "big-file-multi-read-4") fio_job="big-file-multi-read-4:  --rw=read --refill_buffers --bs=256k --size=1G --numjobs=4"
        ;;
        "big-file-multi-read-16") fio_job="big-file-multi-read-16:  --rw=read --refill_buffers --bs=256k --size=1G --numjobs=16"
        ;;
        "big-file-multi-write-1") fio_job="big-file-multi-write-1:       --rw=write --refill_buffers --bs=256k --size=1G --numjobs=1"
        ;;
        "big-file-multi-write-4") fio_job="big-file-multi-write-4:       --rw=write --refill_buffers --bs=256k --size=1G --numjobs=4"
        ;;
        "big-file-multi-write-16") fio_job="big-file-multi-write-16:       --rw=write --refill_buffers --bs=256k --size=1G --numjobs=16"
        ;;
        "big-file-rand-read-4k") fio_job="big-file-rand-read-4k:       --rw=randread --refill_buffers --size=1G --filename=randread.bin --bs=4k"
        ;;
        "big-file-rand-read-256k") fio_job="big-file-rand-read-256k:       --rw=randread --refill_buffers --size=1G --filename=randread.bin --bs=256k"
        ;;
        "big-file-random-write-16k") fio_job="big-file-random-write-16k:    --rw=randwrite --refill_buffers --size=1G --bs=16k"
        ;;
        "big-file-random-write-256k") fio_job="big-file-random-write-256k:    --rw=randwrite --refill_buffers --size=1G --bs=256k"
        ;;
        "small-file-seq-read-4k") fio_job="small-file-seq-read-4k:      --rw=read --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 :--cache-size=0"
        ;;
        "small-file-seq-read-256k") fio_job="small-file-seq-read-256k:      --rw=read --file_service_type=sequential --bs=256k --filesize=256k --nrfiles=10000 :--cache-size=0"
        ;;
        "small-file-seq-write-4k") fio_job="small-file-seq-write-4k:     --rw=write --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 :--writeback"
        ;;
        "small-file-seq-write-256k") fio_job="small-file-seq-write-256k:     --rw=write --file_service_type=sequential --bs=256k --filesize=256k --nrfiles=10000 :--writeback"
        ;;
        "small-file-multi-read-1") fio_job="small-file-multi-read-1:      --rw=read --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 --numjobs=1"
        ;;
        "small-file-multi-read-4") fio_job="small-file-multi-read-4:      --rw=read --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 --numjobs=4"
        ;;
        "small-file-multi-read-16") fio_job="small-file-multi-read-16:      --rw=read --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 --numjobs=16"
        ;;
        "small-file-multi-write-1") fio_job="small-file-multi-write-1:     --rw=write --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 --numjobs=1"
        ;;
        "small-file-multi-write-4") fio_job="small-file-multi-write-4:     --rw=write --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 --numjobs=4"
        ;;
        "small-file-multi-write-16") fio_job="small-file-multi-write-16:     --rw=write --file_service_type=sequential --bs=4k --filesize=4k --nrfiles=10000 --numjobs=16"
        ;;
    esac
    echo $fio_job
}
parse_bandwidth(){
    echo "parse bandwidth"  >&2
    cat fio.log 1>&2
    bw_str=$(tail -1 fio.log | awk '{print $2}' | awk -F '=' '{print $2}' )
    echo bw_str is $bw_str  >&2
    bw=$(echo $bw_str | sed 's/.iB.*//g') 
    if [[ $bw_str == *KiB* ]]; then
        bw=$(echo "scale=2; $bw/1024.0" | bc -l)
    elif [[ $bw_str == *GiB* ]]; then
        bw=$(echo "scale=2; $bw*1024.0" | bc -l)
    fi
    echo bw is $bw  >&2
    echo $bw 
}
          
fio_test()
{
    meta_url=$1
    fio_job_name=$2
    echo "Fio Benchmark"
    fio_job_options=$(get_fio_job_options $fio_job_name)
    echo fio_job_options is $fio_job_options
    name=$(echo $fio_job_options | awk -F: '{print $1}' | xargs)
    fio_arg=$(echo $fio_job_options | awk -F: '{print $2}' | xargs)
    mount_arg=$(echo $fio_job_options | awk -F: '{print $3}' | xargs)
    ./juicefs format --trash-days 0 --storage minio --bucket http://localhost:9000/fio --access-key minioadmin --secret-key minioadmin $meta_url fio
    ./juicefs mount -d $meta_url /tmp/jfs --no-usage-report $mount_arg
    if [[ "$name" =~ ^big-file-rand-read.* ]]; then
        block_size=$(echo $name | awk -F- '{print $NF}' | xargs)
        echo block_size is $block_size
        fio --name=big-file-rand-read-preload --directory=/tmp/jfs --rw=randread --refill_buffers --size=1G --filename=randread.bin --bs=$block_size --pre_read=1
        sudo sync 
        sudo bash -c  "echo 3 > /proc/sys/vm/drop_caches"
    fi
    echo "start fio"
    fio --name=$name --directory=/tmp/jfs $fio_arg | tee "fio.log"
    echo "finish fio"
    ./juicefs umount -f /tmp/jfs
    uuid=$(./juicefs status $meta_url | grep UUID | cut -d '"' -f 4)
    if [ -n "$uuid" ]; then
        sudo ./juicefs destroy --yes $meta_url $uuid
    fi
}
meta_url=$1
name=$2
fio_test $meta_url $name
bandwidth=$(parse_bandwidth)
echo bandwidth is $bandwidth
[[ -z "$bandwidth" ]] && echo "bandwidth is empty" && exit 1
meta=$(echo $meta_url | awk -F: '{print $1}')
echo meta is $meta
[[ -z "$meta" ]] && echo "meta is empty" && exit 1
.github/scripts/save_benchmark.sh --name $name --result $bandwidth --meta $meta --storage minio