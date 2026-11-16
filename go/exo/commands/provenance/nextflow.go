package provenance

import (
	"github.com/Genentech/exohub/go/exo/internal/provenance/importer/nextflow"
)

// init registers the "nextflow" import format (design D8), so `exo
// provenance import nextflow [<run>]` reads a Nextflow lineage store.
func init() {
	RegisterFormat("nextflow", nextflow.Read)
}
