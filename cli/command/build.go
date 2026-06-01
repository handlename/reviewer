package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/handlename/reviewer"
	"github.com/rs/zerolog/log"
)

type Build struct {
	Input  string `arg:"" help:"Input markdown spec file path." type:"existingfile"`
	Output string `short:"o" help:"Output HTML spec file path (defaults to same folder as input)."`
}

func (b *Build) Run(c *Context) error {
	mdContent, err := os.ReadFile(b.Input)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	htmlContent, err := reviewer.RenderSpec(mdContent)
	if err != nil {
		return fmt.Errorf("failed to render spec: %w", err)
	}

	outPath := b.Output
	if outPath == "" {
		dir := filepath.Dir(b.Input)
		base := filepath.Base(b.Input)
		ext := filepath.Ext(base)
		outPath = filepath.Join(dir, fmt.Sprintf("%s.html", strings.TrimSuffix(base, ext)))
	}

	if err := os.WriteFile(outPath, htmlContent, 0644); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	log.Info().Msgf("Successfully compiled static HTML to %s", outPath)
	return nil
}
