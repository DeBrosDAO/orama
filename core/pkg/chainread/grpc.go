package chainread

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// queriesDescriptor is the FileDescriptorSet of every orama module's Query
// service, from chain/proto. Regenerate with gen.sh.
//
//go:embed queries.binpb
var queriesDescriptor []byte

var (
	filesOnce sync.Once
	files     *protoregistry.Files
	filesErr  error
	// types resolves Any type URLs over files; dynamicpb.Types is safe for concurrent use.
	types *dynamicpb.Types
)

func queryFiles() (*protoregistry.Files, error) {
	filesOnce.Do(func() {
		var set descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(queriesDescriptor, &set); err != nil {
			filesErr = fmt.Errorf("embedded query descriptors: %w", err)
			return
		}
		files, filesErr = protodesc.NewFiles(&set)
		if filesErr == nil {
			types = dynamicpb.NewTypes(files)
		}
	})
	return files, filesErr
}

// anyResolver resolves the type URL of every google.protobuf.Any in a request or response against
// the embedded descriptors. protojson's default is protoregistry.GlobalTypes, where core links no
// chain type, so an Account answer (a BaseAccount in an Any) could not be encoded to JSON.
func anyResolver() (*dynamicpb.Types, error) {
	if _, err := queryFiles(); err != nil {
		return nil, err
	}
	return types, nil
}

// ErrNotFound is a query for a key the chain does not have: the SDK's ErrKeyNotFound, which
// baseapp returns for a gRPC NotFound status.
var ErrNotFound = errors.New("not found on chain")

// ErrInvalidRequest is a query the node refused as malformed: the SDK's ErrInvalidRequest, which
// baseapp returns for a gRPC InvalidArgument status or an error that is not a status at all (a
// bad bech32 address, for one).
var ErrInvalidRequest = errors.New("invalid chain query")

const (
	sdkCodespace      = "sdk"
	sdkKeyNotFound    = 22
	sdkInvalidRequest = 18
	sdkUnknownRequest = 6

	// contractInfoPath is wasmd's ContractInfo query. For an address that is not a contract wasmd
	// returns its ErrNoSuchContract, and baseapp turns any error that is not a gRPC status into an
	// SDK unknown-request error (code 6, codespace sdk), which drops the wasm code; the only thing
	// left to tell it from another failure is the message wasmd gives that error.
	contractInfoPath     = "/cosmwasm.wasm.v1.Query/ContractInfo"
	noSuchContractPhrase = "no such contract"

	// paginationField and limitField name the cosmos-sdk PageRequest of a paginated query.
	paginationField = "pagination"
	limitField      = "limit"
)

// Method is one gRPC query method, resolved from the embedded descriptors.
type Method struct {
	// Path is the ABCI query path, "/orama.nodes.v1.Query/Node".
	Path   string
	input  protoreflect.MessageDescriptor
	output protoreflect.MessageDescriptor
}

// Lookup resolves "orama.nodes.v1.Query/Node".
func Lookup(name string) (*Method, error) {
	service, method, ok := strings.Cut(strings.TrimPrefix(name, "/"), "/")
	if !ok {
		return nil, fmt.Errorf("query %q is not Service/Method, for example orama.nodes.v1.Query/Node", name)
	}
	fs, err := queryFiles()
	if err != nil {
		return nil, err
	}
	desc, err := fs.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("unknown query service %q", service)
	}
	svc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is not a service", service)
	}
	m := svc.Methods().ByName(protoreflect.Name(method))
	if m == nil {
		return nil, fmt.Errorf("service %s has no query %q", service, method)
	}
	return &Method{Path: "/" + service + "/" + method, input: m.Input(), output: m.Output()}, nil
}

// EncodeRequest turns a JSON request (field names as in the proto, snake_case
// or lowerCamel) into protobuf bytes. Empty means the empty request.
func (m *Method) EncodeRequest(requestJSON string) ([]byte, error) {
	msg := dynamicpb.NewMessage(m.input)
	if strings.TrimSpace(requestJSON) != "" {
		types, err := anyResolver()
		if err != nil {
			return nil, err
		}
		if err := (protojson.UnmarshalOptions{Resolver: types}).Unmarshal([]byte(requestJSON), msg); err != nil {
			return nil, fmt.Errorf("request for %s: %w", m.Path, err)
		}
	}
	return proto.Marshal(msg)
}

// DecodeResponse turns the response bytes into JSON with the proto field names.
func (m *Method) DecodeResponse(data []byte) (json.RawMessage, error) {
	msg := dynamicpb.NewMessage(m.output)
	if err := proto.Unmarshal(data, msg); err != nil {
		return nil, fmt.Errorf("response of %s: %w", m.Path, err)
	}
	types, err := anyResolver()
	if err != nil {
		return nil, err
	}
	out, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true, Resolver: types}.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("response of %s: %w", m.Path, err)
	}
	return out, nil
}

