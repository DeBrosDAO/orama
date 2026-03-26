package shamir

import (
	"testing"
)

// ── GF(2^8) Field Tests ────────────────────────────────────────────────────

func TestExpTable_Cycle(t *testing.T) {
	// g^0 = 1, g^255 = 1 (cyclic group of order 255)
	if expTable[0] != 1 {
		t.Errorf("exp[0] = %d, want 1", expTable[0])
	}
	if expTable[255] != 1 {
		t.Errorf("exp[255] = %d, want 1", expTable[255])
	}
}

func TestExpTable_AllNonzeroAppear(t *testing.T) {
	var seen [256]bool
	for i := 0; i < 255; i++ {
		v := expTable[i]
		if seen[v] {
			t.Fatalf("duplicate value %d at index %d", v, i)
		}
		seen[v] = true
	}
	for v := 1; v < 256; v++ {
		if !seen[v] {
			t.Errorf("value %d not seen in exp[0..255]", v)
		}
	}
	if seen[0] {
		t.Error("zero should not appear in exp[0..254]")
	}
}

// Cross-platform test vectors from orama-vault/src/sss/test_cross_platform.zig
func TestExpTable_CrossPlatform(t *testing.T) {
	vectors := [][2]int{
		{0, 1}, {10, 114}, {20, 216}, {30, 102},
		{40, 106}, {50, 4}, {60, 211}, {70, 77},
		{80, 131}, {90, 179}, {100, 16}, {110, 97},
		{120, 47}, {130, 58}, {140, 250}, {150, 64},
		{160, 159}, {170, 188}, {180, 232}, {190, 197},
		{200, 27}, {210, 74}, {220, 198}, {230, 141},
		{240, 57}, {250, 108}, {254, 246}, {255, 1},
	}
	for _, v := range vectors {
		if got := expTable[v[0]]; got != byte(v[1]) {
			t.Errorf("exp[%d] = %d, want %d", v[0], got, v[1])
		}
	}
}

func TestMul_CrossPlatform(t *testing.T) {
	vectors := [][3]byte{
		{1, 1, 1}, {1, 2, 2}, {1, 3, 3},
		{1, 42, 42}, {1, 127, 127}, {1, 170, 170}, {1, 255, 255},
		{2, 1, 2}, {2, 2, 4}, {2, 3, 6},
		{2, 42, 84}, {2, 127, 254}, {2, 170, 79}, {2, 255, 229},
		{3, 1, 3}, {3, 2, 6}, {3, 3, 5},
		{3, 42, 126}, {3, 127, 129}, {3, 170, 229}, {3, 255, 26},
		{42, 1, 42}, {42, 2, 84}, {42, 3, 126},
		{42, 42, 40}, {42, 127, 82}, {42, 170, 244}, {42, 255, 142},
		{127, 1, 127}, {127, 2, 254}, {127, 3, 129},
		{127, 42, 82}, {127, 127, 137}, {127, 170, 173}, {127, 255, 118},
		{170, 1, 170}, {170, 2, 79}, {170, 3, 229},
		{170, 42, 244}, {170, 127, 173}, {170, 170, 178}, {170, 255, 235},
		{255, 1, 255}, {255, 2, 229}, {255, 3, 26},
		{255, 42, 142}, {255, 127, 118}, {255, 170, 235}, {255, 255, 19},
	}
	for _, v := range vectors {
		if got := Mul(v[0], v[1]); got != v[2] {
			t.Errorf("Mul(%d, %d) = %d, want %d", v[0], v[1], got, v[2])
		}
	}
}

func TestMul_Zero(t *testing.T) {
	for a := 0; a < 256; a++ {
		if Mul(byte(a), 0) != 0 {
			t.Errorf("Mul(%d, 0) != 0", a)
		}
		if Mul(0, byte(a)) != 0 {
			t.Errorf("Mul(0, %d) != 0", a)
		}
	}
}

func TestMul_Identity(t *testing.T) {
	for a := 0; a < 256; a++ {
		if Mul(byte(a), 1) != byte(a) {
			t.Errorf("Mul(%d, 1) = %d", a, Mul(byte(a), 1))
		}
	}
}

