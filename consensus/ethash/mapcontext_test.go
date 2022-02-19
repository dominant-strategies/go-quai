// tests for the MapContext field

package ethash

import (
	"testing"
)

type LocationMapContextHolder struct {
	location   []byte
	mapcontext []int
}

func TestWrongLocations(t *testing.T) {
	// create holder that will have wrong Region location
	wrongRegionLocation := LocationMapContextHolder{
		location:   []byte{4, 0},
		mapcontext: []int{3, 3, 3},
	}
	// pass wrong location values to verifyMapContext to test result
	var testValue bool = verifyMapContext(wrongRegionLocation.location, wrongRegionLocation.mapcontext)
	if testValue != false {
		t.Errorf("wrong location value %d verified", wrongRegionLocation.location)
	}
}
