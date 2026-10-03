package olric

// Every Olric instance (the index and each namespace) bounds its cache with
// LRU eviction. Olric's default is no eviction and no memory limit, so a cache
// in use grew until systemd's MemoryMax (2G, core/systemd) killed it and every
// key went with it.
const (
	// DMapEvictionPolicy evicts the least recently used keys of a DMap once it
	// holds DMapMaxInuseBytes on a node.
	DMapEvictionPolicy = "LRU"
	// DMapMaxInuseBytes is the memory one DMap may hold on one node before
	// eviction starts (Olric splits it across the partitions the node owns).
	// Well under the unit's 2G MemoryMax, so a namespace using a few DMaps is
	// bounded by eviction rather than by the kernel.
	DMapMaxInuseBytes = 256 << 20
)
