package ipfs

import "testing"

func TestCanonicalCID(t *testing.T) {
	const v0 = "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV"
	const v1 = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	for _, ok := range []string{v0, v1} {
		if err := CanonicalCID(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for name, bad := range map[string]string{
		"empty":            "",
		"not a cid":        "hello",
		"path":             "/ipfs/" + v0,
		"upper-cased v1":   "BAFYBEIGDYRZT5SFP7UDM7HU76UH7Y26NF3EFUYLQABF3OCLGTQY55FBZDI",
		"padding":          v0 + "=",
		"trailing newline": v0 + "\n",
		"nul":              v0 + "\x00",
	} {
		if err := CanonicalCID(bad); err == nil {
			t.Errorf("%s: %q accepted", name, bad)
		}
	}
}
