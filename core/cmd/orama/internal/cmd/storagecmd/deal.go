package storagecmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/globalcmd"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

var dealFlags struct {
	chainID  string
	signer   string
	granter  string
	class    string
	nonce    string
	repair   string
	replicas uint32
	price    string
	duration uint64
	pieces   []string
	dealID   uint64
	extra    uint64
	nodeID   string
	slot     uint32
	reason   string
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

func init() {
	create := &cobra.Command{
		Use:   "create",
		Short: "Open a private or public-pin storage deal",
		Long: `Open a PRIVATE or PUBLIC_PIN deal.

The command does not encrypt the bytes and does not upload them. Each --piece
is a 32-byte root and a byte count, written as <64 hex chars>:<bytes>. Leaf
counts follow the 1024-byte piece rule. The root is not checked against the
bytes. A private deal needs one piece per replica. A public-pin deal needs
exactly one piece. Archive deals are refused. Without --node the command
prints the sign document and does not submit it.`,
		Args: cobra.NoArgs,
		RunE: runCreate,
	}
	extend := &cobra.Command{
		Use:   "extend",
		Short: "Add epochs to a storage deal",
		Long:  `Add epochs to a user deal. Without --node the command prints the sign document and does not submit it.`,
		Args:  cobra.NoArgs,
		RunE:  runExtend,
	}
	accept := &cobra.Command{
		Use:   "accept",
		Short: "Accept an assigned storage slot",
		Long:  `Accept one slot of a deal. The signer is the node's hot key. Without --node the command prints the sign document and does not submit it.`,
		Args:  cobra.NoArgs,
		RunE:  runAccept,
	}
	decline := &cobra.Command{
		Use:   "decline",
		Short: "Decline an assigned storage slot",
		Long:  `Decline one slot of a deal. The signer is the node's hot key. Without --node the command prints the sign document and does not submit it.`,
		Args:  cobra.NoArgs,
		RunE:  runDecline,
	}
	addChain := func(c *cobra.Command) {
		f := c.Flags()
		f.StringVar(&dealFlags.chainID, "chain-id", "", "Chain id [required]")
		f.StringVar(&dealFlags.signer, "signer", "", "Signing account (orama1...) [required]")
		f.StringVar(&dealFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
		f.Uint64Var(&dealFlags.account, "account-number", 0, "Account number, when not read from --node")
		f.Uint64Var(&dealFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
		f.StringVar(&dealFlags.fee, "fee", "", "Fee in norama [required]")
		f.Uint64Var(&dealFlags.gas, "gas", 0, "Gas limit [required]")
		f.StringVar(&dealFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
		globalcmd.AddOnionFlags(f)
	}
	addChain(create)
	addChain(extend)
	addChain(accept)
	addChain(decline)
	create.Flags().StringVar(&dealFlags.class, "class", "", "private or public-pin [required]")
	create.Flags().StringVar(&dealFlags.granter, "granter", "", "Account whose deal allowance pays, when the signer is the grantee")
	create.Flags().StringVar(&dealFlags.nonce, "nonce", "", "32-byte deal nonce hex [required]")
	create.Flags().StringVar(&dealFlags.repair, "repair-delegate", "", "Repair delegate id")
	create.Flags().Uint32Var(&dealFlags.replicas, "replicas", 3, "Replica count")
	create.Flags().StringVar(&dealFlags.price, "price", "", "Price per epoch per replica, in norama [required]")
	create.Flags().Uint64Var(&dealFlags.duration, "duration-epochs", 0, "Deal length in epochs [required]")
	create.Flags().StringArrayVar(&dealFlags.pieces, "piece", nil, "Piece as <64-hex-root>:<bytes> [required]")
	extend.Flags().Uint64Var(&dealFlags.dealID, "deal-id", 0, "Deal id [required]")
	extend.Flags().Uint64Var(&dealFlags.extra, "extra-epochs", 0, "Epochs to add [required]")
	for _, c := range []*cobra.Command{accept, decline} {
		c.Flags().StringVar(&dealFlags.nodeID, "id", "", "Node id [required]")
		c.Flags().Uint64Var(&dealFlags.dealID, "deal-id", 0, "Deal id [required]")
		c.Flags().Uint32Var(&dealFlags.slot, "slot", 0, "Slot index")
	}
	decline.Flags().StringVar(&dealFlags.reason, "reason", "", "Why the slot is declined")
	Cmd.AddCommand(create, extend, accept, decline)
}

func dealDirect(typeURL string, msg []byte) clusterreg.Direct {
	return clusterreg.Direct{
		TypeURL: typeURL, Msg: msg,
		FeeAmount: dealFlags.fee, Gas: dealFlags.gas, ChainID: dealFlags.chainID,
		AccountNumber: dealFlags.account, Sequence: dealFlags.sequence,
	}
}

func submitDeal(cmd *cobra.Command, in clusterreg.Direct, verb string) error {
	return globalcmd.SubmitDirect(cmd, dealFlags.signer, dealFlags.node, dealFlags.pubKey, dealFlags.account, dealFlags.sequence, in, verb)
}

func runCreate(cmd *cobra.Command, args []string) error {
	class, err := clusterreg.ParseDealClass(dealFlags.class)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	nonce, err := clusterreg.ParseNonce(dealFlags.nonce)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	if len(dealFlags.pieces) == 0 {
		return clierr.Usage("at least one --piece is required")
	}
	pieces := make([]clusterreg.Piece, 0, len(dealFlags.pieces))
	for _, spec := range dealFlags.pieces {
		p, err := clusterreg.ParsePieceSpec(spec)
		if err != nil {
			return clierr.Usage("%v", err)
		}
		pieces = append(pieces, p)
	}
	d := clusterreg.Deal{
		Signer: dealFlags.signer, Granter: dealFlags.granter, Class: class, Nonce: nonce,
		RepairDelegate: dealFlags.repair, Replicas: dealFlags.replicas, PricePerEpoch: dealFlags.price,
		DurationEpochs: dealFlags.duration, Pieces: pieces,
	}
	if err := clusterreg.ValidateDeal(d); err != nil {
		return clierr.Usage("%v", err)
	}
	return submitDeal(cmd, dealDirect(clusterreg.CreateDealTypeURL, clusterreg.EncodeDeal(d)), "opened deal")
}

func runExtend(cmd *cobra.Command, args []string) error {
	e := clusterreg.Extend{Signer: dealFlags.signer, DealID: dealFlags.dealID, ExtraEpochs: dealFlags.extra}
	if err := clusterreg.ValidateExtend(e); err != nil {
		return clierr.Usage("%v", err)
	}
	return submitDeal(cmd, dealDirect(clusterreg.ExtendDealTypeURL, clusterreg.EncodeExtend(e)), "extended deal")
}

func runAccept(cmd *cobra.Command, args []string) error {
	s := clusterreg.SlotAct{Signer: dealFlags.signer, NodeID: dealFlags.nodeID, DealID: dealFlags.dealID, Slot: dealFlags.slot}
	if err := clusterreg.ValidateSlot(s); err != nil {
		return clierr.Usage("%v", err)
	}
	return submitDeal(cmd, dealDirect(clusterreg.AcceptDealTypeURL, clusterreg.EncodeAccept(s)), "accepted "+dealFlags.nodeID)
}

func runDecline(cmd *cobra.Command, args []string) error {
	s := clusterreg.SlotAct{
		Signer: dealFlags.signer, NodeID: dealFlags.nodeID, DealID: dealFlags.dealID,
		Slot: dealFlags.slot, Reason: dealFlags.reason,
	}
	if err := clusterreg.ValidateSlot(s); err != nil {
		return clierr.Usage("%v", err)
	}
	return submitDeal(cmd, dealDirect(clusterreg.DeclineDealTypeURL, clusterreg.EncodeDecline(s)), "declined "+dealFlags.nodeID)
}
