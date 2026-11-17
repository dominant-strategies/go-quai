package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateHeaderWorkerLimiterAllowsOnlyConfiguredWorkers(t *testing.T) {
	hc := &HierarchicalCoordinator{}

	require.True(t, hc.tryAcquireGenerateHeaderWorker())
	require.Equal(t, 1, hc.generateHeaderWorkersCount)

	require.False(t, hc.tryAcquireGenerateHeaderWorker())
	require.Equal(t, 1, hc.generateHeaderWorkersCount)

	hc.releaseGenerateHeaderWorker()
	require.Equal(t, 0, hc.generateHeaderWorkersCount)

	require.True(t, hc.tryAcquireGenerateHeaderWorker())
	require.Equal(t, 1, hc.generateHeaderWorkersCount)
}

func TestReleaseGenerateHeaderWorkerDoesNotUnderflow(t *testing.T) {
	hc := &HierarchicalCoordinator{}

	hc.releaseGenerateHeaderWorker()

	require.Equal(t, 0, hc.generateHeaderWorkersCount)
}
