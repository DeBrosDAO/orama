package chainread

import (
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Methods lists every Orama module query method the embedded descriptors carry, as
// "orama.nodes.v1.Query/Node".
func Methods() ([]string, error) {
	return methodsIn("orama.")
}

// SDKMethods lists the Query methods of the cosmos-sdk and wasmd services the embedded descriptors
// carry (bank, auth, staking, distribution and cosmwasm.wasm), as "cosmos.bank.v1beta1.Query/Balance".
// The embedded set is whole services, so this is every method of them, not the ones a gateway serves.
func SDKMethods() ([]string, error) {
	return methodsIn("cosmos.", "cosmwasm.")
}

func methodsIn(packagePrefixes ...string) ([]string, error) {
	fs, err := queryFiles()
	if err != nil {
		return nil, err
	}
	var out []string
	fs.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		if !hasAnyPrefix(string(f.Package()), packagePrefixes) {
			return true
		}
		services := f.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			if !strings.HasSuffix(string(svc.FullName()), ".Query") {
				continue
			}
			for j := 0; j < svc.Methods().Len(); j++ {
				out = append(out, string(svc.FullName())+"/"+string(svc.Methods().Get(j).Name()))
			}
		}
		return true
	})
	sort.Strings(out)
	return out, nil
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
