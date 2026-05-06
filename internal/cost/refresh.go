// Package cost — refresh.go handles loading and decoding pricing.json files.
//
// The embedded pricing.json is the authoritative default. The user may
// optionally supply ~/.agentdeck/pricing.json (path from config) to override
// individual model rates or add new models. Override semantics: per-model
// entries in the override file replace (not merge) the corresponding embedded
// entry. Models absent from the override file keep their embedded values.
package cost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// decodePricingJSON parses raw JSON bytes into a PricingFile.
// Returns an error if the bytes are not valid JSON or the version is
// unsupported (currently only version=1 is accepted).
func decodePricingJSON(data []byte) (*PricingFile, error) {
	var pf PricingFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("refresh: unmarshal: %w", err)
	}
	if pf.Version != 1 {
		return nil, fmt.Errorf("refresh: unsupported pricing schema version %d (expected 1)", pf.Version)
	}
	if pf.Models == nil {
		pf.Models = make(map[string]PerTokenRates)
	}
	return &pf, nil
}

// loadOverride reads and decodes a user-supplied pricing.json from path.
// It returns (nil, nil) if the file does not exist (not an error — the user
// simply hasn't supplied an override). Any other I/O or decode error is
// returned as-is.
func loadOverride(path string) (*PricingFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Override file is optional; absence is normal.
			return nil, nil
		}
		return nil, fmt.Errorf("refresh: read override %q: %w", path, err)
	}
	pf, err := decodePricingJSON(data)
	if err != nil {
		return nil, fmt.Errorf("refresh: decode override %q: %w", path, err)
	}
	return pf, nil
}