func TestMul_Commutative(t *testing.T) {
	for a := 1; a < 256; a += 7 {
		for b := 1; b < 256; b += 11 {
			ab := Mul(byte(a), byte(b))
			ba := Mul(byte(b), byte(a))
			if ab != ba {
				t.Errorf("Mul(%d,%d)=%d != Mul(%d,%d)=%d", a, b, ab, b, a, ba)
			}
		}
	}
}

func TestInv_CrossPlatform(t *testing.T) {
	vectors := [][2]byte{
		{1, 1}, {2, 141}, {3, 246}, {5, 82},
		{7, 209}, {16, 116}, {42, 152}, {127, 130},
		{128, 131}, {170, 18}, {200, 169}, {255, 28},
	}
	for _, v := range vectors {
		got, err := Inv(v[0])
		if err != nil {
			t.Errorf("Inv(%d) returned error: %v", v[0], err)
			continue
		}
		if got != v[1] {
			t.Errorf("Inv(%d) = %d, want %d", v[0], got, v[1])
		}
	}
}

func TestInv_SelfInverse(t *testing.T) {
	for a := 1; a < 256; a++ {
		inv1, _ := Inv(byte(a))
		inv2, _ := Inv(inv1)
		if inv2 != byte(a) {
			t.Errorf("Inv(Inv(%d)) = %d, want %d", a, inv2, a)
		}
	}
}

func TestInv_Product(t *testing.T) {
	for a := 1; a < 256; a++ {
		inv1, _ := Inv(byte(a))
		if Mul(byte(a), inv1) != 1 {
			t.Errorf("Mul(%d, Inv(%d)) != 1", a, a)
		}
	}
}

func TestInv_Zero(t *testing.T) {
	_, err := Inv(0)
	if err != ErrDivisionByZero {
		t.Errorf("Inv(0) should return ErrDivisionByZero, got %v", err)
	}
}

func TestDiv_CrossPlatform(t *testing.T) {
	vectors := [][3]byte{
		{1, 1, 1}, {1, 2, 141}, {1, 3, 246},
		{1, 42, 152}, {1, 127, 130}, {1, 170, 18}, {1, 255, 28},
		{2, 1, 2}, {2, 2, 1}, {2, 3, 247},
		{3, 1, 3}, {3, 2, 140}, {3, 3, 1},
		{42, 1, 42}, {42, 2, 21}, {42, 42, 1},
		{127, 1, 127}, {127, 127, 1},
		{170, 1, 170}, {170, 170, 1},
		{255, 1, 255}, {255, 255, 1},
	}
	for _, v := range vectors {
		got, err := Div(v[0], v[1])
		if err != nil {
			t.Errorf("Div(%d, %d) returned error: %v", v[0], v[1], err)
			continue
		}
		if got != v[2] {
			t.Errorf("Div(%d, %d) = %d, want %d", v[0], v[1], got, v[2])
		}
	}
}

func TestDiv_ByZero(t *testing.T) {
	_, err := Div(42, 0)
	if err != ErrDivisionByZero {
		t.Errorf("Div(42, 0) should return ErrDivisionByZero, got %v", err)
	}
}

// ── Polynomial evaluation ──────────────────────────────────────────────────

func TestEvaluatePolynomial_CrossPlatform(t *testing.T) {
	// p(x) = 42 + 5x + 7x^2
	coeffs0 := []byte{42, 5, 7}
	vectors0 := [][2]byte{
		{1, 40}, {2, 60}, {3, 62}, {4, 78},
		{5, 76}, {10, 207}, {100, 214}, {255, 125},
	}
	for _, v := range vectors0 {
		if got := evaluatePolynomial(coeffs0, v[0]); got != v[1] {
			t.Errorf("p(%d) = %d, want %d [coeffs: 42,5,7]", v[0], got, v[1])
		}
	}

	// p(x) = 0 + 0xAB*x + 0xCD*x^2
	coeffs1 := []byte{0, 0xAB, 0xCD}
	vectors1 := [][2]byte{
		{1, 102}, {3, 50}, {5, 152}, {7, 204}, {200, 96},
	}
	for _, v := range vectors1 {
		if got := evaluatePolynomial(coeffs1, v[0]); got != v[1] {
			t.Errorf("p(%d) = %d, want %d [coeffs: 0,AB,CD]", v[0], got, v[1])
		}
	}

	// p(x) = 0xFF (constant)
	coeffs2 := []byte{0xFF}
	for _, x := range []byte{1, 2, 255} {
		if got := evaluatePolynomial(coeffs2, x); got != 0xFF {
			t.Errorf("constant p(%d) = %d, want 255", x, got)
		}
	}

	// p(x) = 128 + 64x + 32x^2 + 16x^3
	coeffs3 := []byte{128, 64, 32, 16}
	vectors3 := [][2]byte{
		{1, 240}, {2, 0}, {3, 16}, {4, 193}, {5, 234},
	}
	for _, v := range vectors3 {
		if got := evaluatePolynomial(coeffs3, v[0]); got != v[1] {
			t.Errorf("p(%d) = %d, want %d [coeffs: 128,64,32,16]", v[0], got, v[1])
		}
	}
}

