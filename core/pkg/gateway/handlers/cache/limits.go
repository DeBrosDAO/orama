package cache

// Olric refuses an entry its table cannot hold, and a key of 256 bytes or
// more. It says so to the gateway as a protocol error code that the gateway's
// Olric client never registers (Olric registers them when it starts a server),
// so the refusal arrives as an error nothing recognises and the tenant saw a
// 500 "cache unavailable" for a request that was simply too big. The gateway
// therefore enforces the limits itself, before the put.
//
// The instance configs (pkg/olric, the host olric config) set no table size, so
// Olric runs with its default: internal/kvstore defaultTableSize in
// olric v0.7.4. Change a table size there and change OlricTableSizeBytes.
const (
	// OlricTableSizeBytes is the size of one Olric kvstore table, the largest
	// entry a cluster member can hold.
	OlricTableSizeBytes = 1 << 20

	// olricEntryOverheadBytes is what a table spends on an entry besides its key
	// and value: key length (1), TTL, timestamp and last access (8 each) and
	// value length (4). internal/kvstore/table MetadataLength.
	olricEntryOverheadBytes = 29

	// MaxKeyBytes is the longest key Olric stores: its key length is one byte and
	// a key of 256 bytes or more is refused. internal/kvstore/table MaxKeyLength
	// is 256.
	MaxKeyBytes = 255
)

// entryFitsTable reports whether a table can hold key and the stored value.
// Olric allocates a table of OlricTableSizeBytes and needs the entry to leave
// room in it, so an entry that fills the table exactly does not fit.
func entryFitsTable(key string, stored []byte) bool {
	return len(key)+len(stored)+olricEntryOverheadBytes < OlricTableSizeBytes
}
