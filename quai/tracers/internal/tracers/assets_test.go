package tracers

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"log"
	"testing"
)

func TestGenerateAssetBytesForPreState(t *testing.T) {
	// Read the file into a byte slice.
	data, err := ioutil.ReadFile("call_tracer.js")
	if err != nil {
		log.Fatalf("failed to read file: %v", err)
	}

	// Create a buffer to hold the compressed data.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)

	// Write the file data to the gzip writer.
	_, err = gz.Write(data)
	if err != nil {
		log.Fatalf("failed to write data to gzip writer: %v", err)
	}

	// Close the gzip writer to flush the compressed data into the buffer.
	if err := gz.Close(); err != nil {
		log.Fatalf("failed to close gzip writer: %v", err)
	}

	// Retrieve the compressed bytes.
	compressedBytes := buf.Bytes()

	// Print the raw compressed bytes.
	fmt.Println("Compressed bytes:", compressedBytes)

	// Optionally, print the hexadecimal representation.
	fmt.Print("var _opcount_tracerJs = []byte(")
	fmt.Printf("%q", compressedBytes)
	fmt.Println(")")

	asset, err := prestate_tracerJs()
	if err != nil {
		log.Fatalf("failed to get the prestate tracer decoded")
	}

	fmt.Println(asset)
}