// ── Lagrange combine (cross-platform) ─────────────────────────────────────

func TestCombine_CrossPlatform_SingleByte(t *testing.T) {
	// p(x) = 42 + 5x + 7x^2, secret = 42
	// Shares: (1,40) (2,60) (3,62) (4,78) (5,76)
	allShares := []Share{
		{X: 1, Y: []byte{40}},
		{X: 2, Y: []byte{60}},
		{X: 3, Y: []byte{62}},
		{X: 4, Y: []byte{78}},
		{X: 5, Y: []byte{76}},
	}

	subsets := [][]int{
		{0, 1, 2}, // {1,2,3}
		{0, 2, 4}, // {1,3,5}
		{1, 3, 4}, // {2,4,5}
		{2, 3, 4}, // {3,4,5}
	}

	for _, subset := range subsets {
		shares := make([]Share, len(subset))
		for i, idx := range subset {
			shares[i] = allShares[idx]
		}
		result, err := Combine(shares)
		if err != nil {
			t.Fatalf("Combine failed for subset %v: %v", subset, err)
		}
		if result[0] != 42 {
			t.Errorf("Combine(subset %v) = %d, want 42", subset, result[0])
		}
	}
}

func TestCombine_CrossPlatform_MultiByte(t *testing.T) {
	// 2-byte secret [42, 0]
	// byte0: 42 + 5x + 7x^2 → shares at x=1,3,5: 40, 62, 76
	// byte1: 0 + 0xAB*x + 0xCD*x^2 → shares at x=1,3,5: 102, 50, 152
	shares := []Share{
		{X: 1, Y: []byte{40, 102}},
		{X: 3, Y: []byte{62, 50}},
		{X: 5, Y: []byte{76, 152}},
	}
	result, err := Combine(shares)
	if err != nil {
		t.Fatalf("Combine failed: %v", err)
	}
	if result[0] != 42 || result[1] != 0 {
		t.Errorf("Combine = %v, want [42, 0]", result)
	}
}

// ── Split/Combine round-trip ──────────────────────────────────────────────

func TestSplitCombine_RoundTrip_2of3(t *testing.T) {
	secret := []byte("hello world")
	shares, err := Split(secret, 3, 2)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(shares) != 3 {
		t.Fatalf("got %d shares, want 3", len(shares))
	}

	// Any 2 shares should reconstruct
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			result, err := Combine([]Share{shares[i], shares[j]})
			if err != nil {
				t.Fatalf("Combine(%d,%d): %v", i, j, err)
			}
			if string(result) != string(secret) {
				t.Errorf("Combine(%d,%d) = %q, want %q", i, j, result, secret)
			}
		}
	}
}

func TestSplitCombine_RoundTrip_3of5(t *testing.T) {
	secret := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	shares, err := Split(secret, 5, 3)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	// All C(5,3)=10 subsets should reconstruct
	count := 0
	for i := 0; i < 5; i++ {
		for j := i + 1; j < 5; j++ {
			for k := j + 1; k < 5; k++ {
				result, err := Combine([]Share{shares[i], shares[j], shares[k]})
				if err != nil {
					t.Fatalf("Combine(%d,%d,%d): %v", i, j, k, err)
				}
				for idx := range secret {
					if result[idx] != secret[idx] {
						t.Errorf("Combine(%d,%d,%d)[%d] = %d, want %d", i, j, k, idx, result[idx], secret[idx])
					}
				}
				count++
			}
		}
	}
	if count != 10 {
		t.Errorf("tested %d subsets, want 10", count)
	}
}

