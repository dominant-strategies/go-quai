#!/bin/bash

# Exit if no arguments were provided
if [ $# -eq 0 ]; then
    echo "No arguments provided"
    exit 1
fi

# Get the folder name from the argument
folder=$1

# Array of ports and corresponding names
# ports=("9001" "9002" "9003" "9004" "9100" "9101" "9102" "9120" "9121" "9122" "9140" "9141" "1942")
# names=("PRIME" "Region_0" "Region_1" "Region_2" "Zone_0-0" "Zone_0-1" "Zone_0-2" "Zone_1-0" "Zone_1-1" "Zone_1-2" "Zone_2-0" "Zone_2-1" "Zone_2-2")

# Array of ports and corresponding names
ports=("8085")
names=("all")

# Create the new folder in traces
mkdir -p "traces/$folder"


while true; do
    # Loop over the ports
    for i in ${!ports[@]}
    do
        port=${ports[$i]}
        name=${names[$i]}

        # Get the current date and time in the format "YearMonthDay-HourMinuteSecond"
        timestamp=$(date +"%Y%m%d-%H%M%S")

        # Run the go tool pprof command for each port
        echo "Running pprof for port $port"
        curl http://localhost:$port/debug/pprof/goroutine -o "traces/$folder/$name"_"$timestamp"_"$port"_goroutine.pb.gz
        curl http://localhost:$port/debug/pprof/heap -o "traces/$folder/$name"_"$timestamp"_"$port"_heap.pb.gz
        curl http://localhost:$port/debug/pprof/mutex -o "traces/$folder/$name"_"$timestamp"_"$port"_mutex.pb.gz
        curl http://localhost:$port/debug/pprof/trace?seconds=5 -o "traces/$folder/$name"_"$timestamp"_"$port"_trace.pb.gz
    done
    sleep 600 
done
