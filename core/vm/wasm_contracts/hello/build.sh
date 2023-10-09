#!/bin/bash
tinygo build -o hello.wasm -scheduler=none --no-debug -target wasm ./hello.go