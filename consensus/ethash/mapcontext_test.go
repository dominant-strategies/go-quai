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
	wrongRegionLocation1 := LocationMapContextHolder{
		location:   []byte{4, 0},   // non-existent Region
		mapcontext: []int{3, 3, 3}, // standard mapcontext
	}
	// pass wrong location values to verifyMapContext to test result
	var testValue1 bool = verifyMapContext(wrongRegionLocation1.location, wrongRegionLocation1.mapcontext)
	if testValue1 != false {
		t.Errorf("wrong location value %d verified", wrongRegionLocation1.location)
	}
	// create holder that will have wrong Zone value
	wrongZoneLocation1 := LocationMapContextHolder{
		location:   []byte{0, 1},   // impossible location
		mapcontext: []int{3, 3, 3}, // standard mapcontext
	}
	// pass wrong location values to verifyMapContext to test result
	var testValue2 bool = verifyMapContext(wrongZoneLocation1.location, wrongZoneLocation1.mapcontext)
	if testValue2 != false {
		t.Errorf("impossible Zone location value %d verified", wrongZoneLocation1.location)
	}
}
