#!/bin/bash
tinygo build -o get_caller.wasm -scheduler=none --no-debug -target wasi ./get_caller.go