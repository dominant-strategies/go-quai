package main

// main is required for TinyGo to compile to Wasm.
func main() {}

//export run
//go:linkname run
func run() {
	useGas(1000)
}

//export useGas
//go:linkname useGas
func useGas(amount int64)
