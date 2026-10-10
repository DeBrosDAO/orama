//go:build e2e_fleet

package chain

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// A few Query RPCs have no oramad CLI command (x/houses Vote, HouseBond and
// Enacted; x/storage Authorization, Challenges, EpochMint and Slot: their
// client/cli/query.go does not list them, and autocli does not add to a
// module's custom command). They are asked through CometBFT's abci_query on
// the node's loopback RPC, with the request encoded here in protobuf wire
// format and the answer decoded field by field. The encoders cover the
// scalar request shapes those RPCs use.

var grpcPath = regexp.MustCompile(`^/(orama\.[a-z]+\.v1\.Query|cosmos\.base\.reflection\.v1beta1\.ReflectionService)/[A-Z][A-Za-z]+$`)

// storePath is a raw KV read of one module store (/store/<name>/key, the
// request being the key): it answers "no such store" for a store the app
// does not mount, whatever the module's query methods are called.
var storePath = regexp.MustCompile(`^/store/[a-z]+/key$`)

// PB is a protobuf message under construction.
type PB []byte

// Text appends a length-delimited string field.
func (p PB) Text(field int, s string) PB { return p.Bytes(field, []byte(s)) }

// Bytes appends a length-delimited bytes field.
func (p PB) Bytes(field int, b []byte) PB {
	p = binary.AppendUvarint(p, uint64(field)<<3|2)
	p = binary.AppendUvarint(p, uint64(len(b)))
	return append(p, b...)
}

// Uint appends a varint field.
func (p PB) Uint(field int, v uint64) PB {
	p = binary.AppendUvarint(p, uint64(field)<<3)
	return binary.AppendUvarint(p, v)
}

// ABCIAnswer is one abci_query answer.
type ABCIAnswer struct {
	Code  uint32
	Log   string
	Value []byte
}

// ABCIQuery asks path (a gRPC method, /orama.<module>.v1.Query/<Rpc>, or a
// module store read, /store/<name>/key) with the encoded request on node n.
func (c *Chain) ABCIQuery(t testing.TB, n fleet.Node, path string, req PB) ABCIAnswer {
	t.Helper()
	if !grpcPath.MatchString(path) && !storePath.MatchString(path) {
		t.Fatalf("abci_query path %q is neither an orama Query method nor a module store read", path)
	}
	url := fmt.Sprintf(`%s/abci_query?path="%s"&data=0x%s`, c.RPCHTTP(), path, hex.EncodeToString(req))
	out := c.Run(t, n, QueryBudget, "curl -sS --max-time 20 "+fleet.ShellQuote(url))
	if out.Exit != 0 {
		t.Fatalf("%s: abci_query %s exited %d: %s", n.Name, path, out.Exit, out.Stderr)
	}
	var r struct {
		Result struct {
			Response struct {
				Code  uint32 `json:"code"`
				Log   string `json:"log"`
				Value string `json:"value"`
			} `json:"response"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &r); err != nil || len(r.Error) > 0 {
		t.Fatalf("%s: abci_query %s: %v %s: %s", n.Name, path, err, r.Error, out.Stdout)
	}
	val, err := base64.StdEncoding.DecodeString(r.Result.Response.Value)
	if err != nil {
		t.Fatalf("%s: abci_query %s value is not base64: %v", n.Name, path, err)
	}
	return ABCIAnswer{Code: r.Result.Response.Code, Log: r.Result.Response.Log, Value: val}
}

// Fields is a decoded protobuf message: field number to its raw values
// (varints as uint64, length-delimited as []byte), in order.
type Fields map[int][]any

// DecodePB decodes one level of a protobuf message.
func DecodePB(b []byte) (Fields, error) {
	f := Fields{}
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, fmt.Errorf("bad tag")
		}
		b = b[n:]
		field, wire := int(tag>>3), tag&7
		switch wire {
		case 0:
			v, m := binary.Uvarint(b)
			if m <= 0 {
				return nil, fmt.Errorf("bad varint in field %d", field)
			}
			f[field], b = append(f[field], v), b[m:]
		case 2:
			l, m := binary.Uvarint(b)
			if m <= 0 || uint64(len(b)-m) < l {
				return nil, fmt.Errorf("bad length in field %d", field)
			}
			f[field], b = append(f[field], b[m:m+int(l)]), b[m+int(l):]
		case 1:
			if len(b) < 8 {
				return nil, fmt.Errorf("short fixed64 in field %d", field)
			}
			f[field], b = append(f[field], binary.LittleEndian.Uint64(b)), b[8:]
		case 5:
			if len(b) < 4 {
				return nil, fmt.Errorf("short fixed32 in field %d", field)
			}
			f[field], b = append(f[field], uint64(binary.LittleEndian.Uint32(b))), b[4:]
		default:
			return nil, fmt.Errorf("unsupported wire type %d in field %d", wire, field)
		}
	}
	return f, nil
}

// Msg returns field's first value decoded as a nested message.
func (f Fields) Msg(field int) (Fields, bool) {
	for _, v := range f[field] {
		if b, ok := v.([]byte); ok {
			m, err := DecodePB(b)
			return m, err == nil
		}
	}
	return nil, false
}

// Str returns field's first value as a string.
func (f Fields) Str(field int) string {
	for _, v := range f[field] {
		if b, ok := v.([]byte); ok {
			return string(b)
		}
	}
	return ""
}

// Varint returns field's first value as an integer (0 when absent: proto3
// does not encode a zero scalar).
func (f Fields) Varint(field int) uint64 {
	for _, v := range f[field] {
		if u, ok := v.(uint64); ok {
			return u
		}
	}
	return 0
}
