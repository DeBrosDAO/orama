package siw

import "testing"

func TestIsWalletAddress(t *testing.T) {
	cases := map[string]bool{
		"0x852ad3DBB4A7da8b1D9C75Da2a7D35801a5A987e":   true, // checksummed
		"0x852ad3dbb4a7da8b1d9c75da2a7d35801a5a987e":   true, // lowercase is still a wallet
		"0X852AD3DBB4A7DA8B1D9C75DA2A7D35801A5A987E":   true,
		"9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM": true, // a Solana public key
		"":             false,
		"not-a-wallet": false,
		"0x123":        false,
		"0x852ad3dbb4a7da8b1d9c75da2a7d35801a5a987g":   false, // not hex
		"0x852ad3dbb4a7da8b1d9c75da2a7d35801a5a987e00": false, // 21 bytes
		"9WzDXwBbmkg8ZTbN":                             false, // too short for a key
		"0OIl" + "abcdefghijkmnopqrstuvwxyz1234":       false, // not base58
	}
	for in, want := range cases {
		if got := IsWalletAddress(in); got != want {
			t.Errorf("IsWalletAddress(%q) = %v, want %v", in, got, want)
		}
	}
}
