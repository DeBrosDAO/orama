package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.Msg = &MsgClaimNodeName{}
	_ sdk.Msg = &MsgReleaseNodeName{}
	_ sdk.Msg = &MsgRegisterOperator{}
	_ sdk.Msg = &MsgRegisterNode{}
	_ sdk.Msg = &MsgUpdateNode{}
	_ sdk.Msg = &MsgRetireNode{}
	_ sdk.Msg = &MsgBondNode{}
	_ sdk.Msg = &MsgUnbondNode{}
	_ sdk.Msg = &MsgDeclareCapacity{}
	_ sdk.Msg = &MsgRegisterCluster{}
	_ sdk.Msg = &MsgUpdateCluster{}
	_ sdk.Msg = &MsgRetireCluster{}
)

// ValidateBasic checks MsgRegisterOperator's signer.
func (msg MsgRegisterOperator) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("register operator: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgRegisterNode's stateless fields. Binding signatures
// are checked by the keeper, which has the chain id.
func (msg MsgRegisterNode) ValidateBasic() error {
	operator, err := CanonicalAddress(msg.Operator)
	if err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	hot, err := CanonicalAddress(msg.HotKey)
	if err != nil {
		return fmt.Errorf("register node hot key: %w", err)
	}
	if hot == operator {
		return fmt.Errorf("register node: %w", ErrHotKey)
	}
	if err := ValidateRoles(msg.Roles); err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if len(msg.Bindings) == 0 {
		return fmt.Errorf("register node: at least one binding is required")
	}
	if err := validateBindingList(msg.Bindings); err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if err := CheckHotKeyBinding(hot, msg.Bindings); err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if err := ValidateEndpoints(msg.Endpoints, 0, absoluteEndpointCap); err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if err := ValidateRegion(msg.RegionHint); err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if msg.Asn != 0 {
		if err := ValidateASN(msg.Asn); err != nil {
			return fmt.Errorf("register node: %w", err)
		}
	}
	return nil
}

// ValidateBasic checks MsgUpdateNode. An empty hot key and an empty binding
// list mean "leave unchanged"; the keeper rejects an update that sets neither
// those nor the endpoint/region flags. A new hot key must arrive with the
// bindings that carry its own signed "hot-key" binding. A binding list sent
// without a new hot key is checked against the stored hot key by the keeper.
func (msg MsgUpdateNode) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("update node: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("update node: %w", err)
	}
	if msg.HotKey != "" {
		hot, err := CanonicalAddress(msg.HotKey)
		if err != nil {
			return fmt.Errorf("update node hot key: %w", err)
		}
		if err := CheckHotKeyBinding(hot, msg.Bindings); err != nil {
			return fmt.Errorf("update node: %w", err)
		}
	}
	if len(msg.Bindings) > 0 {
		if err := validateBindingList(msg.Bindings); err != nil {
			return fmt.Errorf("update node: %w", err)
		}
	}
	if msg.SetEndpoints {
		if err := ValidateEndpoints(msg.Endpoints, 0, absoluteEndpointCap); err != nil {
			return fmt.Errorf("update node: %w", err)
		}
	}
	if msg.SetRegionHint {
		if err := ValidateRegion(msg.RegionHint); err != nil {
			return fmt.Errorf("update node: %w", err)
		}
	}
	if msg.SetAsn && msg.Asn != 0 {
		if err := ValidateASN(msg.Asn); err != nil {
			return fmt.Errorf("update node: %w", err)
		}
	}
	return nil
}

// ValidateBasic checks MsgRetireNode.
func (msg MsgRetireNode) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("retire node: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("retire node: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgBondNode.
func (msg MsgBondNode) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("bond node: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("bond node: %w", err)
	}
	if !knownRole(msg.Role) {
		return fmt.Errorf("bond node: unknown role %s", msg.Role)
	}
	if err := PositiveAmount(msg.Amount); err != nil {
		return fmt.Errorf("bond node: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgUnbondNode.
func (msg MsgUnbondNode) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("unbond node: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("unbond node: %w", err)
	}
	if !knownRole(msg.Role) {
		return fmt.Errorf("unbond node: unknown role %s", msg.Role)
	}
	if err := PositiveAmount(msg.Amount); err != nil {
		return fmt.Errorf("unbond node: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgDeclareCapacity.
func (msg MsgDeclareCapacity) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("declare capacity: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("declare capacity: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgFundHotKey.
func (msg MsgFundHotKey) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("fund hot key: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("fund hot key: %w", err)
	}
	if err := PositiveAmount(msg.Amount); err != nil {
		return fmt.Errorf("fund hot key: %w", err)
	}
	if _, ok := FundSource_name[int32(msg.Source)]; !ok {
		return fmt.Errorf("fund hot key: unknown funding source %d (0 is earnings, 1 is the bank balance)", int32(msg.Source))
	}
	return nil
}

// ValidateBasic checks MsgRegisterCluster.
func (msg MsgRegisterCluster) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("register cluster: %w", err)
	}
	if err := validateClusterBody(msg.ClusterId, msg.BaseDomain, msg.PublicEndpoints, msg.MetadataUri); err != nil {
		return fmt.Errorf("register cluster: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgUpdateCluster.
func (msg MsgUpdateCluster) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("update cluster: %w", err)
	}
	if err := validateClusterBody(msg.ClusterId, msg.BaseDomain, msg.PublicEndpoints, msg.MetadataUri); err != nil {
		return fmt.Errorf("update cluster: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgRetireCluster.
func (msg MsgRetireCluster) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("retire cluster: %w", err)
	}
	if err := ValidateID(msg.ClusterId); err != nil {
		return fmt.Errorf("retire cluster: %w", err)
	}
	return nil
}

func validateBindingList(bindings []Binding) error {
	if uint32(len(bindings)) > absoluteBindingCap {
		return fmt.Errorf("got %d bindings, max is %d", len(bindings), absoluteBindingCap)
	}
	services := make(map[string]struct{}, len(bindings))
	pubs := make(map[string]struct{}, len(bindings))
	for i, binding := range bindings {
		if err := ValidateBindingShape(binding); err != nil {
			return fmt.Errorf("binding %d: %w", i, err)
		}
		if _, ok := services[binding.Service]; ok {
			return fmt.Errorf("duplicate binding service %q", binding.Service)
		}
		services[binding.Service] = struct{}{}
		key := fmt.Sprintf("%x", binding.Pubkey)
		if _, ok := pubs[key]; ok {
			return fmt.Errorf("duplicate binding pubkey %s", key)
		}
		pubs[key] = struct{}{}
	}
	return nil
}

func validateClusterBody(id, domain string, endpoints []string, metadata string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if err := ValidateBaseDomain(domain); err != nil {
		return err
	}
	if err := ValidateEndpoints(endpoints, 1, absoluteEndpointCap); err != nil {
		return err
	}
	if err := ValidateMetadataURI(metadata); err != nil {
		return err
	}
	return nil
}

// ValidateBasic checks MsgClaimNodeName.
func (msg MsgClaimNodeName) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("claim node name: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("claim node name: %w", err)
	}
	if err := ValidateName(msg.Name); err != nil {
		return fmt.Errorf("claim node name: %w", err)
	}
	return nil
}

// ValidateBasic checks MsgReleaseNodeName.
func (msg MsgReleaseNodeName) ValidateBasic() error {
	if _, err := CanonicalAddress(msg.Operator); err != nil {
		return fmt.Errorf("release node name: %w", err)
	}
	if err := ValidateID(msg.NodeId); err != nil {
		return fmt.Errorf("release node name: %w", err)
	}
	return nil
}
