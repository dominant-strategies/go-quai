#!/bin/bash
tinygo build -o get_block_number.wasm -scheduler=none --no-debug -target wasi ./get_block_number.go