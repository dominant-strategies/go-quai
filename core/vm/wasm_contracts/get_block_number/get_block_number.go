package main

import (
	"strconv"
	"unsafe"
)

// main is required for TinyGo to compile to Wasm.
func main() {}

//export hostLogString
//go:linkname hostLogString
func hostLogString(offset uint32, byteCount uint32)

//export run
//go:linkname run
func run() {
	number := getBlockNumber()
	hostLogString(strToPtr(strconv.Itoa(int(number))))
}

//export getBlockNumber
//go:linkname getBlockNumber
func getBlockNumber() int64

// strToPtr returns a pointer and size pair for the given string in a way
// compatible with WebAssembly numeric types.
func strToPtr(s string) (uint32, uint32) {
	buf := []byte(s)
	ptr := &buf[0]
	unsafePtr := uintptr(unsafe.Pointer(ptr))
	return uint32(unsafePtr), uint32(len(buf))
}