func TestSplitCombine_RoundTrip_LargeSecret(t *testing.T) {
	secret := make([]byte, 256)
	for i := range secret {
		secret[i] = byte(i)
	}
	shares, err := Split(secret, 10, 5)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	// Use first 5 shares
	result, err := Combine(shares[:5])
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}
	for i := range secret {
		if result[i] != secret[i] {
			t.Errorf("result[%d] = %d, want %d", i, result[i], secret[i])
			break
		}
	}
}

func TestSplitCombine_AllZeros(t *testing.T) {
	secret := make([]byte, 10)
	shares, err := Split(secret, 5, 3)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	result, err := Combine(shares[:3])
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}
	for i, b := range result {
		if b != 0 {
			t.Errorf("result[%d] = %d, want 0", i, b)
		}
	}
}

func TestSplitCombine_AllOnes(t *testing.T) {
	secret := make([]byte, 10)
	for i := range secret {
		secret[i] = 0xFF
	}
	shares, err := Split(secret, 5, 3)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	result, err := Combine(shares[:3])
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}
	for i, b := range result {
		if b != 0xFF {
			t.Errorf("result[%d] = %d, want 255", i, b)
		}
	}
}

// ── Share indices ─────────────────────────────────────────────────────────

func TestSplit_ShareIndices(t *testing.T) {
	shares, err := Split([]byte{42}, 5, 3)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	for i, s := range shares {
		if s.X != byte(i+1) {
			t.Errorf("shares[%d].X = %d, want %d", i, s.X, i+1)
		}
	}
}

// ── Error cases ───────────────────────────────────────────────────────────

func TestSplit_Errors(t *testing.T) {
	tests := []struct {
		name   string
		secret []byte
		n, k   int
		want   error
	}{
		{"k < 2", []byte{1}, 3, 1, ErrThresholdTooSmall},
		{"n < k", []byte{1}, 2, 3, ErrShareCountTooSmall},
		{"n > 255", []byte{1}, 256, 3, ErrTooManyShares},
		{"empty secret", []byte{}, 3, 2, ErrEmptySecret},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Split(tt.secret, tt.n, tt.k)
			if err != tt.want {
				t.Errorf("Split() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCombine_Errors(t *testing.T) {
	t.Run("not enough shares", func(t *testing.T) {
		_, err := Combine([]Share{{X: 1, Y: []byte{1}}})
		if err != ErrNotEnoughShares {
			t.Errorf("got %v, want ErrNotEnoughShares", err)
		}
	})

	t.Run("zero index", func(t *testing.T) {
		_, err := Combine([]Share{
			{X: 0, Y: []byte{1}},
			{X: 1, Y: []byte{2}},
		})
		if err != ErrZeroShareIndex {
			t.Errorf("got %v, want ErrZeroShareIndex", err)
		}
	})

	t.Run("mismatched lengths", func(t *testing.T) {
		_, err := Combine([]Share{
			{X: 1, Y: []byte{1, 2}},
			{X: 2, Y: []byte{3}},
		})
		if err != ErrMismatchedShareLen {
			t.Errorf("got %v, want ErrMismatchedShareLen", err)
		}
	})

	t.Run("duplicate indices", func(t *testing.T) {
		_, err := Combine([]Share{
			{X: 1, Y: []byte{1}},
			{X: 1, Y: []byte{2}},
		})
		if err != ErrDuplicateShareIndex {
			t.Errorf("got %v, want ErrDuplicateShareIndex", err)
		}
	})
}

// ── Threshold / Quorum ────────────────────────────────────────────────────

func TestAdaptiveThreshold(t *testing.T) {
	tests := [][2]int{
		{1, 3}, {2, 3}, {3, 3}, {5, 3}, {8, 3}, {9, 3},
		{10, 3}, {12, 4}, {15, 5}, {30, 10}, {100, 33},
	}
	for _, tt := range tests {
		if got := AdaptiveThreshold(tt[0]); got != tt[1] {
			t.Errorf("AdaptiveThreshold(%d) = %d, want %d", tt[0], got, tt[1])
		}
	}
}

func TestWriteQuorum(t *testing.T) {
	tests := [][2]int{
		{0, 0}, {1, 1}, {2, 2}, {3, 2}, {4, 3}, {5, 4},
		{6, 4}, {10, 7}, {14, 10}, {100, 67},
	}
	for _, tt := range tests {
		if got := WriteQuorum(tt[0]); got != tt[1] {
			t.Errorf("WriteQuorum(%d) = %d, want %d", tt[0], got, tt[1])
		}
	}
}
