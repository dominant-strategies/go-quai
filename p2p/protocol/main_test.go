package protocol

import (
	"os"
	"testing"

	"github.com/dominant-strategies/go-quai/log"
	"github.com/sirupsen/logrus"
)

func TestMain(m *testing.M) {
	// Comment / un comment below to see log output while testing
	// log.ConfigureLogger(log.WithNullLogger())
	log.Global.SetLevel(logrus.DebugLevel)
	os.Exit(m.Run())
}
