package types

const (
	EventTypeRegisterOperator = "register_operator"
	EventTypeRegisterNode     = "register_node"
	EventTypeUpdateNode       = "update_node"
	EventTypeRetireNode       = "retire_node"
	EventTypeBondNode         = "bond_node"
	EventTypeUnbondNode       = "unbond_node"
	EventTypeDeclareCapacity  = "declare_capacity"
	EventTypeFundHotKey       = "fund_hot_key"
	EventTypeRegisterCluster  = "register_cluster"
	EventTypeUpdateCluster    = "update_cluster"
	EventTypeRetireCluster    = "retire_cluster"
	EventTypeSlash            = "slash_node"
	EventTypeJail             = "jail_node"
	EventTypeUnjail           = "unjail_node"
	EventTypeTombstone        = "tombstone_node"

	AttributeOperator = "operator"
	AttributeNodeID   = "node_id"
	AttributeCluster  = "cluster_id"
	AttributeRole     = "role"
	AttributeAmount   = "amount"
	AttributeHotKey   = "hot_key"
)
