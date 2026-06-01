package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/handlename/reviewer"
	"github.com/rs/zerolog/log"
)

type Serve struct {
	Input  string `arg:"" help:"Input markdown spec file path." type:"existingfile"`
	Output string `short:"o" help:"Output HTML spec file path (defaults to same folder as input)."`
	Port   int    `short:"p" default:"5500" help:"Target port for HTTP server."`
	NoOpen bool   `name:"no-open" help:"Do not automatically open the default web browser."`
}

func (s *Serve) Run(c *Context) error {
	mdContent, err := os.ReadFile(s.Input)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	htmlContent, err := reviewer.RenderSpec(mdContent)
	if err != nil {
		return fmt.Errorf("failed to render spec: %w", err)
	}

	outPath := s.Output
	if outPath == "" {
		dir := filepath.Dir(s.Input)
		base := filepath.Base(s.Input)
		ext := filepath.Ext(base)
		outPath = filepath.Join(dir, fmt.Sprintf("%s.html", strings.TrimSuffix(base, ext)))
	}

	if err := os.WriteFile(outPath, htmlContent, 0644); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	log.Info().Msgf("Compiled review spec saved to %s", outPath)

	return reviewer.StartReviewServer(c.Ctx, htmlContent, s.Input, s.Port, s.NoOpen, nil)
}
