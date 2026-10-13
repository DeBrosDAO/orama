package main

import (
	"bytes"
	"fmt"
	"os"
)

// generator renders one generated appendix from the code.
type generator func(b *Book) ([]byte, error)

// generators maps a generated appendix file to its generator. Appendix D
// (the CLI reference) is rendered by core/cmd/orama's book_reference_test.go,
// which writes it and fails when it is stale; see core/Makefile `docs`.
var generators = map[string]generator{
	"appendices/a-port-map.md":                   genPorts,
	"appendices/b-schema.md":                     genSchema,
	"appendices/c-gateway-routes.md":             genRoutes,
	"appendices/e-chain-messages-and-queries.md": genChainMessages,
	"appendices/f-configuration.md":              genConfiguration,
	"appendices/h-known-gaps.md":                 genKnownGaps,
}

// generatedHeader opens every generated appendix.
func generatedHeader(title, source string) string {
	return fmt.Sprintf("# %s\n\n> **At a glance.**\n>\n> - **Generated** from %s by `make whitepaper-gen`. Do not edit by hand: the gate fails when this file and the code disagree.\n\n", title, source)
}

func titleOf(b *Book, file string) string {
	for _, a := range b.Manifest.Appendices {
		if a.File == file {
			return a.Title
		}
	}
	return file
}

// writeGenerated renders every generated appendix to disk.
func writeGenerated(b *Book) error {
	if len(b.Manifest.Appendices) == 0 {
		return nil
	}
	for file, gen := range generators {
		out, err := gen(b)
		if err != nil {
			return fmt.Errorf("failed to generate %s: %w", file, err)
		}
		if err := os.WriteFile(b.Path(file), out, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file, err)
		}
		fmt.Printf("generated %s\n", file)
	}
	return nil
}

// checkGenerated is the generated-content gate.
func checkGenerated(b *Book) []problem {
	if len(b.Manifest.Appendices) == 0 {
		return nil
	}
	var probs []problem
	for file, gen := range generators {
		want, err := gen(b)
		if err != nil {
			probs = append(probs, problem{gate: "generated", file: file, msg: err.Error()})
			continue
		}
		got, err := os.ReadFile(b.Path(file))
		if err != nil || !bytes.Equal(got, want) {
			probs = append(probs, problem{gate: "generated", file: file, msg: "is out of date with the code: run make whitepaper-gen"})
		}
	}
	return probs
}
