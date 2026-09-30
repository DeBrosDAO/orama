package provision

import (
	"fmt"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// refuseStagenet fails an operation that would create, change or destroy
// servers when st is the stagenet target: that cluster is only ever tested.
func refuseStagenet(op string, st *fleet.State) error {
	if st != nil && st.IsStagenet() {
		return fmt.Errorf("%s is not available on the stagenet target: the existing stagenet cluster is only tested, never provisioned, changed or destroyed", op)
	}
	return nil
}
