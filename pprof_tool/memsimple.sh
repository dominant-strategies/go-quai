#!/bin/bash

convert_to_kilobytes() {
    value=$1
    case $value in
        *K) printf "%.0f" ${value%K} ;;
        *M) printf "%.0f" $(echo "${value%M} * 1024" | bc) ;;
        *G) printf "%.0f" $(echo "${value%G} * 1024 * 1024" | bc) ;;
        *)  printf "%.0f" $(echo "$value / 1024" | bc) ;;
    esac
}

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 pid"
    exit 1
fi

pid=$1
start_time=$(date +%s)

# Print headers
echo "Elapsed_Time,PID,RSS,Stack_VSZ,Stack_Resident,Stack_Dirty,Stack_Swapped,VM_Allocate_VSZ,VM_Allocate_Resident,VM_Allocate_Dirty,VM_Allocate_Swapped"

while true; do
    if ps -p $pid > /dev/null; then
        elapsed_time=$(($(date +%s) - start_time))

        rss=$(ps -p $pid -o rss=) # ps prints rss in KB by default

        echo "$elapsed_time,$pid,$rss"
    else
        echo "Process $pid not found."
        exit 1
    fi
sleep 1
done 
