// Package registry assembles the extractor set used by the CLI.
package registry

import (
	"github.com/fenixstarlord/replicant/internal/cliconfig"
	"github.com/fenixstarlord/replicant/internal/extract"
	"github.com/fenixstarlord/replicant/internal/extract/ale"
	"github.com/fenixstarlord/replicant/internal/extract/arri"
	"github.com/fenixstarlord/replicant/internal/extract/braw"
	"github.com/fenixstarlord/replicant/internal/extract/bwf"
	"github.com/fenixstarlord/replicant/internal/extract/ffprobe"
	"github.com/fenixstarlord/replicant/internal/extract/red"
	"github.com/fenixstarlord/replicant/internal/extract/sony"
	"github.com/fenixstarlord/replicant/internal/scan"
)

// All returns every extractor, configured from tools and bound to the
// scan's entries where needed (ALE lookup). Order is informational; the
// runner merges by priority.
func All(root string, entries []scan.Entry, tools cliconfig.Tools) []extract.Extractor {
	return []extract.Extractor{
		&arri.Extractor{Path: tools.ArtCmd, Args: tools.ArtCmdArgs},
		&red.Extractor{Path: tools.REDline},
		bwf.Extractor{},
		sony.Extractor{},
		braw.Extractor{},
		ale.NewFromEntries(root, entries),
		&ffprobe.Extractor{Path: tools.FFprobe},
	}
}
