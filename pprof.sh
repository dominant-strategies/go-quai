#!/bin/bash

# Exit if no arguments were provided
if [ $# -eq 0 ]; then
    echo "No arguments provided"
    exit 1
fi

# Get the folder name from the argument
folder=$1

# Array of ports and corresponding names
ports=("8081" "8090" "8100" "8110" "8091" "8092" "8093" "8101" "8102" "8103" "8111" "8112" "8113")
names=("PRIME" "Region_0" "Region_1" "Region_2" "Zone_0-0" "Zone_0-1" "Zone_0-2" "Zone_1-0" "Zone_1-1" "Zone_1-2" "Zone_2-0" "Zone_2-1" "Zone_2-2")

# Create the new folder in traces
mkdir -p "traces/$folder"

# Loop over the ports
for i in ${!ports[@]}
do
    port=${ports[$i]}
    name=${names[$i]}

    # Run the go tool pprof command for each port
    echo "Running pprof for port $port"
    curl http://localhost:$port/debug/pprof/heap -o "traces/$folder/$name"_"$port"_heap.pb.gz
    curl http://localhost:$port/debug/pprof/goroutine -o "traces/$folder/$name"_"$port"_goroutine.pb.gz
    curl http://localhost:$port/debug/pprof/trace?seconds=2  -o "traces/$folder/$name"_"$port"_trace.pb.gz
done
