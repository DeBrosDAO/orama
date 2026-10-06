package cache

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	olriclib "github.com/olric-data/olric"
)

// A namespace's whole cache lives in ONE Olric DMap, and the tenant's dmap name
// is folded into each key.
//
// Olric bounds memory per DMap (dmaps.maxInuse, pkg/olric DMapMaxInuseBytes),
// and a tenant names its own dmaps. One Olric DMap per tenant dmap therefore
// left a namespace's Olric (orama-namespace-olric@<ns>, systemd MemoryMax 2G)
// with no bound at all: eight dmaps at the 256 MiB limit already reach the
// MemoryMax, and the kernel's OOM kill loses every key. One DMap per namespace
// puts the whole cache under the one LRU limit, however many dmaps the tenant
// makes.

// namespaceDMapName is the name of the Olric DMap holding a namespace's cache.
// It cannot collide with the serverless cache (":serverless_cache:<ns>") or
// the trigger DMaps.
func namespaceDMapName(namespace string) string {
	return "gateway_cache:" + namespace
}

// dmapKeyPrefix is what every key of a tenant dmap starts with in the
// namespace's DMap: the dmap name's length in bytes, a colon, the name. The
// length makes the encoding unambiguous: reading the digits up to the colon
// says where the name ends, whatever bytes the name and the key contain, so no
// key of one dmap can be read as a key of another, and no dmap's prefix is a
// prefix of another's.
func dmapKeyPrefix(dmap string) string {
	return strconv.Itoa(len(dmap)) + ":" + dmap
}

// maxKeyBytesIn is the longest key a tenant may use in dmap: Olric's key limit
// less the prefix the dmap takes. Zero or less means the dmap name alone
// leaves no room for a key.
func maxKeyBytesIn(dmap string) int {
	return MaxKeyBytes - len(dmapKeyPrefix(dmap))
}

// foldKey is the Olric key of key in dmap. ok is false when it would be longer
// than Olric stores, which means no such entry can exist.
func foldKey(dmap, key string) (folded string, ok bool) {
	folded = dmapKeyPrefix(dmap) + key
	return folded, len(folded) <= MaxKeyBytes
}

// unfoldKey is the tenant's key of an Olric key of dmap; ok is false for a key
// of another dmap.
func unfoldKey(dmap, folded string) (key string, ok bool) {
	return strings.CutPrefix(folded, dmapKeyPrefix(dmap))
}

// keyTooLargeMessage tells the tenant the limit that applies to this dmap: the
// key shares Olric's 255 bytes with the dmap name and its length prefix.
func keyTooLargeMessage(dmap string) string {
	if max := maxKeyBytesIn(dmap); max > 0 {
		return fmt.Sprintf("key too large: a dmap name and its key share %d bytes, and this dmap name takes %d of them (its length and a colon included), so a key in this dmap is at most %d bytes",
			MaxKeyBytes, len(dmapKeyPrefix(dmap)), max)
	}
	return fmt.Sprintf("dmap name too large: a dmap name and its key share %d bytes and this name leaves no room for a key; use a shorter dmap name", MaxKeyBytes)
}

// namespaceCache opens the calling namespace's cache DMap, answering the
// request itself (and returning false) when it cannot.
func (h *CacheHandlers) namespaceCache(ctx context.Context, w http.ResponseWriter) (olriclib.DMap, bool) {
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		writeError(w, http.StatusUnauthorized, "namespace not found in context")
		return nil, false
	}
	dm, err := h.olricClient.GetClient().NewDMap(namespaceDMapName(namespace))
	if err != nil {
		h.writeCacheFailure(w, http.StatusInternalServerError, "failed to create DMap", err)
		return nil, false
	}
	return dm, true
}
