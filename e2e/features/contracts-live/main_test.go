//go:build e2e_fleet

package contractslive

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }
