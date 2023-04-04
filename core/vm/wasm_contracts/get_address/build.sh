#!/bin/bash
tinygo build -o get_address.wasm -scheduler=none --no-debug -target wasi ./get_address.go