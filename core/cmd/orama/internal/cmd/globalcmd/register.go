package globalcmd

import (
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
	"github.com/spf13/cobra"
)

var nodeFlags struct {
	chainID  string
	operator string
	id       string
	hotKey   string
	roles    []string
	bindings []string
	ends     []string
	region   string
	asn      uint32
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

var registerNodeCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a global node from signed service-key bindings",
	Long: `Build MsgRegisterNode from bindings that 'orama global bind' wrote.

The message names the operator, a node id, roles, a hot key that is not the
operator, the bindings, public endpoints, an optional region and an optional
--asn, the autonomous system number the node declares. The chain cannot verify
the ASN; a protocol deal slot goes only to a node that declared one, and slots
go to distinct ASNs. Reserved, documentation and private-use numbers are
refused. It does not include a tenant list or a cluster secret.

--node is the chain REST API. The command reads the account there, asks the
RootWallet agent to sign this one transaction, and broadcasts it. Without
--node it prints the sign document and does not submit anything.

Each --binding file is the JSON bind printed. Its signature must verify for
this --chain-id and --operator.`,
	Args: cobra.NoArgs,
	RunE: runRegisterNode,
}

func init() {
	f := registerNodeCmd.Flags()
	f.StringVar(&nodeFlags.chainID, "chain-id", "", "Chain id [required]")
	f.StringVar(&nodeFlags.operator, "operator", "", "Operator account (orama1...) [required]")
	f.StringVar(&nodeFlags.id, "id", "", "Node id [required]")
	f.StringVar(&nodeFlags.hotKey, "hot-key", "", "Hot key account, not the operator [required]")
	f.StringArrayVar(&nodeFlags.roles, "role", nil, "Role: validator, storage, relay, exit, dirauth, archiver [required]")
	f.StringArrayVar(&nodeFlags.bindings, "binding", nil, "Binding JSON from orama global bind [required]")
	f.StringArrayVar(&nodeFlags.ends, "endpoint", nil, "Public endpoint (repeatable)")
	f.StringVar(&nodeFlags.region, "region", "", "Region hint")
	f.Uint32Var(&nodeFlags.asn, "asn", 0, "Autonomous system number the node declares (0 leaves it undeclared)")
	f.StringVar(&nodeFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
	f.Uint64Var(&nodeFlags.account, "account-number", 0, "Account number, when not read from --node")
	f.Uint64Var(&nodeFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
	f.StringVar(&nodeFlags.fee, "fee", "", "Fee in norama [required]")
	f.Uint64Var(&nodeFlags.gas, "gas", 0, "Gas limit [required]")
	f.StringVar(&nodeFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it")
	AddOnionFlags(f)
	Cmd.AddCommand(registerNodeCmd)
}

func runRegisterNode(cmd *cobra.Command, args []string) error {
	roles, err := parseRoles(nodeFlags.roles)
	if err != nil {
		return err
	}
	bindings, err := readBindings(nodeFlags.bindings, nodeFlags.chainID, nodeFlags.operator)
	if err != nil {
		return err
	}
	reg := clusterreg.NodeRegistration{
		Operator: nodeFlags.operator, NodeID: nodeFlags.id, Roles: roles,
		HotKey: nodeFlags.hotKey, Bindings: bindings, Endpoints: nodeFlags.ends,
		RegionHint: nodeFlags.region, ASN: nodeFlags.asn,
	}
	if err := clusterreg.ValidateNode(reg); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.RegisterNodeTypeURL, Msg: clusterreg.EncodeRegisterNode(reg),
		FeeAmount: nodeFlags.fee, Gas: nodeFlags.gas, ChainID: nodeFlags.chainID,
		AccountNumber: nodeFlags.account, Sequence: nodeFlags.sequence,
	}
	return SubmitDirect(cmd, nodeFlags.operator, nodeFlags.node, nodeFlags.pubKey, nodeFlags.account, nodeFlags.sequence, in, "registered "+nodeFlags.id)
}

func parseRoles(names []string) ([]int, error) {
	var roles []int
	for _, name := range names {
		switch name {
		case "validator":
			roles = append(roles, clusterreg.RoleValidator)
		case "storage":
			roles = append(roles, clusterreg.RoleStorage)
		case "relay":
			roles = append(roles, clusterreg.RoleRelay)
		case "exit":
			roles = append(roles, clusterreg.RoleExit)
		case "dirauth":
			roles = append(roles, clusterreg.RoleDirauth)
		case "archiver":
			roles = append(roles, clusterreg.RoleArchiver)
		default:
			return nil, clierr.Usage("unknown role %q", name)
		}
	}
	return roles, nil
}

func readBindings(paths []string, chainID, operator string) ([]clusterreg.NodeBinding, error) {
	var out []clusterreg.NodeBinding
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, clierr.Failure("read %s: %w", path, err)
		}
		var doc struct {
			Service   string `json:"service"`
			KeyType   string `json:"key_type"`
			Pubkey    string `json:"pubkey"`
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, clierr.Usage("%s is not binding JSON", path)
		}
		pub, err := hex.DecodeString(doc.Pubkey)
		if err != nil {
			return nil, clierr.Usage("%s pubkey is not hex", path)
		}
		sig, err := hex.DecodeString(doc.Signature)
		if err != nil {
			return nil, clierr.Usage("%s signature is not hex", path)
		}
		b := globalbind.Binding{Service: doc.Service, KeyType: doc.KeyType, Pubkey: pub, Signature: sig}
		if err := globalbind.Verify(b, chainID, operator); err != nil {
			return nil, clierr.Usage("%s: %v", path, err)
		}
		out = append(out, clusterreg.NodeBinding{
			Service: doc.Service, KeyType: doc.KeyType, Pubkey: pub, Signature: sig,
		})
	}
	return out, nil
}