// Query runs one Orama module query through the node's CometBFT RPC when the Reader has one, and
// through the gateway's /v1/chain/query/ route otherwise.
func (r *Reader) Query(ctx context.Context, name, requestJSON string) (json.RawMessage, error) {
	if r.RPC != "" {
		return r.GRPC(ctx, name, requestJSON)
	}
	return r.GatewayQuery(ctx, name, requestJSON)
}

// GatewayQuery runs one Orama module query through the gateway's read-only
// GET /v1/chain/query/<package.Service>/<Method> route, which answers the decoded response as JSON.
// The request is checked here first, so an unknown query or a malformed request sends nothing.
func (r *Reader) GatewayQuery(ctx context.Context, name, requestJSON string) (json.RawMessage, error) {
	m, err := Lookup(name)
	if err != nil {
		return nil, err
	}
	data, err := m.EncodeRequest(requestJSON)
	if err != nil {
		return nil, err
	}
	path := "query" + m.Path
	if len(data) > 0 {
		path += "?data=" + base64.RawURLEncoding.EncodeToString(data)
	}
	return r.GatewayGet(ctx, path)
}

// GRPC runs one query through the node's CometBFT RPC abci_query and returns
// the decoded response as JSON. The query runs at the latest height and asks
// for no proof.
func (r *Reader) GRPC(ctx context.Context, name, requestJSON string) (json.RawMessage, error) {
	m, err := Lookup(name)
	if err != nil {
		return nil, err
	}
	data, err := m.EncodeRequest(requestJSON)
	if err != nil {
		return nil, err
	}
	root, err := base(r.RPC, "--rpc")
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "abci_query",
		"params": map[string]any{"path": m.Path, "data": hex.EncodeToString(data), "height": "0", "prove": false},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	body, err := r.do(req)
	if err != nil {
		return nil, err
	}
	result, err := rpcResult(body)
	if err != nil {
		return nil, err
	}
	return m.abciResponse(result)
}

// CheckRequest reports whether data is a well-formed protobuf request for the method.
func (m *Method) CheckRequest(data []byte) error {
	if err := proto.Unmarshal(data, dynamicpb.NewMessage(m.input)); err != nil {
		return fmt.Errorf("request for %s: %w", m.Path, err)
	}
	return nil
}

// PageLimit returns the pagination.limit of an encoded request: 0 when the method takes no
// pagination or the request leaves the limit unset, which a cosmos-sdk query reads as its default.
func (m *Method) PageLimit(data []byte) (uint64, error) {
	msg := dynamicpb.NewMessage(m.input)
	if err := proto.Unmarshal(data, msg); err != nil {
		return 0, fmt.Errorf("request for %s: %w", m.Path, err)
	}
	page := m.input.Fields().ByName(paginationField)
	if page == nil || page.Message() == nil || !msg.Has(page) {
		return 0, nil
	}
	pm := msg.Get(page).Message()
	limit := page.Message().Fields().ByName(limitField)
	if limit == nil {
		return 0, nil
	}
	return pm.Get(limit).Uint(), nil
}

// DecodeRPC decodes the body of a CometBFT abci_query answer, in its JSON-RPC envelope, into the
// method's response as JSON.
func (m *Method) DecodeRPC(body []byte) (json.RawMessage, error) {
	result, err := rpcResult(body)
	if err != nil {
		return nil, err
	}
	return m.abciResponse(result)
}

// abciResponse unwraps an abci_query result and decodes its value.
func (m *Method) abciResponse(result json.RawMessage) (json.RawMessage, error) {
	var res struct {
		Response struct {
			Code      uint32 `json:"code"`
			Log       string `json:"log"`
			Codespace string `json:"codespace"`
			Value     string `json:"value"`
		} `json:"response"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		return nil, fmt.Errorf("abci_query result: %w", err)
	}
	if res.Response.Code == sdkKeyNotFound && res.Response.Codespace == sdkCodespace {
		return nil, fmt.Errorf("%w: %s: %s", ErrNotFound, m.Path, truncate(res.Response.Log))
	}
	if res.Response.Code == sdkInvalidRequest && res.Response.Codespace == sdkCodespace {
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalidRequest, m.Path, truncate(res.Response.Log))
	}
	if m.Path == contractInfoPath && res.Response.Code == sdkUnknownRequest && res.Response.Codespace == sdkCodespace &&
		strings.Contains(res.Response.Log, noSuchContractPhrase) {
		return nil, fmt.Errorf("%w: %s: %s", ErrNotFound, m.Path, truncate(res.Response.Log))
	}
	if res.Response.Code != 0 {
		return nil, fmt.Errorf("%s failed (code %d %s): %s", m.Path, res.Response.Code, res.Response.Codespace, truncate(res.Response.Log))
	}
	value, err := base64.StdEncoding.DecodeString(res.Response.Value)
	if err != nil {
		return nil, fmt.Errorf("abci_query value is not base64: %w", err)
	}
	return m.DecodeResponse(value)
}
