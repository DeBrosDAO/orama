//go:build e2e_fleet

package soak

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }
