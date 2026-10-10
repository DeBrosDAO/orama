package sandbox

import (
	"math/rand"
)

var adjectives = []string{
	"swift", "bright", "calm", "dark", "eager",
	"fair", "gold", "hazy", "iron", "jade",
	"keen", "lush", "mild", "neat", "opal",
	"pure", "raw", "sage", "teal", "warm",
}

var nouns = []string{
	"falcon", "beacon", "cedar", "delta", "ember",
	"frost", "grove", "haven", "ivory", "jewel",
	"knot", "latch", "maple", "nexus", "orbit",
	"prism", "reef", "spark", "tide", "vault",
}

// GenerateName produces a random adjective-noun name like "swift-falcon".
func GenerateName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return adj + "-" + noun
}
