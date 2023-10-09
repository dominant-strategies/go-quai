package main

// main is required for TinyGo to compile to Wasm.
func main() {}

//export run
//go:linkname run
func run() {
	hello()
}

//export logHelloWorld
//go:linkname logHelloWorld
func logHelloWorld()

//export hello
func hello() {
	// 🖐 a wasm module cannot print something
	//fmt.Println(x,y)
	logHelloWorld()
}
