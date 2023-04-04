#!/bin/bash
tinygo build -o use_gas.wasm -scheduler=none --no-debug -target wasi ./use_gas.go