package main

// import "unsafe"

// // main is required for TinyGo to compile to Wasm.
// func main() {}

// //export run
// //go:linkname run
// func run() {
// 	var address [8]uint32
// 	rawAddress := uintptr(unsafe.Pointer(&address[0]))
// 	int32Address := (*int32)(unsafe.Pointer(rawAddress))
// 	getAddress(int32Address)
// }

// //export getAddress
// //go:linkname getAddress
// func getAddress(resultOffset int32)

/*
#include <stdint.h>

extern void getAddress(uint32_t *offset);
extern void storageStore(uint32_t *keyOffset, uint32_t *valueOffset);
*/
import (
	"reflect"
	"strconv"
	"unsafe"
)

func main() {}

//export hostLogString
//go:linkname hostLogString
func hostLogString(offset uint32, byteCount uint32)

//export run
//go:linkname run
func run() {
	var address string
	rawAddress := uintptr(unsafe.Pointer(&address))

	int32RawAddress := int32(int64(rawAddress))

	getCaller(int32RawAddress)

	addressString := ptrToString(uint32(rawAddress), 42)

	hostLogString(strToPtr(addressString))
	hostLogString(strToPtr("pointer " + strconv.Itoa(int(int32RawAddress))))
	hostLogString(strToPtr("Hello from wasm"))
}

//export getCaller
//go:linkname getCaller
func getCaller(resultOffset int32)


// ptrToString returns a string from WebAssembly compatible numeric types
// representing its pointer and length.
func ptrToString(ptr uint32, size uint32) string {
	// Get a slice view of the underlying bytes in the stream. We use SliceHeader, not StringHeader
	// as it allows us to fix the capacity to what was allocated.
	return *(*string)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(ptr),
		Len:  uintptr(size), // Tinygo requires these as uintptrs even if they are int fields.
		Cap:  uintptr(size), // ^^ See https://github.com/tinygo-org/tinygo/issues/1284
	}))
}


// strToPtr returns a pointer and size pair for the given string in a way
// compatible with WebAssembly numeric types.
func strToPtr(s string) (uint32, uint32) {
	buf := []byte(s)
	ptr := &buf[0]
	unsafePtr := uintptr(unsafe.Pointer(ptr))
	return uint32(unsafePtr), uint32(len(buf))
}