package requestManager

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestManager(t *testing.T) {
	rm := NewManager()

	doTestRM(t, rm)
}

func TestMultipleRequestManager(t *testing.T) {
	rm := NewManager()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doTestRM(t, rm)
		}()
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doTestRM(t, rm)
		}()
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doTestRM(t, rm)
		}()
	}

	wg.Wait()
}

func doTestRM(t *testing.T, rm RequestManager) {
	id := rm.CreateRequest()
	require.NotZero(t, id, "Request ID should not be zero")

	dataChan, err := rm.GetRequestChan(id)
	require.NotNil(t, dataChan, "Data channel should not be nil")
	require.NoError(t, err, "Error on getting data channel")

	rm.CloseRequest(id)

	dataChan, err = rm.GetRequestChan(id)
	require.Nil(t, dataChan, "Data channel should not be nil")
	require.Error(t, err, "Request Channel should be closed")
}
