package management

import (
	"os"
	"testing"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/pluginfixture"
)

func TestMain(m *testing.M) {
	if os.Getenv(centerpluginv1.EnvironmentPluginSocket) != "" {
		os.Exit(pluginfixture.Run("1.2.3"))
	}
	os.Exit(m.Run())
}
