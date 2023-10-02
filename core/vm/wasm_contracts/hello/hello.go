package main

import (
	"unsafe"
)

// main is required for TinyGo to compile to Wasm.
func main() {}

//export run
//go:linkname run
func run() {
	add(1, 2)
}

//export hostLogString
//go:linkname hostLogString
func hostLogString(offset uint32, byteCount uint32)

//export hostLogUint32
//go:linkname hostLogUint32
func hostLogUint32(value uint32)

//export add
func add(x uint32, y uint32) uint32 {
	// 🖐 a wasm module cannot print something
	//fmt.Println(x,y)
	res := x + y

	hostLogUint32(res)

	// //offset, byteCount := strToPtr("from wasm: " + strconv.FormatUint(uint64(res), 10))
	offset, byteCount := strToPtr("👋 Hello from wasm")

	hostLogString(offset, byteCount)

	return res
}

// strToPtr returns a pointer and size pair for the given string in a way
// compatible with WebAssembly numeric types.
func strToPtr(s string) (uint32, uint32) {
	buf := []byte(s)
	ptr := &buf[0]
	unsafePtr := uintptr(unsafe.Pointer(ptr))
	return uint32(unsafePtr), uint32(len(buf))
}