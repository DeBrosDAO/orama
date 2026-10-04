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
	// bounded by eviction rather than by the kernel. The bound is per DMap, so
	// the namespace's tenant-facing cache is ONE DMap (pkg/gateway/handlers/cache
	// namespace_dmap.go): tenant-chosen dmap names must never become Olric DMaps,
	// or a tenant could multiply this bound past the MemoryMax.
	DMapMaxInuseBytes = 256 << 20
)
